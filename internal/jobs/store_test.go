package jobs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func TestLeaseSurvivesRestartExpiryAndPolicyChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	j := config.JobConfig{Name: "accounts", SessionTTL: "-1s", MaxSessions: 2}
	l, err := store.Acquire(j, "policy-a", "account-a", []SelectedNode{{Node: "node-a"}})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Acquire(j, "policy-b", "account-a", []SelectedNode{{Node: "node-b"}})
	if err != nil || again.Job != l.Job || again.Session != l.Session || again.Node != l.Node || again.Policy != l.Policy || !again.ExpiresAt.Equal(l.ExpiresAt) {
		t.Fatalf("session silently rebound: %+v %v", again, err)
	}
	if err := reopened.Release(j.Name, "account-a"); err != nil {
		t.Fatal(err)
	}
	newLease, err := reopened.Acquire(j, "policy-b", "account-a", []SelectedNode{{Node: "node-b"}})
	if err != nil || newLease.Node != "node-b" {
		t.Fatalf("explicit recovery failed: %+v %v", newLease, err)
	}
}
func TestConcurrentAcquireIsIdempotentAndBounded(t *testing.T) {
	s, _ := Open("")
	j := config.JobConfig{Name: "accounts", SessionTTL: "1h", MaxSessions: 2}
	ranked := []SelectedNode{{Node: "a"}, {Node: "b"}}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.Acquire(j, "p", "same", ranked)
			if err != nil || l.Node != "a" {
				t.Errorf("non-idempotent allocation: %+v %v", l, err)
			}
		}()
	}
	wg.Wait()
	l, err := s.Acquire(j, "p", "second", ranked)
	if err != nil || l.Node != "b" {
		t.Fatalf("did not balance sessions: %+v %v", l, err)
	}
	if _, err := s.Acquire(j, "p", "third", ranked); !errors.Is(err, ErrConflict) {
		t.Fatal("session bound not enforced")
	}
}

func TestPausedAssignmentsKeepExistingLeasesAndResumeExactlyOnce(t *testing.T) {
	s, _ := Open("")
	j := config.JobConfig{Name: "accounts", SessionTTL: "1h", MaxSessions: 2}
	ranked := []SelectedNode{{Node: "a"}}
	original, err := s.Acquire(j, "old", "existing", ranked)
	if err != nil {
		t.Fatal(err)
	}
	resumeOuter, resumeInner := s.PauseNewAssignments(), s.PauseNewAssignments()
	defer resumeOuter()
	defer resumeInner()
	if existing, err := s.Acquire(j, "candidate", "existing", ranked); err != nil || existing != original {
		t.Fatalf("pause changed an existing lease: %+v %v", existing, err)
	}
	resumeOuter()
	resumeOuter()
	if _, err := s.Acquire(j, "candidate", "new", ranked); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nested pause did not block allocation: %v", err)
	}
	if _, exists := s.Lease(j.Name, "new"); exists {
		t.Fatal("blocked allocation left a lease")
	}
	resumeInner()
	if _, err := s.Acquire(j, "committed", "new", ranked); err != nil {
		t.Fatalf("resume did not re-enable allocations: %v", err)
	}
}
func TestFailedSessionWriteRollsBackAndCorruptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open("")
	s.path = dir
	j := config.JobConfig{Name: "a", SessionTTL: "1h", MaxSessions: 1}
	if _, err := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}}); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, ok := s.Lease("a", "s"); ok {
		t.Fatal("failed durable write published lease")
	}
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt sessions must not reset silently")
	}
	if err := os.WriteFile(path, []byte("{} garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("trailing corruption accepted")
	}
}
func TestTargetRankingExcludesFailuresStaleAndForeignNodes(t *testing.T) {
	s, _ := Open("")
	s.Record("site-a", Measurement{Node: "fast", Success: true, DurationMS: 10})
	s.Record("site-a", Measurement{Node: "slow", Success: true, DurationMS: 100})
	s.Record("site-b", Measurement{Node: "fast", Success: false})
	values, _ := s.Measurements("site-a")
	ranked := Rank(values, map[string]bool{"fast": true, "slow": true}, time.Minute, time.Now())
	if len(ranked) != 2 || ranked[0].Node != "fast" {
		t.Fatal("target ranking leaked another target's failure")
	}
	s.Record("site-a", Measurement{Node: "fast", Success: false})
	values, _ = s.Measurements("site-a")
	ranked = Rank(values, map[string]bool{"fast": true, "slow": true}, time.Minute, time.Now())
	if len(ranked) != 1 || ranked[0].Node != "slow" {
		t.Fatal("failed node retained")
	}
	if len(Rank(values, map[string]bool{"slow": true}, time.Minute, time.Now().Add(2*time.Minute))) != 0 {
		t.Fatal("stale node retained")
	}
}

func TestSelectionPolicyIgnoresDisabledBenchmarkSettings(t *testing.T) {
	j := config.JobConfig{Name: "accounts", Mode: "pinned", Profile: "chosen"}
	profile := config.ProfileConfig{Name: "chosen"}
	manual := Policy(j, profile, false)
	j.Selection, j.TargetURL, j.Size = "manual", "https://example.test/", 5
	j.RefreshInterval, j.ProbeBatchSize, j.ProbeConcurrency, j.MaxResponseBytes = "1m", 32, 4, 1024
	if Policy(j, profile, false) != manual {
		t.Fatal("disabled benchmark settings invalidated manual session")
	}
	j.Selection = "auto"
	automatic := Policy(j, profile, false)
	if automatic == manual {
		t.Fatal("changing selection did not protect existing assignments")
	}
	j.TargetURL = "https://another.test/"
	if Policy(j, profile, false) == automatic {
		t.Fatal("automatic target change did not invalidate old policy")
	}
}

