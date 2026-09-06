package probetarget

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const maximumProbeResponseHeaders = 16 * 1024

// HTTPStatusError means the target responded, but not successfully. It is not
// evidence of a permanent proxy-protocol failure (the target may be down or
// rejecting a particular path, region, or request rate).
type HTTPStatusError struct{ Code int }

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status: %d", e.Code)
}

// ProbeHTTP validates a bounded HTTP response on an already connected socket.
// It closes the single-use connection without downloading the response body or
// following redirects. Both live probes and subscription preflight use it.
func ProbeHTTP(ctx context.Context, conn net.Conn, host, requestURI string) (time.Duration, error) {
	defer conn.Close()
	if requestURI == "" {
		requestURI = "/"
	}
	if host == "" || !strings.HasPrefix(requestURI, "/") || strings.ContainsAny(host, "\r\n\t /\\@?#") || strings.ContainsAny(requestURI, "\r\n") {
		return 0, fmt.Errorf("invalid HTTP probe target")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+requestURI, nil)
	if err != nil {
		return 0, fmt.Errorf("invalid HTTP probe request")
	}
	request.Close = true
	request.Header.Set("User-Agent", "Mozilla/5.0")
	deadline := time.Now().Add(10 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	start := time.Now()
	if err := request.Write(conn); err != nil {
		return 0, fmt.Errorf("write request: %w", err)
	}
	reader := bufio.NewReader(io.LimitReader(conn, maximumProbeResponseHeaders))
	for interim := 0; interim < 5; interim++ {
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			return 0, fmt.Errorf("read HTTP response: %w", err)
		}
		if response.StatusCode >= 100 && response.StatusCode < 200 && response.StatusCode != http.StatusSwitchingProtocols {
			_ = response.Body.Close() // Informational responses have no body.
			continue
		}
		// Measure the response, not potentially slow connection teardown.
		responseLatency := time.Since(start)
		// Close the socket first: Body.Close must not drain an unbounded body.
		_ = conn.Close()
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return 0, &HTTPStatusError{Code: response.StatusCode}
		}
		return responseLatency, nil
	}
	return 0, fmt.Errorf("too many informational HTTP responses")
}
