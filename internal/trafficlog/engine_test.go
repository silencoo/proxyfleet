package trafficlog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineRecordsQueriesAndRedacts(t *testing.T) {
	engine, err := Open(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "traffic.db"), Retention: time.Hour, MaxEntries: 10, RedactDestination: true})
	if err != nil {
		t.Fatal(err)
	}
	engine.record(Event{NodeID: "node-a", Profile: "HK", Destination: "example.com:443", Network: "tcp", Success: true, UploadBytes: 12})
	time.Sleep(2 * flushInterval)
	events, err := engine.Query(context.Background(), Query{Limit: 5, NodeID: "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Profile != "hk" || !strings.HasPrefix(events[0].Destination, "[sha256:") || events[0].UploadBytes != 12 {
		t.Fatalf("unexpected events: %#v", events)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineQueueDoesNotBlock(t *testing.T) {
	engine := &Engine{config: Config{}, events: make(chan Event, 1), done: make(chan struct{})}
	if !engine.record(Event{}) || engine.record(Event{}) || engine.dropped.Load() != 1 {
		t.Fatal("bounded queue did not drop overflow")
	}
}
