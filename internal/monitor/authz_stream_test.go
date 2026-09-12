package monitor

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeAllStreamsThroughAuthorizedRouter(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	release := make(chan struct{})
	defer close(release)
	manager.Register(NodeInfo{Tag: "synthetic"}).SetProbe(func(ctx context.Context) (time.Duration, error) {
		select {
		case <-release:
			return time.Millisecond, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:9091", Password: "synthetic-admin"}, manager, nil)
	defer server.Shutdown(context.Background())
	server.sessions["synthetic-token"] = &Session{Token: "synthetic-token", Role: RoleOperator, ExpiresAt: time.Now().Add(time.Hour)}
	finished := make(chan struct{}, 1)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.srv.Handler.ServeHTTP(w, r)
		finished <- struct{}{}
	}))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/nodes/probe-all", nil)
	request.Header.Set("Authorization", "Bearer synthetic-token")
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatalf("first SSE event was not flushed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, data)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, `"type":"start"`) {
		t.Fatalf("missing start event before probe completes: %q %v", line, err)
	}
	// Disconnecting an SSE client must still let the middleware record its status.
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnected SSE handler did not return")
	}
	events := manager.AuditLog().Events(1)
	if len(events) != 1 || events[0].Status != http.StatusOK || events[0].Role != RoleOperator {
		t.Fatalf("incorrect stream audit: %+v", events)
	}
}

type nonFlushingWriter struct{ http.ResponseWriter }

func TestAuthorizedWriterPreservesFlushCapability(t *testing.T) {
	for _, flushing := range []bool{true, false} {
		underlying := httptest.NewRecorder()
		var writer http.ResponseWriter = underlying
		if !flushing {
			writer = nonFlushingWriter{underlying}
		}
		server := &Server{}
		server.serveAuthorized(writer, httptest.NewRequest(http.MethodPost, "/", nil), RoleAdmin, func(w http.ResponseWriter, _ *http.Request) {
			flusher, ok := w.(http.Flusher)
			if ok != flushing {
				t.Fatalf("Flusher=%v want=%v", ok, flushing)
			}
			if ok {
				flusher.Flush()
			}
		})
		if underlying.Flushed != flushing {
			t.Fatal("Flush did not reach underlying writer")
		}
	}
}
