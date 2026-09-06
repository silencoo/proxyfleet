package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCooldownExpiryDoesNotConfirmHealth(t *testing.T) {
	e := &entry{}
	e.markProbeResult(time.Millisecond, nil)
	e.cooldown(time.Now().Add(time.Minute))
	e.markProbeResult(0, context.DeadlineExceeded)
	e.clearCooldown()

	snapshot := e.snapshot()
	if snapshot.CoolingDown || snapshot.Available || !snapshot.InitialCheckDone {
		t.Fatalf("cooldown expiry fabricated recovery: %+v", snapshot)
	}
	if summary := summarizeNodes([]Snapshot{snapshot}); summary.HealthyNodes != 0 || summary.UnavailableNodes != 1 {
		t.Fatalf("failed node counted as healthy: %+v", summary)
	}
	if snapshot.ConsecutiveProbeFailures != 1 || snapshot.LastError == "" {
		t.Fatalf("cooldown expiry erased failure evidence: %+v", snapshot)
	}

	e.markProbeResult(2*time.Millisecond, nil)
	if snapshot := e.snapshot(); !snapshot.Available || snapshot.ConsecutiveProbeFailures != 0 {
		t.Fatalf("successful recovery probe did not restore health: %+v", snapshot)
	}
}

func TestProbeOutcomeAppliedOnceAcrossWaiters(t *testing.T) {
	for _, failed := range []bool{false, true} {
		e := &entry{ewmaLatencyMs: 100}
		started := make(chan struct{})
		release := make(chan struct{})
		e.setProbe(func(context.Context) (time.Duration, error) {
			close(started)
			<-release
			if failed {
				return 0, errors.New("failed probe")
			}
			return 20 * time.Millisecond, nil
		})
		done := make(chan struct{})
		go func() {
			defer close(done)
			latency, err, version := e.executeProbeGeneration(context.Background(), context.Background(), time.Second)
			e.markProbeResultGeneration(version, latency, err)
		}()
		<-started
		e.probeMu.Lock()
		joined := e.probeCall
		e.probeMu.Unlock()
		close(release)
		latency, err, version := e.waitForProbeCall(context.Background(), joined)
		e.markProbeResultGeneration(version, latency, err)
		<-done
		snapshot := e.snapshot()
		if len(snapshot.Timeline) != 1 {
			t.Fatalf("joined callers duplicated timeline events: %+v", snapshot)
		}
		if failed && snapshot.ConsecutiveProbeFailures != 1 {
			t.Fatalf("joined callers multiplied failures: %+v", snapshot)
		}
		if !failed && snapshot.EWMALatencyMs != 80 {
			t.Fatalf("joined callers multiplied EWMA updates: %+v", snapshot)
		}
	}
}

func TestNewerProbeOutcomeSupersedesLateWaiter(t *testing.T) {
	e := &entry{probeGeneration: 1, ewmaLatencyMs: 100}
	e.markProbeResultGeneration(probeResultVersion{generation: 1, id: 2}, 20*time.Millisecond, nil)
	e.markProbeResultGeneration(probeResultVersion{generation: 1, id: 1}, 0, context.DeadlineExceeded)
	if snapshot := e.snapshot(); !snapshot.Available || snapshot.EWMALatencyMs != 80 || len(snapshot.Timeline) != 1 {
		t.Fatalf("late waiter replaced a newer result: %+v", snapshot)
	}
}

func TestCanceledProbePreservesHealthEvidence(t *testing.T) {
	e := &entry{}
	e.markProbeResult(time.Millisecond, nil)
	before := e.snapshot()
	e.markProbeResult(0, context.Canceled)
	after := e.snapshot()
	if !after.Available || after.ConsecutiveProbeFailures != before.ConsecutiveProbeFailures || after.LastProbeAt != before.LastProbeAt || after.LastError != before.LastError {
		t.Fatalf("cancellation altered health: before=%+v after=%+v", before, after)
	}
}
