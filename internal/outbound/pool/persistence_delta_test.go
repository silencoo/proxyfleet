package pool

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeFlushWritesOnlyDirtyNodesAndRetriesLatestState(t *testing.T) {
	ResetSharedStateStore()
	resetHealthPersistenceForTest()
	t.Cleanup(func() { ResetSharedStateStore(); resetHealthPersistenceForTest() })
	dbPath := filepath.Join(t.TempDir(), "state.db")
	if err := ConfigureRuntimeState(dbPath, ""); err != nil {
		t.Fatal(err)
	}
	storeMemberHealth("changed", persistedMemberHealth{Failures: 1})
	storeMemberHealth("untouched", persistedMemberHealth{Failures: 2})
	recordDomainLatency("changed", "first.test", time.Millisecond)
	recordDomainLatency("untouched", "keep.test", 2*time.Millisecond)
	if err := FlushHealthState(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TRIGGER protect_health BEFORE UPDATE ON node_health WHEN NEW.node_id='untouched' BEGIN SELECT RAISE(ABORT,'untouched health rewritten'); END`,
		`CREATE TRIGGER protect_domains BEFORE DELETE ON node_domain_latency WHEN OLD.node_id='untouched' BEGIN SELECT RAISE(ABORT,'untouched cache rewritten'); END`,
		`CREATE TRIGGER fail_flush BEFORE INSERT ON node_domain_latency WHEN NEW.domain='fail.test' BEGIN SELECT RAISE(ABORT,'injected failure'); END`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	storeMemberHealth("changed", persistedMemberHealth{Failures: 3})
	recordDomainLatency("changed", "fail.test", 3*time.Millisecond)
	if err := FlushHealthState(); err == nil {
		t.Fatal("expected injected flush failure")
	}
	storeMemberHealth("changed", persistedMemberHealth{Failures: 4})
	if _, err := db.Exec(`DROP TRIGGER fail_flush`); err != nil {
		t.Fatal(err)
	}
	if err := FlushHealthState(); err != nil {
		t.Fatal(err)
	}
	rows, err := healthPersistence.engine.LoadHealth(context.Background())
	if err != nil || rows["changed"].Failures != 4 || rows["untouched"].Failures != 2 {
		t.Fatalf("newest state lost: %+v %v", rows, err)
	}
	// A retired-only deletion must not fall back to rewriting every live row.
	healthPersistence.mu.Lock()
	old := healthPersistence.records["changed"]
	old.UpdatedAt = time.Now().Add(-retiredHealthRetention - time.Hour)
	healthPersistence.records["changed"] = old
	healthPersistence.mu.Unlock()
	PruneRetiredRuntimeState(map[string]struct{}{"untouched": {}})
	if err := FlushHealthState(); err != nil {
		t.Fatal(err)
	}
	rows, _ = healthPersistence.engine.LoadHealth(context.Background())
	domains, _ := healthPersistence.engine.LoadDomainLatencies(context.Background())
	if len(rows) != 1 || len(domains) != 1 || domains[0].NodeID != "untouched" {
		t.Fatal("retired records were not removed exactly")
	}
}

func TestRetiredHealthRetentionBoundsHistoryButKeepsCurrentNodesAndBans(t *testing.T) {
	resetHealthPersistenceForTest()
	t.Cleanup(resetHealthPersistenceForTest)
	now := time.Now()
	healthPersistence.mu.Lock()
	for i := 0; i < maxRetiredHealthNodes+20; i++ {
		healthPersistence.records[fmt.Sprintf("retired-%05d", i)] = persistedMemberHealth{UpdatedAt: now.Add(time.Duration(i) * -time.Second)}
	}
	healthPersistence.records["current"] = persistedMemberHealth{UpdatedAt: now.Add(-30 * 24 * time.Hour)}
	healthPersistence.records["manual-ban"] = persistedMemberHealth{UpdatedAt: now.Add(-30 * 24 * time.Hour), ManualBlacklist: true, BlacklistedUntil: now.Add(time.Hour)}
	healthPersistence.records["expired"] = persistedMemberHealth{UpdatedAt: now.Add(-30 * 24 * time.Hour)}
	healthPersistence.mu.Unlock()
	pruneRetiredRuntimeState(map[string]struct{}{"current": {}}, now)
	if len(healthPersistence.records) != maxRetiredHealthNodes+2 {
		t.Fatalf("retained history not bounded: %d", len(healthPersistence.records))
	}
	for _, tag := range []string{"current", "manual-ban"} {
		if _, ok := healthPersistence.records[tag]; !ok {
			t.Fatalf("pruned protected %s", tag)
		}
	}
	if _, ok := healthPersistence.records["expired"]; ok {
		t.Fatal("expired history retained")
	}
}
