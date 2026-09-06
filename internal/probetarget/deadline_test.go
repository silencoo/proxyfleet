package probetarget

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

type auditDeadlineConn struct {
	net.Conn
	deadline time.Time
}

func (c *auditDeadlineConn) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.Conn.SetDeadline(deadline)
}

func TestRegressionProbeHTTPHonorsConfiguredLongDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err == nil {
			_, _ = io.WriteString(server, "HTTP/1.1 204 No Content\r\n\r\n")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	conn := &auditDeadlineConn{Conn: client}
	_, err := ProbeHTTP(ctx, conn, "example.test", "/")
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if conn.deadline.Before(want.Add(-time.Millisecond)) {
		t.Fatalf("configured deadline shortened by %v (configured 30s, applied about %v)", want.Sub(conn.deadline), time.Until(conn.deadline).Round(time.Millisecond))
	}
}
