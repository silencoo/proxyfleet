package pool

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/monitor"
	"github.com/silencoo/proxyfleet/internal/trafficlog"
)

func newTrafficHealthFixture(t *testing.T) (*poolOutbound, *memberState, *monitor.Manager) {
	t.Helper()
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	mgr, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	s := acquireSharedState("traffic-health")
	h := mgr.Register(monitor.NodeInfo{Tag: s.tag})
	s.attachEntry(h)
	h.RestoreHealthState(monitor.PersistedHealthState{InitialCheckDone: true, Available: false, LastError: "old timeout", ConsecutiveProbeFailures: 2})
	member := &memberState{tag: s.tag, shared: s, entry: h}
	p := newIndexedTestPool(member, normalizeOptions(Options{}))
	t.Cleanup(func() { _ = p.Close() })
	return p, member, mgr
}

func TestTrafficHealthRequiresResponseAndIgnoresNormalEOF(t *testing.T) {
	p, member, mgr := newTrafficHealthFixture(t)
	client, peer := net.Pipe()
	defer peer.Close()
	member.outbound = &probeTransportOutbound{dial: func(context.Context) (net.Conn, error) { return client, nil }}
	conn, err := p.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.test:443"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if mgr.Snapshot()[0].SuccessCount != 0 || mgr.Snapshot()[0].Available {
		t.Fatal("dial fabricated health")
	}
	requestRead := make(chan struct{})
	respond := make(chan struct{})
	go func() {
		defer peer.Close()
		var request [1]byte
		_, _ = io.ReadFull(peer, request[:])
		close(requestRead)
		<-respond
		_, _ = peer.Write([]byte("ok"))
	}()
	if _, err := conn.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	<-requestRead
	if mgr.Snapshot()[0].SuccessCount != 0 {
		t.Fatal("a write fabricated health")
	}
	close(respond)
	response, err := io.ReadAll(conn)
	if err != nil || string(response) != "ok" {
		t.Fatalf("response=%q err=%v", response, err)
	}
	s := mgr.Snapshot()[0]
	if !s.Available || s.SuccessCount != 1 || s.FailureCount != 0 || s.LastPassiveSuccess.IsZero() {
		t.Fatalf("response/normal EOF misclassified: %+v", s)
	}
}

func TestTrafficObservationCountsOnceAndUpdatesLogOutcome(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "success"}[success], func(t *testing.T) {
			p, member, mgr := newTrafficHealthFixture(t)
			traffic := &trafficSession{event: trafficlog.Event{ErrorCategory: "unconfirmed"}}
			observation := p.newTrafficObservation(context.Background(), member, "example.test", time.Now(), traffic)
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); observation.observe(success, io.EOF) }()
			}
			wg.Wait()
			s := mgr.Snapshot()[0]
			if (s.SuccessCount == 1) != success || (s.FailureCount == 1) == success || s.SuccessCount+int64(s.FailureCount) != 1 {
				t.Fatalf("duplicate/wrong outcome: %+v", s)
			}
			if traffic.event.Success != success || (traffic.event.ErrorCategory == "") != success {
				t.Fatal("traffic log differs from health")
			}
		})
	}
}

func TestLocalCloseAndCancellationAreNotNodeFailures(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		p, member, mgr := newTrafficHealthFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		o := p.newTrafficObservation(ctx, member, "", time.Now(), nil)
		if canceled {
			cancel()
		} else {
			o.close()
		}
		o.observe(false, net.ErrClosed)
		o.observe(true, nil)
		cancel()
		s := mgr.Snapshot()[0]
		if s.SuccessCount != 0 || s.FailureCount != 0 || s.Available {
			t.Fatal("local cancellation/close changed health")
		}
	}
}

type healthPacketConn struct {
	net.PacketConn
	readErr error
}

func (c *healthPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, &net.UDPAddr{}, c.readErr
}
func (c *healthPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (*healthPacketConn) Close() error                                { return nil }

func TestUDPHealthRequiresReceivedDatagram(t *testing.T) {
	for _, failed := range []bool{false, true} {
		p, member, mgr := newTrafficHealthFixture(t)
		raw := &healthPacketConn{}
		if failed {
			raw.readErr = io.EOF
		}
		conn := &trackedPacketConn{PacketConn: raw, health: p.newTrafficObservation(context.Background(), member, "", time.Now(), nil), release: func() {}}
		_, _ = conn.WriteTo([]byte("query"), &net.UDPAddr{})
		if mgr.Snapshot()[0].SuccessCount != 0 {
			t.Fatal("UDP write confirmed health")
		}
		_, _, _ = conn.ReadFrom(make([]byte, 16))
		_ = conn.Close()
		s := mgr.Snapshot()[0]
		if s.Available == failed || (s.SuccessCount == 1) == failed || (s.FailureCount == 1) != failed {
			t.Fatalf("UDP result misclassified: %+v", s)
		}
	}
}