func TestUpdatingFullMeasurementStoreDoesNotEvictAnotherNode(t *testing.T) {
	s, _ := Open("")
	for i := range 4096 {
		s.Record("target", Measurement{Node: fmt.Sprintf("node-%d", i), Success: true, DurationMS: 10})
	}
	oldest := s.scores["target"].values["node-0"]
	oldest.CheckedAt = time.Now().Add(-time.Hour)
	s.scores["target"].values["node-0"] = oldest
	s.Record("target", Measurement{Node: "node-4095", Success: true, DurationMS: 20})
	values, _ := s.Measurements("target")
	if len(values) != 4096 || values["node-0"].Node != "node-0" {
		t.Fatalf("updating an existing score evicted another node: count=%d oldest=%+v", len(values), values["node-0"])
	}
	s.Record("target", Measurement{Node: "new", Success: true, DurationMS: 10})
	values, _ = s.Measurements("target")
	if len(values) != 4096 || values["new"].Node != "new" || values["node-0"].Node != "" {
		t.Fatal("adding a new score did not evict the oldest at capacity")
	}
}

func TestMeasurementCachePreservesRunningRefreshAtCapacity(t *testing.T) {
	s, _ := Open("")
	if !s.BeginRefresh("running", time.Minute, false) {
		t.Fatal("initial refresh did not start")
	}
	s.Record("running", Measurement{Node: "a", Success: true, DurationMS: 10})
	// Model a slow benchmark that started before the other policy revisions.
	s.scores["running"].lastAttempt = time.Now().Add(-time.Hour)
	for i := range 128 {
		key := fmt.Sprintf("retired-policy-%d", i)
		if !s.BeginRefresh(key, time.Minute, false) {
			t.Fatal("new policy could not evict a completed refresh")
		}
		s.EndRefresh(key)
	}
	values, refreshing := s.Measurements("running")
	if !refreshing || values["a"].Node != "a" {
		t.Fatalf("cache pressure discarded a running benchmark: refreshing=%t values=%v", refreshing, values)
	}
	if s.BeginRefresh("running", time.Minute, true) {
		t.Fatal("cache eviction allowed a duplicate benchmark")
	}
	if len(s.scores) != 128 {
		t.Fatalf("measurement cache is not bounded: %d", len(s.scores))
	}
	s.EndRefresh("running")
	if s.IsRefreshing("running") {
		t.Fatal("completed benchmark remained active")
	}
}

func TestMissingMeasurementReadsDoNotEvictExistingScores(t *testing.T) {
	s, _ := Open("")
	s.Record("active", Measurement{Node: "a", Success: true, DurationMS: 10})
	version := s.Version("active")
	for i := range 128 {
		key := fmt.Sprintf("missing-policy-%d", i)
		if s.Version(key) != 0 || s.IsRefreshing(key) {
			t.Fatal("unknown policy has runtime state")
		}
		if values, refreshing := s.Measurements(key); len(values) != 0 || refreshing {
			t.Fatal("unknown policy has measurements")
		}
		s.EndRefresh(key)
	}
	if len(s.scores) != 1 || s.Version("active") != version {
		t.Fatal("reading missing policies evicted existing measurements")
	}
}

func TestMeasurementCacheDefersNewRefreshWhenAllEntriesAreBusy(t *testing.T) {
	s, _ := Open("")
	for i := range 128 {
		if !s.BeginRefresh(fmt.Sprintf("running-%d", i), time.Minute, false) {
			t.Fatal("refresh within capacity did not start")
		}
	}
	if s.BeginRefresh("extra", time.Minute, false) {
		t.Fatal("starting another benchmark displaced a running one")
	}
	s.Record("extra", Measurement{Node: "a", Success: true, DurationMS: 10})
	if len(s.scores) != 128 || s.Version("extra") != 0 {
		t.Fatal("recording an extra policy displaced an active benchmark")
	}
	for i := range 128 {
		if !s.IsRefreshing(fmt.Sprintf("running-%d", i)) {
			t.Fatal("active benchmark was forgotten at capacity")
		}
	}
	s.EndRefresh("running-0")
	if !s.BeginRefresh("extra", time.Minute, false) {
		t.Fatal("new refresh did not resume after capacity became available")
	}
	if len(s.scores) != 128 {
		t.Fatal("refresh exceeded the cache bound")
	}
}

func TestCertificateVerificationModeInvalidatesJobPolicy(t *testing.T) {
	j := config.JobConfig{Name: "accounts", Mode: "pinned", Selection: "manual"}
	profile := config.ProfileConfig{Name: "fast"}
	original := Policy(j, profile, false)
	if PolicyWithCertVerifyMode(j, profile, false, "default") != original || PolicyWithCertVerifyMode(j, profile, false, "") != original {
		t.Fatal("default mode changed existing policy hashes")
	}
	if PolicyWithCertVerifyMode(j, profile, false, "override") == original {
		t.Fatal("override reused old certificate-policy measurements and leases")
	}
}
