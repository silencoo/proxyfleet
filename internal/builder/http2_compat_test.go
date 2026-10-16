package builder

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/v2rayhttp"
	"golang.org/x/net/http2"
)

// sing-box's HTTP/2 reset uses a private x/net connection-pool hook. Compiling
// alone cannot verify its unsafe layout assumptions; exercise the real TLS
// transport and ensure a reset closes the old connection and can reconnect.
func TestHTTP2TransportResetDependencyCompatibility(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	transport := &http2.Transport{
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone(),
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func() net.Conn {
		t.Helper()
		var conn net.Conn
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { conn = info.Conn }}
		ctx := httptrace.WithClientTrace(context.Background(), trace)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 2 || resp.StatusCode != http.StatusNoContent || conn == nil {
			t.Fatalf("expected a successful HTTP/2 connection, got %s %d", resp.Proto, resp.StatusCode)
		}
		return conn
	}
	first := request()
	if next := request(); next != first {
		t.Fatal("HTTP/2 transport did not reuse its connection before reset")
	}
	if v2rayhttp.ResetTransport(transport) != transport {
		t.Fatal("reset unexpectedly replaced the HTTP/2 transport")
	}
	if next := request(); next == first {
		t.Fatal("reset left the old HTTP/2 connection in use")
	}
}
