package runtimestate

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSaveChangesPreservesUntouchedNodesAndRollsBack(t *testing.T) {
	e, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	if err := e.SaveSnapshot(ctx, []HealthRecord{{NodeID: "a", Failures: 1}, {NodeID: "b", Failures: 2}}, []DomainLatencyRecord{{NodeID: "a", Domain: "old.test", EWMAMs: 10}, {NodeID: "b", Domain: "keep.test", EWMAMs: 20}}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`CREATE TRIGGER protect_b_health BEFORE UPDATE ON node_health WHEN NEW.node_id='b' BEGIN SELECT RAISE(ABORT,'untouched health rewritten'); END`,
		`CREATE TRIGGER protect_b_domains BEFORE DELETE ON node_domain_latency WHEN OLD.node_id='b' BEGIN SELECT RAISE(ABORT,'untouched domains deleted'); END`,
		`CREATE TRIGGER fail_domain BEFORE INSERT ON node_domain_latency WHEN NEW.domain='fail.test' BEGIN SELECT RAISE(ABORT,'injected failure'); END`,
	} {
		if _, err := e.db.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.SaveChanges(ctx, []HealthRecord{{NodeID: "a", Failures: 3}}, []DomainLatencyRecord{{NodeID: "a", Domain: "new.test", EWMAMs: 30}}, []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveChanges(ctx, []HealthRecord{{NodeID: "a", Failures: 99}}, []DomainLatencyRecord{{NodeID: "a", Domain: "fail.test", EWMAMs: 99}}, []string{"a"}, nil); err == nil {
		t.Fatal("expected injected failure")
	}
	health, err := e.LoadHealth(ctx)
	if err != nil || health["a"].Failures != 3 || health["b"].Failures != 2 {
		t.Fatalf("health rollback/preservation failed: %+v %v", health, err)
	}
	domains, err := e.LoadDomainLatencies(ctx)
	if err != nil || len(domains) != 2 {
		t.Fatalf("domains changed: %+v %v", domains, err)
	}
	for _, row := range domains {
		if row.NodeID == "a" && row.Domain != "new.test" || row.NodeID == "b" && row.Domain != "keep.test" {
			t.Fatal("domain rollback/preservation failed")
		}
	}
	if err := e.SaveChanges(ctx, nil, nil, nil, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	health, _ = e.LoadHealth(ctx)
	domains, _ = e.LoadDomainLatencies(ctx)
	if len(health) != 1 || len(domains) != 1 || domains[0].NodeID != "b" {
		t.Fatal("retirement did not remove exactly one node's health and cache")
	}
}
