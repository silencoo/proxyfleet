package runtimestate

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveSnapshotRollsBackHealthAndDomainReplacement(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "runtime-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.SaveSnapshot(ctx,
		[]HealthRecord{{NodeID: "node-a", Failures: 1}},
		[]DomainLatencyRecord{{NodeID: "node-a", Domain: "old.example", EWMAMs: 42}},
	); err != nil {
		t.Fatal(err)
	}
	wantHealth, err := engine.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantDomains, err := engine.LoadDomainLatencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Fail after health upserts, domain deletion, and the first new domain insert.
	if _, err := engine.db.ExecContext(ctx, `CREATE TRIGGER reject_test_domain
		BEFORE INSERT ON node_domain_latency WHEN NEW.domain = 'fail.example'
		BEGIN SELECT RAISE(ABORT, 'injected snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveSnapshot(ctx,
		[]HealthRecord{{NodeID: "node-a", Failures: 9}, {NodeID: "node-b", Failures: 2}},
		[]DomainLatencyRecord{
			{NodeID: "node-a", Domain: "new.example", EWMAMs: 15},
			{NodeID: "node-b", Domain: "fail.example", EWMAMs: 30},
		},
	); err == nil {
		t.Fatal("expected injected snapshot failure")
	}
	gotHealth, err := engine.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotDomains, err := engine.LoadDomainLatencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotHealth, wantHealth) || !reflect.DeepEqual(gotDomains, wantDomains) {
		t.Fatalf("failed transaction changed persisted state: health=%+v domains=%+v", gotHealth, gotDomains)
	}
	// The same connection must remain usable after statement cleanup and rollback.
	if err := engine.SaveSnapshot(ctx, []HealthRecord{{NodeID: "node-a", Failures: 3}}, nil); err != nil {
		t.Fatalf("save after rollback: %v", err)
	}
}

func TestSaveSnapshotEmptyBatchesDefaultsAndUpserts(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "runtime-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if err := engine.SaveSnapshot(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := engine.SaveSnapshot(ctx, nil, []DomainLatencyRecord{
		{NodeID: " ", Domain: "ignored.example", EWMAMs: 10},
		{NodeID: "node-a", Domain: " ", EWMAMs: 10},
		{NodeID: "node-a", Domain: "ignored.example", EWMAMs: 0},
		{NodeID: "node-a", Domain: "first.example", EWMAMs: 12},
		{NodeID: "node-b", Domain: "second.example", EWMAMs: 23},
		{NodeID: "node-a", Domain: "first.example", EWMAMs: 34},
	}); err != nil {
		t.Fatal(err)
	}
	domains, err := engine.LoadDomainLatencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 {
		t.Fatalf("got %d domains, want 2", len(domains))
	}
	for _, row := range domains {
		if row.UpdatedAt.IsZero() || !row.LastAccess.Equal(row.UpdatedAt) {
			t.Fatalf("domain timestamp defaults missing: %+v", row)
		}
		if row.NodeID == "node-a" && row.EWMAMs != 34 || row.NodeID == "node-b" && row.EWMAMs != 23 {
			t.Fatalf("incorrect domain upsert: %+v", row)
		}
	}
	if err := engine.SaveSnapshot(ctx, []HealthRecord{
		{NodeID: " "},
		{NodeID: "node-a", Failures: 1},
		{NodeID: "node-b", Failures: 2},
		{NodeID: "node-a", Failures: 3},
	}, nil); err != nil {
		t.Fatal(err)
	}
	health, err := engine.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(health) != 2 || health["node-a"].Failures != 3 || health["node-b"].Failures != 2 {
		t.Fatalf("incorrect health upserts: %+v", health)
	}
	for _, row := range health {
		if row.UpdatedAt.IsZero() || string(row.MonitorJSON) != "{}" {
			t.Fatalf("health defaults missing: %+v", row)
		}
	}
	domains, err = engine.LoadDomainLatencies(ctx)
	if err != nil || len(domains) != 0 {
		t.Fatalf("empty domain snapshot did not remove old rows: domains=%+v err=%v", domains, err)
	}
}

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
