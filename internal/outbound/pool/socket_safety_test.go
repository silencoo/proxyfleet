package pool

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

type resourceFailureOutbound struct {
	adapter.Outbound
	calls atomic.Int32
}

func (*resourceFailureOutbound) Network() []string { return []string{"tcp", "udp"} }
func (o *resourceFailureOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.calls.Add(1)
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("socket", syscall.ENOBUFS)}
}
func (o *resourceFailureOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	o.calls.Add(1)
	return nil, &net.OpError{Op: "listen", Net: "udp", Err: os.NewSyscallError("socket", syscall.ENOBUFS)}
}

func TestLocalResourceFailureStopsTCPAndUDPRetries(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			p := jobPoolFixture(t)
			p.resourceBackoff = new(resourceBackoff)
			p.options.RetryEnabled = true
			p.options.RetryAttempts = 3
			upstream := new(resourceFailureOutbound)
			for _, member := range p.members {
				member.outbound = upstream
				p.setMemberEligible(member, true)
			}
			dial := func() error {
				if network == "udp" {
					_, err := p.ListenPacket(context.Background(), M.ParseSocksaddr("127.0.0.1:53"))
					return err
				}
				_, err := p.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:80"))
				return err
			}
			if err := dial(); !errors.Is(err, syscall.ENOBUFS) {
				t.Fatalf("lost original local error: %v", err)
			}
			if upstream.calls.Load() != 1 {
				t.Fatalf("retried a local failure %d times", upstream.calls.Load())
			}
			if err := dial(); !errors.Is(err, errLocalResourceBackoff) || upstream.calls.Load() != 1 {
				t.Fatalf("backoff admitted another dial: %v, calls=%d", err, upstream.calls.Load())
			}
			for _, member := range p.members {
				if activeConnections(member) != 0 || !p.memberEligible(member, network) {
					t.Fatalf("local error changed eligibility or leaked active count: %s", member.tag)
				}
				if member.shared.failures != 0 || member.shared.blacklistedFast.Load() {
					t.Fatalf("local error penalized %s", member.tag)
				}
			}
		})
	}
}

func TestLocalResourceFailureDoesNotPublishNodeHealth(t *testing.T) {
	p := jobPoolFixture(t)
	p.resourceBackoff = new(resourceBackoff)
	member := p.members[0]
	p.recordFailure(member, syscall.ENOBUFS)
	p.recordProbeFailure(member, syscall.ENOBUFS)
	published := false
	applied := p.makeProbePublisher(member)(0, syscall.ENOBUFS, func(func()) bool {
		published = true
		return true
	})
	if applied || published || member.shared.failures != 0 || member.shared.blacklistedFast.Load() {
		t.Fatal("local pressure was published as failed node health")
	}
	if got := trafficErrorCategory(syscall.ENOBUFS); got != "local_resource" {
		t.Fatalf("missing resource-pressure diagnostic: %s", got)
	}
}

func TestLocalResourceFailurePreservesJobMeasurements(t *testing.T) {
	p := jobPoolFixture(t)
	p.resourceBackoff = new(resourceBackoff)
	upstream := new(resourceFailureOutbound)
	for _, member := range p.members {
		member.outbound = upstream
	}
	j := p.options.Jobs[0]
	key := p.options.JobPolicies[j.Name]
	before, _ := p.jobStore.Measurements(key)
	if _, err := p.RefreshJob(context.Background(), j.Name); err != nil {
		t.Fatal(err)
	}
	after, _ := p.jobStore.Measurements(key)
	for node, value := range before {
		if after[node] != value {
			t.Fatalf("local pressure overwrote target measurement for %s", node)
		}
	}
}

type heldSocketConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *heldSocketConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

type heldSocketOutbound struct {
	adapter.Outbound
	address string
	started chan *heldSocketConn
	resume  chan struct{}
	calls   atomic.Int32
}

func (*heldSocketOutbound) Network() []string { return []string{"tcp"} }
func (o *heldSocketOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.calls.Add(1)
	conn, err := net.DialTimeout("tcp", o.address, time.Second)
	if err != nil {
		return nil, err
	}
	held := &heldSocketConn{Conn: conn, closed: make(chan struct{})}
	o.started <- held
	<-o.resume // Deliberately ignore cancellation after allocating a real socket.
	return held, nil
}

func TestJobProbeTimeoutRetainsActualDialSlot(t *testing.T) {
	p := jobPoolFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	upstream := &heldSocketOutbound{address: server.Listener.Addr().String(), started: make(chan *heldSocketConn, 1), resume: make(chan struct{})}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(upstream.resume) }) }
	defer resume()
	member := p.members[0]
	member.outbound = upstream

	// Leave exactly one of the shared store's 16 slots for a real HTTP probe.
	for i := 0; i < 15; i++ {
		release, err := p.jobStore.AdmitProbe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	j := p.options.Jobs[0]
	j.TargetURL = server.URL
	j.Timeout = "200ms"
	result := make(chan jobs.Measurement, 1)
	go func() { result <- p.measureJobNode(context.Background(), j, member) }()
	var held *heldSocketConn
	select {
	case held = <-upstream.started:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP transport did not start its dial")
	}
	select {
	case m := <-result:
		if m.Success {
			t.Fatal("blocked dial unexpectedly succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not honor its timeout")
	}

	// Repeated caller timeouts must not admit more physical dial callbacks.
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		release, err := p.jobStore.AdmitProbe(ctx)
		cancel()
		if err == nil {
			release()
			t.Fatal("timed-out HTTP request released its still-running dial's slot")
		}
	}
	if upstream.calls.Load() != 1 || activeConnections(member) != 1 {
		t.Fatalf("unexpected retained work: calls=%d active=%d", upstream.calls.Load(), activeConnections(member))
	}
	resume()
	select {
	case <-held.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("late connection was not closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release, err := p.jobStore.AdmitProbe(ctx)
	if err != nil {
		t.Fatalf("completed dial did not return admission slot: %v", err)
	}
	release()
	if activeConnections(member) != 0 {
		t.Fatal("late connection leaked active accounting")
	}
}
