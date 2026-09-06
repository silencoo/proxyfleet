package probetarget

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type slowCloseConn struct {
	net.Conn
	once sync.Once
}

func (c *slowCloseConn) Close() error {
	c.once.Do(func() { time.Sleep(40 * time.Millisecond) })
	return c.Conn.Close()
}

func TestProbeHTTPLatencyExcludesConnectionTeardown(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer server.Close()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
			return
		}
		_, _ = io.WriteString(server, "HTTP/1.1 204 No Content\r\n\r\n")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	latency, err := ProbeHTTP(ctx, &slowCloseConn{Conn: client}, "example.test", "/")
	elapsed := time.Since(start)
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	if elapsed-latency < 30*time.Millisecond {
		t.Fatalf("connection teardown inflated probe latency: reported %v, wall time %v", latency, elapsed)
	}
}

func TestProbeHTTPValidatesResponsesAndPreservesRequestTarget(t *testing.T) {
	for _, test := range []struct {
		name, response string
		wantError      bool
	}{
		{"ok", "HTTP/1.1 200 OK\r\nContent-Length: 1000000000\r\n\r\n", false},
		{"no-content", "HTTP/1.1 204 No Content\r\n\r\n", false},
		{"redirect", "HTTP/1.1 302 Found\r\nLocation: https://unused.invalid/\r\n\r\n", false},
		{"informational", "HTTP/1.1 103 Early Hints\r\n\r\nHTTP/1.1 204 No Content\r\n\r\n", false},
		{"forbidden", "HTTP/1.1 403 Forbidden\r\n\r\n", true},
		{"rate-limit", "HTTP/1.1 429 Too Many Requests\r\n\r\n", true},
		{"unavailable", "HTTP/1.1 503 Service Unavailable\r\n\r\n", true},
		{"single-byte", "H", true},
		{"non-http", "not HTTP\r\n\r\n", true},
		{"truncated-headers", "HTTP/1.1 200 OK\r\nX-Unfinished: value", true},
		{"oversize-headers", "HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("x", maximumProbeResponseHeaders) + "\r\n\r\n", true},
		{"too-many-interim", strings.Repeat("HTTP/1.1 103 Early Hints\r\n\r\n", 6), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			requests := make(chan *http.Request, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer server.Close()
				request, err := http.ReadRequest(bufio.NewReader(server))
				if err != nil {
					return
				}
				requests <- request
				_, _ = io.WriteString(server, test.response)
				if !test.wantError {
					_, _ = io.Copy(io.Discard, server) // Wait for client close, not EOF from us.
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			elapsed, err := ProbeHTTP(ctx, client, "example.test:8080", "/a%2Fb?check=1")
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected response result: %v", err)
			}
			if !test.wantError && elapsed >= 500*time.Millisecond {
				t.Fatalf("probe waited for body/connection close: %v", elapsed)
			}
			<-finished
			select {
			case request := <-requests:
				if request.RequestURI != "/a%2Fb?check=1" || request.Host != "example.test:8080" || !request.Close {
					t.Fatalf("incorrect probe request: %+v", request)
				}
			default:
				t.Fatal("probe did not send an HTTP request")
			}
		})
	}
}
