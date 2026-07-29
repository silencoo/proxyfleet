package runtimestate

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEnginePersistsHealthAndDomainLatency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-state.db")
	engine, err := Open(path)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	err = engine.SaveSnapshot(context.Background(), []HealthRecord{{
		NodeID: "node-a", Failures: 2, BlacklistedUntil: now.Add(time.Hour),
		ManualBlacklist: true, MonitorJSON: []byte(`{"available":false}`), UpdatedAt: now,
	}}, []DomainLatencyRecord{{
		NodeID: "node-a", Domain: "example.com", EWMAMs: 42.5, UpdatedAt: now, LastAccess: now,
	}})
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("close engine: %v", err)
	}

	engine, err = Open(path)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	defer engine.Close()
	health, err := engine.LoadHealth(context.Background())
	if err != nil {
		t.Fatalf("load health: %v", err)
	}
	if got := health["node-a"]; got.Failures != 2 || !got.ManualBlacklist || string(got.MonitorJSON) != `{"available":false}` {
		t.Fatalf("unexpected health record: %+v", got)
	}
	domains, err := engine.LoadDomainLatencies(context.Background())
	if err != nil {
		t.Fatalf("load domains: %v", err)
	}
	if len(domains) != 1 || domains[0].Domain != "example.com" || domains[0].EWMAMs != 42.5 {
		t.Fatalf("unexpected domain records: %+v", domains)
	}

	if err := engine.SaveSnapshot(context.Background(), nil, []DomainLatencyRecord{{
		NodeID: "node-a", Domain: "new.example", EWMAMs: 12, UpdatedAt: now, LastAccess: now,
	}}); err != nil {
		t.Fatalf("replace domain snapshot: %v", err)
	}
	domains, err = engine.LoadDomainLatencies(context.Background())
	if err != nil {
		t.Fatalf("reload replaced domains: %v", err)
	}
	if len(domains) != 1 || domains[0].Domain != "new.example" {
		t.Fatalf("stale domain records survived replacement: %+v", domains)
	}
}
