package boxmgr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestRefreshPreservesUnchangedHealthWithoutProbeBudget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	cfg := newPreflightTargetConfig(t, target.URL)
	quarantine := false
	cfg.SubscriptionRefresh.QuarantineNewNodes = &quarantine
	manager := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: "manual"})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	before := manager.MonitorManager().Snapshot()[0]
	if _, err := manager.MonitorManager().Probe(context.Background(), before.Tag); err != nil {
		t.Fatal(err)
	}
	before = manager.MonitorManager().Snapshot()[0]
	for i := 0; i < 3; i++ {
		candidate, _ := manager.ConfigSnapshot()
		candidate.Nodes[0].Name = fmt.Sprintf("renamed-%d", i)
		if err := normalizeAndReload(t, manager, candidate); err != nil {
			t.Fatal(err)
		}
		after := manager.MonitorManager().Snapshot()[0]
		if !after.InitialCheckDone || !after.Available || !after.LastProbeAt.Equal(before.LastProbeAt) || len(after.Timeline) != len(before.Timeline) {
			t.Fatalf("metadata refresh reset or re-probed unchanged node: %+v", after)
		}
	}
}

func TestRefreshImportsAllCandidateHealthWithExhaustedAdaptiveBudget(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	cfg := newPreflightTargetConfig(t, target.URL)
	manager := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: "adaptive", ProbeBatchSize: 1, ProbeMaxPerHour: 1, ProbeMaxPerDay: 1, ProbeHealthyInterval: time.Hour})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	// Spend the only startup credit. Imported preflight evidence must not need
	// another automatic probe to mark every committed node checked.
	manager.MonitorManager().ProbeConfiguredNow(time.Second)
	candidate, _ := manager.ConfigSnapshot()
	candidate.SubscriptionRefresh.MinAvailableNodes = 1
	candidate.SubscriptionRefresh.HealthCheckTimeout = 100 * time.Millisecond
	candidate.Nodes = append(candidate.Nodes,
		config.NodeConfig{Name: "added-healthy", URI: "socks5://" + startTestSOCKS5Proxy(t) + "#added"},
		config.NodeConfig{Name: "added-failed", URI: fmt.Sprintf("socks5://127.0.0.1:%d#failed", findManagerTestPort(t))},
	)
	if err := normalizeAndReload(t, manager, candidate); err != nil {
		t.Fatal(err)
	}
	available := 0
	for _, s := range manager.MonitorManager().Snapshot() {
		if !s.InitialCheckDone {
			t.Fatalf("candidate result discarded: %+v", s)
		}
		if s.Available {
			available++
		}
		if s.SuccessCount != 0 || s.FailureCount != 0 || !s.LastPassiveSuccess.IsZero() {
			t.Fatal("validation was counted as traffic")
		}
	}
	if available != 2 {
		t.Fatalf("available=%d, want 2", available)
	}
	if budget := manager.MonitorManager().ProbeBudgetStatus(); budget.Used != 1 {
		t.Fatalf("unexpected extra automatic probes: %+v", budget)
	}
	if calls.Load() != 3 {
		t.Fatalf("healthy target received %d checks, want 1 startup + 2 preflight", calls.Load())
	}
}

func TestRejectedRefreshDoesNotPublishCandidateHealth(t *testing.T) {
	var failing atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer target.Close()
	cfg := newPreflightTargetConfig(t, target.URL)
	manager := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: "manual"})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	tag := manager.MonitorManager().Snapshot()[0].Tag
	if _, err := manager.MonitorManager().Probe(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	before := manager.MonitorManager().Snapshot()[0]
	failing.Store(true)
	candidate, _ := manager.ConfigSnapshot()
	candidate.Nodes[0].Name = "rejected rename"
	candidate.SubscriptionRefresh.MinAvailableNodes = 1
	if err := normalizeAndReload(t, manager, candidate); err == nil {
		t.Fatal("unhealthy candidate accepted")
	}
	after := manager.MonitorManager().Snapshot()[0]
	if !after.Available || !after.InitialCheckDone || !after.LastProbeAt.Equal(before.LastProbeAt) || len(after.Timeline) != len(before.Timeline) {
		t.Fatalf("rejected preflight polluted health: %+v", after)
	}
}

func TestRefreshDoesNotDuplicateValidationInAllAndSampleModes(t *testing.T) {
	for _, mode := range []string{"all", "sample"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer target.Close()
			cfg := newPreflightTargetConfig(t, target.URL)
			manager := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: mode, ProbeBatchSize: 1})
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			eventuallyBoxManager(t, time.Second, func() bool { return manager.MonitorManager().Snapshot()[0].InitialCheckDone }, "startup check did not finish")
			before := calls.Load()
			candidate, _ := manager.ConfigSnapshot()
			candidate.Nodes[0].Name = "updated label"
			candidate.SubscriptionRefresh.MinAvailableNodes = 1
			if err := normalizeAndReload(t, manager, candidate); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != before+1 {
				t.Fatalf("candidate validation was repeated: %d extra checks", calls.Load()-before)
			}
			if !manager.MonitorManager().Snapshot()[0].Available {
				t.Fatal("candidate health was not reused")
			}
		})
	}
}
