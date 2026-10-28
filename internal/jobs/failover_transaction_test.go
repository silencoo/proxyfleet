package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func TestFailoverReservationIsNotDurableAndReservesCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	j := recoveryConfig()
	j.SessionIdleTimeout = "1m"
	old, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	disk, _ := os.ReadFile(path)
	a, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Abort()
	// Reads, reservation, and even an idle sweep cannot move or reclaim the pin.
	current, _ := s.Lease(j.Name, "s")
	if !reflect.DeepEqual(current, old) {
		t.Fatal("reservation mutated pin")
	}
	if n, err := s.SweepIdle([]config.JobConfig{j}, time.Now().Add(time.Hour)); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if now, _ := os.ReadFile(path); string(now) != string(disk) {
		t.Fatal("reservation persisted")
	}
	reopened, _ := Open(path)
	restored, _ := reopened.Lease(j.Name, "s")
	if restored.Node != "a" || restored.LastFailover != nil {
		t.Fatal("crash changed pin")
	}
	if _, err := s.Acquire(j, "p", "other", []SelectedNode{{Node: "b"}}); !errors.Is(err, ErrCapacity) {
		t.Fatal("acquire stole reservation", err)
	}
	other, _ := s.Acquire(j, "p", "manual", []SelectedNode{{Node: "c"}})
	if _, err := s.Migrate(j, "p", Migration{Session: other.Session, Node: "b", ExpectedRevision: other.Version()}, []SelectedNode{{Node: "b"}}); !errors.Is(err, ErrCapacity) {
		t.Fatal("migration stole reservation", err)
	}
	cross := j
	cross.Name = "different-app"
	if _, err := s.Acquire(cross, "p", "other", []SelectedNode{{Node: "b"}}); err != nil {
		t.Fatal("cross-app isolation", err)
	}
	a.Abort()
	a.Abort()
	current, _ = s.Lease(j.Name, "s")
	if current.Node != "a" || current.LastFailover != nil {
		t.Fatal("abort mutated pin")
	}
	if _, err := s.Acquire(j, "p", "other", []SelectedNode{{Node: "b"}}); err != nil {
		t.Fatal("capacity leaked", err)
	}
}

func TestFailoverCommitFencesAndWriteFailure(t *testing.T) {
	for _, mode := range []string{"manual", "release", "reacquire", "pause", "write", "expired", "abort"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s, _ := Open(filepath.Join(dir, "state"))
			j := recoveryConfig()
			old, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
			a, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
			if err != nil {
				t.Fatal(err)
			}
			defer a.Abort()
			switch mode {
			case "manual":
				_, err = s.Migrate(j, "p", Migration{Session: "s", Node: "c", ExpectedRevision: old.Version()}, []SelectedNode{{Node: "c"}})
			case "release", "reacquire":
				err = s.Release(j.Name, "s")
				if mode == "reacquire" {
					_, err = s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
				}
			case "pause":
				defer s.PauseNewAssignments()()
			case "write":
				s.path = dir
			case "expired":
				l := s.leases[leaseKey(j.Name, "s")]
				l.ExpiresAt = time.Now().Add(-time.Second)
				s.leases[leaseKey(j.Name, "s")] = l
			case "abort":
				a.Abort()
			}
			if err != nil {
				t.Fatal(err)
			}
			before, exists := s.Lease(j.Name, "s")
			if _, done, err := a.Commit(); err == nil {
				if done != nil {
					done()
				}
				t.Fatal("late commit accepted")
			}
			after, ok := s.Lease(j.Name, "s")
			if ok != exists || !reflect.DeepEqual(before, after) {
				t.Fatal("commit changed winner")
			}
		})
	}
}

func TestConcurrentFailoverDialsShareOneReservation(t *testing.T) {
	s, _ := Open("")
	j := recoveryConfig()
	old, _ := s.Acquire(j, "p", "s", []SelectedNode{{Node: "a"}})
	a, _ := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	b, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Abort() // One failed dial must not release the other dial's reservation.
	if _, err := s.Acquire(j, "p", "other", []SelectedNode{{Node: "b"}}); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	l, done, err := b.Commit()
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	defer b.Abort()
	if l.Node != "b" || l.FailoverCount != 1 || !l.ExpiresAt.Equal(old.ExpiresAt) {
		t.Fatal(l)
	}
	c, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Abort()
	again, done2, err := c.Commit()
	if err != nil {
		t.Fatal(err)
	}
	defer done2()
	if again.Version() != l.Version() || again.FailoverCount != 1 {
		t.Fatal("duplicate migration")
	}
}

func TestReloadCannotJoinReservationAboveReducedCapacity(t *testing.T) {
	s, _ := Open("")
	j := recoveryConfig()
	j.MaxAccountsPerNode = 2
	old, _ := s.Acquire(j, "p", "moving", []SelectedNode{{Node: "a"}})
	_, _ = s.Acquire(j, "p", "existing", []SelectedNode{{Node: "b"}})
	pending, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", "")
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Abort()
	j.MaxAccountsPerNode = 1
	if joined, err := s.BeginFailover(j, "p", old, []SelectedNode{{Node: "b"}}, "dial_failed", ""); !errors.Is(err, ErrCapacity) {
		if joined != nil {
			joined.Abort()
		}
		t.Fatal("new runtime inherited over-cap reservation", err)
	}
	current, _ := s.Lease(j.Name, old.Session)
	if current.Node != "a" || s.AccountCounts(j.Name)["b"] != 1 {
		t.Fatal("new assignment bypassed reduced capacity")
	}
}
