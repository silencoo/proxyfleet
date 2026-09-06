package pool

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/monitor"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

type probeTransportOutbound struct {
	adapter.Outbound
	dial func(context.Context) (net.Conn, error)
}

func (*probeTransportOutbound) Network() []string { return []string{"tcp"} }

func (o *probeTransportOutbound) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	return o.dial(ctx)
}

type probeNoDeadlineConn struct{ net.Conn }

func (c probeNoDeadlineConn) SetDeadline(time.Time) error { return nil }

func TestProbeWatchdogTimeoutUsesCooldownNotBlacklist(t *testing.T) {
	for _, stage := range []string{"dial", "tls", "response"} {
		t.Run(stage, func(t *testing.T) {
			ResetSharedStateStore()
			t.Cleanup(ResetSharedStateStore)
			state := acquireSharedState("timeout-" + stage)
			outbound := &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) {
				if stage == "dial" {
					<-ctx.Done()
					return nil, net.ErrClosed
				}
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					_, _ = io.Copy(io.Discard, server)
				}()
				return probeNoDeadlineConn{client}, nil
			}}
			member := &memberState{tag: state.tag, shared: state, outbound: outbound}
			proxyPool := newIndexedTestPool(member, Options{BlacklistDuration: 24 * time.Hour, TransientCooldown: time.Minute})
			t.Cleanup(func() { _ = proxyPool.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := proxyPool.probeMember(ctx, member, monitor.ProbeTarget{
				Host: "example.test", TLS: stage == "tls", Destination: M.ParseSocksaddrHostPort("example.test", 80),
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost watchdog deadline cause: %v", err)
			}
			// The transport callback is measurement-only; the publisher applies
			// the policy after the monitor validates generation/freshness.
			proxyPool.makeProbePublisher(member)(0, err, func(apply func()) bool { apply(); return true })
			if state.isBlacklisted(time.Now()) || !state.isCoolingDown(time.Now()) {
				t.Fatal("probe timeout did not use transient cooldown")
			}
		})
	}
}

func TestCanceledProbeDoesNotPenalizeNode(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	state := acquireSharedState("cancelled")
	outbound := &probeTransportOutbound{dial: func(context.Context) (net.Conn, error) {
		return nil, net.ErrClosed
	}}
	member := &memberState{tag: state.tag, shared: state, outbound: outbound}
	proxyPool := newIndexedTestPool(member, Options{})
	t.Cleanup(func() { _ = proxyPool.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := proxyPool.probeMember(ctx, member, monitor.ProbeTarget{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
	if state.isBlocked(time.Now()) || proxyPool.selectEligibleMember("") != member {
		t.Fatal("canceled probe penalized node")
	}
}

func TestActiveProbesDoNotRecordPassiveTraffic(t *testing.T) {
	t.Cleanup(ResetSharedStateStore)
	for _, success := range []bool{true, false} {
		ResetSharedStateStore()
		manager, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.test/check?ping=1"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(manager.Stop)
		handle := manager.Register(monitor.NodeInfo{Tag: "probe-source"})
		state := acquireSharedState("probe-source")
		state.attachEntry(handle)
		requestPaths := make(chan string, 1)
		outbound := &probeTransportOutbound{dial: func(context.Context) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				request, err := http.ReadRequest(bufio.NewReader(server))
				if err != nil {
					return
				}
				requestPaths <- request.RequestURI
				if success {
					_, _ = io.WriteString(server, "HTTP/1.1 204 No Content\r\n\r\n")
				} else {
					_, _ = io.WriteString(server, "HTTP/1.1 503 Service Unavailable\r\n\r\n")
				}
			}()
			return client, nil
		}}
		member := &memberState{tag: state.tag, shared: state, outbound: outbound, entry: handle}
		proxyPool := newIndexedTestPool(member, Options{})
		proxyPool.monitor = manager
		t.Cleanup(func() { _ = proxyPool.Close() })
		proxyPool.registerProbe(member)
		handle.RestoreHealthState(monitor.PersistedHealthState{EWMALatencyMs: 100})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = manager.Probe(ctx, state.tag)
		cancel()
		if (err == nil) != success {
			t.Fatalf("unexpected probe result: success=%v err=%v", success, err)
		}
		if path := <-requestPaths; path != "/check?ping=1" {
			t.Fatalf("probe ignored configured path: %q", path)
		}
		snapshot := manager.Snapshot()[0]
		if snapshot.SuccessCount != 0 || snapshot.FailureCount != 0 || !snapshot.LastPassiveSuccess.IsZero() || !snapshot.LastPassiveFailure.IsZero() {
			t.Fatalf("probe polluted traffic statistics: %+v", snapshot)
		}
		if snapshot.Available != success || snapshot.LastProbeAt.IsZero() || len(snapshot.Timeline) != 1 || snapshot.Timeline[0].Source != "probe" {
			t.Fatalf("missing independent probe diagnostics: %+v", snapshot)
		}
		if success && math.Abs(snapshot.EWMALatencyMs-(75+0.25*float64(snapshot.LastProbeLatency)/float64(time.Millisecond))) > 1e-9 {
			t.Fatalf("probe latency was applied more than once: %+v", snapshot)
		}
		if !success && (state.isBlacklisted(time.Now()) || !state.isCoolingDown(time.Now())) {
			t.Fatal("HTTP 503 did not use transient cooldown")
		}
		state.recordSuccessWithLatency(time.Millisecond)
		if snapshot := manager.Snapshot()[0]; snapshot.SuccessCount != 1 || snapshot.LastPassiveSuccess.IsZero() {
			t.Fatalf("real traffic was not counted: %+v", snapshot)
		}
	}
	ResetSharedStateStore()
}

func TestProbeConnectionWatchdogClosesBlockedConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	stop := watchProbeConnection(ctx, client)
	readDone := make(chan error, 1)
	go func() {
		var buf [1]byte
		_, err := client.Read(buf[:])
		readDone <- err
	}()
	cancel()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("blocked read unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("watchdog did not close blocked connection")
	}
	stop()
}

func TestUpgradeProbeConnVerifiesHTTPSCertificate(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	address := server.Listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	strictConn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeProbeConn(ctx, strictConn, monitor.ProbeTarget{Host: "localhost", TLS: true}); err == nil {
		strictConn.Close()
		t.Fatal("strict TLS probe accepted httptest's untrusted certificate")
	}
	strictConn.Close()

	insecureConn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := upgradeProbeConn(ctx, insecureConn, monitor.ProbeTarget{Host: "localhost", TLS: true, SkipCertVerify: true})
	if err != nil {
		insecureConn.Close()
		t.Fatalf("explicit skip_cert_verify did not permit TLS probe: %v", err)
	}
	upgraded.Close()
}
