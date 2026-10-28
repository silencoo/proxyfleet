package jobs

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func recoveryConfig() config.JobConfig {
	return config.JobConfig{Name: "browser", Mode: "pinned", Ingress: "shared", FailurePolicy: "failover", SessionTTL: "48h", MaxSessions: 100, MaxAccountsPerNode: 1}
}

func TestFailoverAtomicDurableAndPreservesLifetime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s, _ := Open(path)
	j := recoveryConfig()
	old, _ := s.Acquire(j, "policy", "session", []SelectedNode{{Node: "a"}})
	other := j
	other.Name = "social"
	_, _ = s.Acquire(other, "policy", "account", []SelectedNode{{Node: "b"}})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := commitTestFailover(s, j, "policy", old, []SelectedNode{{Node: "b", Name: "US B"}, {Node: "c"}}, "node_missing", "US A")
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Errorf("failed recovery: %v", err)
			}
		}()
	}
	wg.Wait()
	l, _ := s.Lease(j.Name, old.Session)
	if l.Version() == old.Version() || !l.ExpiresAt.Equal(old.ExpiresAt) || l.Permanent != old.Permanent || l.Policy != old.Policy || l.LastFailover.FromNode != "a" || l.LastFailover.ToNode != "b" || l.LastFailover.FromName != "US A" {
		t.Fatalf("lost identity/lifetime/audit: %+v", l)
	}
	if err := s.FlushActivity(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := reopened.Lease(j.Name, old.Session)
	wantJSON, _ := json.Marshal(l)
	gotJSON, _ := json.Marshal(restored)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("recovery not durable: %+v", restored)
	}
	if _, err := commitTestFailover(reopened, j, "policy", restored, []SelectedNode{{Node: "c"}}, "dial_failed", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cooldown lost across restart: %v", err)
	}
	counts := s.AccountCounts(j.Name)
	if counts["a"] != 0 || counts["b"] != 1 {
		t.Fatal(counts)
	}
}

func TestFailoverRejectsUnsafeRecoveryAndRollsBack(t *testing.T) {
	for _, tc := range []string{"strict", "direct", "pooled", "expired", "policy", "fenced", "capacity", "empty", "same-node", "write", "reason"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			s, _ := Open(filepath.Join(dir, "state"))
			j := recoveryConfig()
			policy, reason := "policy", "node_unavailable"
			nodes := []SelectedNode{{Node: "b"}}
			if tc == "expired" {
				j.SessionTTL = "-1s"
			}
			old, _ := s.Acquire(j, "policy", "session", []SelectedNode{{Node: "a"}})
			switch tc {
			case "strict":
				j.FailurePolicy = "strict"
			case "direct":
				j.Ingress = "direct"
			case "pooled":
				j.Mode = "pooled"
			case "policy":
				policy = "new"
			case "fenced":
				defer s.PauseNewAssignments()()
			case "capacity":
				_, _ = s.Acquire(j, "policy", "other", nodes)
			case "empty":
				nodes = nil
			case "same-node":
				nodes = []SelectedNode{{Node: "a"}}
			case "write":
				s.path = dir
			case "reason":
				reason = "cloudflare"
			}
			if _, err := commitTestFailover(s, j, policy, old, nodes, reason, ""); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			now, _ := s.Lease(j.Name, old.Session)
			if !reflect.DeepEqual(now, old) {
				t.Fatalf("failed migration altered binding: %+v", now)
			}
		})
	}
}

func TestFailoverNeverOverwritesManualMigrationOrRelease(t *testing.T) {
	s, _ := Open("")
	j := recoveryConfig()
	old, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	_, err := s.Migrate(j, "p", Migration{Session: "s", Node: "c", ExpectedRevision: old.Version()}, []SelectedNode{{Node: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := commitTestFailover(s, j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatal("overwrote manual winner", got, err)
	}
	_ = s.Release(j.Name, "s")
	if _, err := commitTestFailover(s, j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("recreated released binding", err)
	}
	_, _ = s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	got, err = commitTestFailover(s, j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale failure crossed release/reacquire")
	}
}

func TestFailoverConcurrentSessionsRespectCapacity(t *testing.T) {
	s, _ := Open("")
	j := recoveryConfig()
	a, _ := s.Acquire(j, "p", "a", []SelectedNode{{Node: "a"}})
	b, _ := s.Acquire(j, "p", "b", []SelectedNode{{Node: "b"}})
	var wg sync.WaitGroup
	for _, l := range []Lease{a, b} {
		wg.Add(1)
		go func(l Lease) {
			defer wg.Done()
			_, err := commitTestFailover(s, j, "p", l, []SelectedNode{{Node: "target"}}, "node_missing", "")
			if err != nil && !errors.Is(err, ErrCapacity) {
				t.Error(err)
			}
		}(l)
	}
	wg.Wait()
	if s.AccountCounts(j.Name)["target"] != 1 {
		t.Fatal("oversubscribed")
	}
}

func TestFailoverPolicyTogglePreservesLegacyIdentity(t *testing.T) {
	j := recoveryConfig()
	j.FailurePolicy = ""
	profile := config.ProfileConfig{Name: "browser"}
	old := Policy(j, profile, false)
	for _, policy := range []string{"strict", "failover"} {
		j.FailurePolicy = policy
		if Policy(j, profile, false) != old {
			t.Fatal("toggling recovery invalidated existing bindings")
		}
	}
	profile.Regions = []string{"us"}
	if Policy(j, profile, false) == old {
		t.Fatal("candidate policy must remain fenced")
	}
}

func TestPermanentFailoverDoesNotRenewOrRevert(t *testing.T) {
	s, _ := Open("")
	j := recoveryConfig()
	j.SessionTTL = "0"
	old, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	l, err := commitTestFailover(s, j, "p", old, []SelectedNode{{Node: "b"}}, "node_missing", "")
	if err != nil || !l.Permanent || !l.ExpiresAt.IsZero() || l.Expired(time.Now().AddDate(100, 0, 0)) {
		t.Fatal(l, err)
	}
	again, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	if again.Node != "b" {
		t.Fatal("reverted recovery")
	}
}

// Models a successful alternate dial for store-only tests.
func commitTestFailover(s *Store, j config.JobConfig, policy string, old Lease, nodes []SelectedNode, reason, name string) (Lease, error) {
	a, err := s.BeginFailover(j, policy, old, nodes, reason, name)
	if err != nil {
		return Lease{}, err
	}
	defer a.Abort()
	l, done, err := a.Commit()
	if done != nil {
		done()
	}
	return l, err
}
