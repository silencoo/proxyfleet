package boxmgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/monitor"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

type preflightIdentityOutbound struct {
	adapter.Outbound
}

func TestNodeLevelPreflightUsesCandidateProbeTarget(t *testing.T) {
	goodTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer goodTarget.Close()
	badTarget := fmt.Sprintf("http://127.0.0.1:%d", findManagerTestPort(t))

	t.Run("old bad new good succeeds", func(t *testing.T) {
		cfg := newPreflightTargetConfig(t, badTarget)
		manager := New(cfg, monitor.Config{Enabled: false, ProbeTarget: badTarget})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := manager.Start(ctx); err != nil {
			t.Fatalf("start manager: %v", err)
		}
		defer manager.Close()

		candidate := cfg.Clone()
		candidate.Management.ProbeTarget = goodTarget.URL
		candidate.SubscriptionRefresh.MinAvailableNodes = 1
		if err := normalizeAndReload(t, manager, candidate); err != nil {
			t.Fatalf("candidate with healthy new target was rejected: %v", err)
		}
		committed, _ := manager.ConfigSnapshot()
		if got := committed.Management.ProbeTarget; got != goodTarget.URL {
			t.Fatalf("committed probe target=%q, want %q", got, goodTarget.URL)
		}
	})

	t.Run("old good new bad is rejected", func(t *testing.T) {
		cfg := newPreflightTargetConfig(t, goodTarget.URL)
		manager := New(cfg, monitor.Config{Enabled: false, ProbeTarget: goodTarget.URL})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := manager.Start(ctx); err != nil {
			t.Fatalf("start manager: %v", err)
		}
		defer manager.Close()

		candidate := cfg.Clone()
		candidate.Management.ProbeTarget = badTarget
		candidate.SubscriptionRefresh.MinAvailableNodes = 1
		err := normalizeAndReload(t, manager, candidate)
		if err == nil || !strings.Contains(err.Error(), "before cutover") {
			t.Fatalf("candidate with unhealthy new target was not rejected: %v", err)
		}
		committed, _ := manager.ConfigSnapshot()
		if got := committed.Management.ProbeTarget; got != goodTarget.URL {
			t.Fatalf("rejected candidate changed committed target to %q", got)
		}
	})
}

func TestPreflightFlightKeySeparatesTargetAndInstance(t *testing.T) {
	manager := New(&config.Config{}, monitor.Config{})
	instanceA := new(box.Box)
	instanceB := new(box.Box)
	outbound := &preflightIdentityOutbound{}
	targetA, configured, err := monitor.ResolveProbeTarget("http://127.0.0.1:18080", false)
	if err != nil || !configured {
		t.Fatalf("resolve first target: configured=%t err=%v", configured, err)
	}
	targetB, configured, err := monitor.ResolveProbeTarget("http://127.0.0.1:18081", false)
	if err != nil || !configured {
		t.Fatalf("resolve second target: configured=%t err=%v", configured, err)
	}

	keyA := preflightProbeFlightKey(instanceA, "node", targetA, outbound)
	keyTargetB := preflightProbeFlightKey(instanceA, "node", targetB, outbound)
	keyInstanceB := preflightProbeFlightKey(instanceB, "node", targetA, outbound)
	targetPath := targetA
	targetPath.RequestURI = "/different?check=1"
	if keyA == preflightProbeFlightKey(instanceA, "node", targetPath, outbound) {
		t.Fatal("different request paths produced the same preflight flight key")
	}
	if keyA == keyTargetB {
		t.Fatal("different probe targets produced the same preflight flight key")
	}
	if keyA == keyInstanceB {
		t.Fatal("different sing-box instances produced the same preflight flight key")
	}

	blocked := make(chan struct{})
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = manager.runPreflightProbe(ctx, keyA, func() error {
		calls.Add(1)
		<-blocked
		return nil
	})
	cancel()
	if err == nil {
		t.Fatal("hung base flight unexpectedly completed")
	}

	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	err = manager.runPreflightProbe(ctx, keyTargetB, func() error {
		calls.Add(1)
		return nil
	})
	cancel()
	if err != nil {
		t.Fatalf("different-target probe incorrectly joined hung flight: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	err = manager.runPreflightProbe(ctx, keyInstanceB, func() error {
		calls.Add(1)
		return nil
	})
	cancel()
	if err != nil {
		t.Fatalf("different-instance probe incorrectly joined hung flight: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("launched probes=%d, want 3 independent target/instance flights", got)
	}

	close(blocked)
	eventuallyBoxManager(t, time.Second, func() bool {
		manager.preflightMu.Lock()
		remaining := len(manager.preflightCalls)
		manager.preflightMu.Unlock()
		return remaining == 0
	}, "preflight flight did not clean up")
}

type responsePreflightOutbound struct {
	adapter.Outbound
	response string
	paths    chan string
}

func (o *responsePreflightOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		request, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return
		}
		o.paths <- request.RequestURI
		_, _ = io.WriteString(server, o.response)
	}()
	return client, nil
}

func TestPreflightValidatesHTTPStatusAndConfiguredPath(t *testing.T) {
	for _, code := range []int{204, 302, 403, 503} {
		outbound := &responsePreflightOutbound{
			response: fmt.Sprintf("HTTP/1.1 %d %s\r\n\r\n", code, http.StatusText(code)),
			paths:    make(chan string, 1),
		}
		target, _, err := monitor.ResolveProbeTarget("http://example.test/health?ping=1", false)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = probeOutboundConnection(ctx, outbound, target)
		cancel()
		if (err == nil) != (code < 400) {
			t.Fatalf("status %d: %v", code, err)
		}
		if path := <-outbound.paths; path != "/health?ping=1" {
			t.Fatalf("preflight ignored configured path: %q", path)
		}
	}
}

func newPreflightTargetConfig(t *testing.T, probeTarget string) *config.Config {
	t.Helper()
	listenPort := findManagerTestPort(t)
	cfg := &config.Config{
		Mode: "multi-port",
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			BasePort: listenPort,
		},
		Nodes: []config.NodeConfig{{
			Name: "healthy",
			URI:  "socks5://" + startTestSOCKS5Proxy(t) + "#healthy",
			Port: listenPort,
		}},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			MinAvailableNodes:  0,
			HealthCheckTimeout: 500 * time.Millisecond,
		},
		Management: config.ManagementConfig{ProbeTarget: probeTarget},
	}
	cfg.SetFilePath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	cfg.SubscriptionRefresh.MinAvailableNodes = 0
	return cfg
}
