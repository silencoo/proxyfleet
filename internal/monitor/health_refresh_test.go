package monitor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRuntimeProbeReplacementPreservesUnchangedHealth(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		e := &entry{}
		fn := func(context.Context) (time.Duration, error) { return time.Millisecond, nil }
		e.setProbeForRuntime("transport-1", fn)
		var probeErr error
		if !healthy {
			probeErr = errors.New("failed probe")
		}
		e.markProbeResult(time.Millisecond, probeErr)
		before := e.snapshot()
		generation := e.probeGeneration
		e.setProbeForRuntime("transport-1", fn)
		if after := e.snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("unchanged transport lost health: before=%+v after=%+v", before, after)
		}
		if e.markProbeResultGeneration(probeResultVersion{generation: generation, id: 1}, time.Millisecond, nil) {
			t.Fatal("old callback result survived replacement")
		}
		e.setProbeForRuntime("transport-2", fn)
		if after := e.snapshot(); after.InitialCheckDone || after.Available {
			t.Fatal("changed transport inherited health")
		}
	}
}

func TestValidatedProbeRejectsStaleOrUncommittedEvidence(t *testing.T) {
	for _, rejection := range []string{"identity", "canceled", "newer-probe", "newer-traffic", "newer-completion", "in-flight", "duplicate", "not-admitted"} {
		t.Run(rejection, func(t *testing.T) {
			e := &entry{}
			h := &EntryHandle{ref: e}
			h.SetProbeForRuntime("live", func(context.Context) (time.Duration, error) { return time.Millisecond, nil })
			start := time.Now().Add(-time.Second)
			end := start.Add(time.Millisecond)
			identity := "live"
			var err error
			switch rejection {
			case "identity":
				identity = "candidate"
			case "canceled":
				err = context.Canceled
			case "newer-probe":
				e.markProbeResult(time.Millisecond, errors.New("newer failure"))
			case "newer-traffic":
				h.RecordSuccess()
			case "in-flight":
				e.probeCall = &inFlightProbe{}
			case "newer-completion":
				e.probeCompletedAt = time.Now() // Callback finished; waiter has not committed it yet.
			case "duplicate":
				if !h.ApplyValidatedProbe(identity, start, end, time.Millisecond, nil, nil) {
					t.Fatal("first result rejected")
				}
			case "not-admitted":
				start = time.Time{}
			}
			before := e.snapshot()
			called := false
			if h.ApplyValidatedProbe(identity, start, end, time.Millisecond, err, func() { called = true }) || called {
				t.Fatal("invalid observation changed routing")
			}
			if !reflect.DeepEqual(before, e.snapshot()) {
				t.Fatal("invalid observation changed health")
			}
		})
	}
}

func TestProbeTargetChangeInvalidatesHealthButIdenticalTargetDoesNot(t *testing.T) {
	m, err := NewManager(Config{ProbeTarget: "http://example.test/a"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	h := m.Register(NodeInfo{Tag: "node"})
	h.SetProbeForRuntime("live", func(context.Context) (time.Duration, error) { return time.Millisecond, nil })
	h.MarkInitialCheckDone(true)
	if err := m.SetProbeTarget("http://example.test/a", false); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot()[0].Available {
		t.Fatal("identical target reset health")
	}
	if err := m.SetProbeTarget("http://example.test/b", false); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot()[0]; s.Available || s.InitialCheckDone {
		t.Fatal("changed target reused old health")
	}
}

func TestHealthSummaryAndFiltersPartitionUnknownAndBlockedNodes(t *testing.T) {
	snapshots := []Snapshot{
		{InitialCheckDone: true, Available: true}, {InitialCheckDone: true, Available: false}, {},
		{Blacklisted: true}, {CoolingDown: true}, {InitialCheckDone: true, Available: true, Blacklisted: true},
	}
	summary := summarizeNodes(snapshots)
	if summary.HealthyNodes != 1 || summary.UnavailableNodes != 4 || summary.UnknownNodes != 1 {
		t.Fatalf("incorrect partition: %+v", summary)
	}
	for _, s := range snapshots {
		matches := 0
		for _, status := range []string{"healthy", "unavailable", "unknown"} {
			if nodeMatchesStatus(s, status) {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("overlapping or missing status for %+v", s)
		}
	}
}
