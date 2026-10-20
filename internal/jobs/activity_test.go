package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func idleFixture(t *testing.T) (*Store, config.JobConfig, Lease) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	j := config.JobConfig{Name: "app", Mode: "pinned", Ingress: "shared", SessionTTL: "0", SessionIdleTimeout: "1m", MaxSessions: 10}
	l, err := s.Acquire(j, "policy", "old", []SelectedNode{{Node: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, j, l
}

func TestIdleActiveProtectionAndLastClose(t *testing.T) {
	s, j, l := idleFixture(t)
	a, err := s.Hold(j.Name, l.Session, l.Version())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Hold(j.Name, l.Session, l.Version())
	if err != nil {
		t.Fatal(err)
	}
	for _, close := range []func(){func() {}, a} {
		close()
		if n, err := s.SweepIdle([]config.JobConfig{j}, time.Now().Add(24*time.Hour)); n != 0 || err != nil {
			t.Fatalf("active reclaimed: %d %v", n, err)
		}
	}
	b()
	b()
	a()
	if n, err := s.SweepIdle([]config.JobConfig{j}, time.Now().Add(59*time.Second)); n != 0 || err != nil {
		t.Fatalf("closed too soon: %d %v", n, err)
	}
	info := SessionInfo{Job: l.Job, Session: l.Session, Revision: l.Version()}
	s.DescribeActivity(&info, j)
	if info.ActiveConnections != 0 || info.ReclaimAt.IsZero() {
		t.Fatalf("bad activity: %+v", info)
	}
	if n, err := s.SweepIdle([]config.JobConfig{j}, time.Now().Add(61*time.Second)); n != 1 || err != nil {
		t.Fatalf("not reclaimed: %d %v", n, err)
	}
	if _, err := s.Hold(j.Name, l.Session, l.Version()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale dial accepted")
	}
}

func TestIdleDisabledDirectRemovedAndPaused(t *testing.T) {
	for _, kind := range []string{"disabled", "pooled", "direct", "removed", "handoff"} {
		t.Run(kind, func(t *testing.T) {
			s, j, _ := idleFixture(t)
			switch kind {
			case "disabled":
				j.SessionIdleTimeout = "0"
			case "pooled":
				j.Mode = "pooled"
			case "direct":
				j.Ingress = "direct"
			case "handoff":
				defer s.PauseNewAssignments()()
			}
			cfg := []config.JobConfig{j}
			if kind == "removed" {
				cfg = nil
			}
			if n, err := s.SweepIdle(cfg, time.Now().Add(48*time.Hour)); n != 0 || err != nil {
				t.Fatalf("reclaimed: %d %v", n, err)
			}
		})
	}
}

func TestIdlePersistenceRestartGraceAndReadOnlyViews(t *testing.T) {
	s, j, l := idleFixture(t)
	l.LastUsedAt = time.Now().Add(-time.Hour).UTC()
	s.leases[leaseKey(l.Job, l.Session)] = l
	s.activityDirty = true
	if err := s.FlushActivity(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := restarted.SweepIdle([]config.JobConfig{j}, time.Now()); n != 0 || err != nil {
		t.Fatalf("restart lost grace: %d %v", n, err)
	}
	for range 10 {
		info := SessionInfo{Job: l.Job, Session: l.Session, Revision: l.Version()}
		restarted.DescribeActivity(&info, j)
		if info.LastUsedAt != l.LastUsedAt {
			t.Fatal("view touched activity")
		}
		if _, err := restarted.Acquire(j, "policy", l.Session, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := restarted.SweepIdle([]config.JobConfig{j}, time.Now().Add(61*time.Second)); n != 1 || err != nil {
		t.Fatalf("reads renewed binding: %d %v", n, err)
	}
	restarted, err = Open(s.path)
	if err != nil || len(restarted.Leases()) != 0 {
		t.Fatal("reclamation not durable", err)
	}
}

func TestIdleWriteFailureRollbackAndMigrationFence(t *testing.T) {
	s, j, l := idleFixture(t)
	done, err := s.Hold(j.Name, l.Session, l.Version())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Migrate(j, "policy", Migration{Session: l.Session, Node: "b", ExpectedRevision: l.Version()}, []SelectedNode{{Node: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	done()
	current, _ := s.Lease(l.Job, l.Session)
	if current != updated {
		t.Fatal("old connection modified replacement binding")
	}
	bad := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(bad, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(bad, "state.json")
	if n, err := s.SweepIdle([]config.JobConfig{j}, time.Now().Add(time.Hour)); n != 0 || err == nil {
		t.Fatal("failed save not reported")
	}
	if current, ok := s.Lease(l.Job, l.Session); !ok || current != updated {
		t.Fatal("failed save removed binding")
	}
}

func TestIdleConcurrentUseAndPolicyCompatibility(t *testing.T) {
	s, j, l := idleFixture(t)
	base := j
	base.SessionIdleTimeout = ""
	if Policy(base, config.ProfileConfig{}, false) != Policy(j, config.ProfileConfig{}, false) {
		t.Fatal("retention invalidated policy")
	}
	legacy := l
	legacy.Revision = ""
	legacy.LastUsedAt = time.Time{}
	version := legacy.Version()
	legacy.LastUsedAt = time.Now()
	if version != legacy.Version() {
		t.Fatal("activity invalidated legacy revision")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 30 {
				done, err := s.Hold(l.Job, l.Session, l.Version())
				if err != nil {
					t.Error(err)
					return
				}
				done()
				_, err = s.SweepIdle([]config.JobConfig{j}, time.Now())
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	info := SessionInfo{Job: l.Job, Session: l.Session, Revision: l.Version()}
	s.DescribeActivity(&info, j)
	if info.ActiveConnections != 0 {
		t.Fatal("leaked active connections")
	}
}
