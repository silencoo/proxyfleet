package session

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"
)

type waitingRouter struct {
	adapter.Router
	started   chan struct{}
	cancelled chan struct{}
	cleanup   chan struct{}
}

func (r *waitingRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, _ adapter.InboundContext, onClose N.CloseHandlerFunc) {
	defer conn.Close()
	if onClose != nil {
		defer onClose(nil)
	}
	close(r.started)
	select {
	case <-ctx.Done():
		close(r.cancelled)
	case <-r.cleanup:
	}
}

func TestCancelledHTTPRequestStopsPendingRoute(t *testing.T) {
	router := &waitingRouter{started: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
	defer close(router.cleanup)
	h := &Inbound{router: router, username: "worker", passwordHash: sha256.Sum256([]byte("secret"))}
	server := httptest.NewUnstartedServer(http.HandlerFunc(h.serveHTTP))
	server.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		return context.WithValue(ctx, metadataKey{}, adapter.InboundContext{})
	}
	server.Start()
	defer server.Close()
	proxy, _ := url.Parse(server.URL)
	proxy.User = url.UserPassword("worker-session-account", "secret")
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/page", nil)
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-router.started:
	case <-time.After(time.Second):
		t.Fatal("proxy request did not reach router")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client request did not cancel")
	}
	select {
	case <-router.cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelled HTTP request left its route running")
	}
}
