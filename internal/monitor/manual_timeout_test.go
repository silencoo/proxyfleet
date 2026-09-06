package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRegressionManualProbeUsesConfiguredTimeout(t *testing.T) {
	mgr, err := NewManager(Config{ProbeTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()
	remaining := make(chan time.Duration, 1)
	h := mgr.Register(NodeInfo{Tag: "synthetic-timeout"})
	h.SetProbe(func(ctx context.Context) (time.Duration, error) {
		deadline, _ := ctx.Deadline()
		remaining <- time.Until(deadline)
		return time.Millisecond, nil
	})
	if _, err := mgr.Probe(context.Background(), "synthetic-timeout"); err != nil {
		t.Fatal(err)
	}
	if got := <-remaining; got < 29*time.Second || got > 30*time.Second {
		t.Fatalf("configured per-node timeout is 30s but manual callback gets %v", got.Round(time.Millisecond))
	}
}

func TestManualProbeHTTPHandlerUsesConfiguredTimeout(t *testing.T) {
	mgr, err := NewManager(Config{ProbeTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()
	h := mgr.Register(NodeInfo{Tag: "api-timeout"})
	h.SetProbe(func(ctx context.Context) (time.Duration, error) {
		deadline, _ := ctx.Deadline()
		if time.Until(deadline) < 29*time.Second {
			t.Error("API callback received a hard-coded timeout")
		}
		return time.Millisecond, nil
	})
	server := &Server{mgr: mgr}
	response := httptest.NewRecorder()
	server.handleNodeAction(response, httptest.NewRequest(http.MethodPost, "/api/nodes/api-timeout/probe", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("API probe returned %d", response.Code)
	}
}

func TestProbeWaiterTimeoutDoesNotPublishFalseFailure(t *testing.T) {
	e := &entry{}
	release := make(chan struct{})
	e.setProbe(func(context.Context) (time.Duration, error) { <-release; return time.Millisecond, nil })
	e.initialCheckDone, e.available = true, true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	latency, err, version := e.executeProbeGeneration(ctx, context.Background(), time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	e.markProbeResultGeneration(version, latency, err)
	if s := e.snapshot(); !s.Available || s.ConsecutiveProbeFailures != 0 || len(s.Timeline) != 0 {
		t.Fatal("short waiter changed node health")
	}
	e.probeMu.Lock()
	call := e.probeCall
	e.probeMu.Unlock()
	close(release)
	<-call.done
	if s := e.snapshot(); !s.Available || len(s.Timeline) != 1 || !s.Timeline[0].Success {
		t.Fatal("callback did not independently publish its result")
	}
}

func TestHungProbePublishesConfiguredDeadlineOnlyOnce(t *testing.T) {
	e := &entry{}
	release := make(chan struct{})
	e.setProbe(func(context.Context) (time.Duration, error) { <-release; return time.Millisecond, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err, _ := e.executeProbeGeneration(ctx, context.Background(), 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if s := e.snapshot(); s.Available || s.ConsecutiveProbeFailures != 1 {
		t.Fatal("hung transport did not publish its configured timeout")
	}
	e.probeMu.Lock()
	call := e.probeCall
	e.probeMu.Unlock()
	close(release)
	<-call.done
	if s := e.snapshot(); s.Available || s.ConsecutiveProbeFailures != 1 || len(s.Timeline) != 1 {
		t.Fatal("late success replaced or duplicated the configured timeout")
	}
}
