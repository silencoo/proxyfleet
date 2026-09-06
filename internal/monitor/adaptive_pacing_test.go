package monitor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdaptivePacingRefillsInsteadOfSpendingHourlyBudgetAtOnce(t *testing.T) {
	m, err := NewManager(Config{ProbeMode: "adaptive", ProbeBatchSize: 100, ProbeMaxPerHour: 600, ProbeMaxPerDay: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	if got := m.reserveProbeBudget(100, now); got != 100 {
		t.Fatalf("startup batch = %d, want 100", got)
	}
	if got := m.reserveProbeBudget(100, now); got != 0 {
		t.Fatalf("repeated batch bypassed pacing: %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(5*time.Minute)); got != 17 {
		t.Fatalf("daily-paced five-minute refill = %d, want 17", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(10*time.Minute)); got != 17 {
		t.Fatalf("fractional refill was not preserved: %d", got)
	}
	if m.probeBudgetUsed != 134 || m.probeBudgetDailyUsed != 134 {
		t.Fatal("pacing and hard-cap counters diverged")
	}
	if got := m.reserveProbeBudget(500, now.Add(10*time.Hour)); got != 100 {
		t.Fatalf("idle credit exceeded one batch: %d", got)
	}
}

func TestAdaptivePacingDoesNotIssueFreeBurstOnResetOrClockRollback(t *testing.T) {
	m, err := NewManager(Config{ProbeBatchSize: 100, ProbeMaxPerHour: 600})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	now := time.Date(2026, 9, 6, 10, 59, 59, 0, time.UTC)
	if got := m.reserveProbeBudget(100, now); got != 100 {
		t.Fatalf("initial batch = %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(2*time.Second)); got != 0 {
		t.Fatalf("clock-hour reset created free credit: %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(-time.Hour)); got != 0 {
		t.Fatalf("clock rollback created free credit: %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(6*time.Second)); got != 1 {
		t.Fatalf("refill after clock changes = %d, want 1", got)
	}
}

func TestAdaptivePacingReservationsAreAtomic(t *testing.T) {
	m, err := NewManager(Config{ProbeBatchSize: 100, ProbeMaxPerHour: 600, ProbeMaxPerDay: 5000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	now := time.Now()
	var total atomic.Int64
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			total.Add(int64(m.reserveProbeBudget(100, now)))
		}()
	}
	workers.Wait()
	if got := total.Load(); got != 100 {
		t.Fatalf("concurrent reservations consumed %d, want 100", got)
	}
	status := m.ProbeBudgetStatus()
	if status.AvailableNow != 0 || status.Remaining != 500 || status.PacingBurst != 100 {
		t.Fatalf("status does not distinguish pacing from hard caps: %+v", status)
	}
}

func TestAdaptivePacingStillEnforcesHardCaps(t *testing.T) {
	m, err := NewManager(Config{ProbeBatchSize: 100, ProbeMaxPerHour: 10, ProbeMaxPerDay: 15})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	if got := m.reserveProbeBudget(100, now); got != 10 {
		t.Fatalf("initial cap = %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(59*time.Minute)); got != 0 {
		t.Fatalf("refilled token bypassed hourly cap: %d", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(10*time.Hour)); got != 5 {
		t.Fatalf("remaining daily cap = %d, want 5", got)
	}
	if got := m.reserveProbeBudget(100, now.Add(11*time.Hour)); got != 0 {
		t.Fatalf("hourly reset bypassed daily cap: %d", got)
	}
}

func TestAdaptiveSelectionSharesBudgetAcrossNodeClasses(t *testing.T) {
	for _, batch := range []int{1, 4} {
		t.Run(fmt.Sprintf("batch-%d", batch), func(t *testing.T) {
			m, err := NewManager(Config{ProbeMode: "adaptive"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Stop)
			now := time.Now()
			var entries []*entry
			classes := make(map[*entry]int)
			for class := range 3 {
				for index := range 10 {
					e := &entry{
						info:             NodeInfo{Tag: fmt.Sprintf("%d-%d", class, index)},
						probe:            func(context.Context) (time.Duration, error) { return time.Millisecond, nil },
						initialCheckDone: class != 0, available: class == 2,
						lastProbeAt: now.Add(-2 * time.Hour),
					}
					if class == 1 {
						e.consecutiveProbeFails = 2
					}
					entries = append(entries, e)
					classes[e] = class
				}
			}
			var got [3]int
			for range 4 / batch {
				selected := m.selectAdaptiveEntries(entries, batch, now)
				if len(selected) != batch {
					t.Fatalf("selected %d, want %d", len(selected), batch)
				}
				for _, e := range selected {
					got[classes[e]]++
					e.markProbeResult(time.Millisecond, nil)
				}
			}
			if got != [3]int{2, 1, 1} {
				t.Fatalf("unfair new/recovery/healthy shares: %v", got)
			}
		})
	}
}

func TestAdaptivePassiveGraceRequiresCurrentHealthyEvidence(t *testing.T) {
	for _, reason := range []string{"new-failure", "replacement"} {
		t.Run(reason, func(t *testing.T) {
			m, err := NewManager(Config{ProbeMode: "adaptive"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Stop)
			now := time.Now()
			e := &entry{
				info:               NodeInfo{Tag: reason},
				probe:              func(context.Context) (time.Duration, error) { return time.Millisecond, nil },
				lastPassiveSuccess: now.Add(-time.Minute), lastProbeAt: now.Add(-time.Hour),
			}
			if reason == "new-failure" {
				e.initialCheckDone = true
				e.lastFail = now.Add(-30 * time.Second)
				e.consecutiveProbeFails = 1
			}
			if got := m.selectAdaptiveEntries([]*entry{e}, 1, now); len(got) != 1 {
				t.Fatal("stale passive success suppressed a needed check")
			}
		})
	}
}
