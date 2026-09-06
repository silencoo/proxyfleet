package pool

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"syscall"

	"github.com/silencoo/proxyfleet/internal/probetarget"
)

var transientHTTPStatusPattern = regexp.MustCompile(`(?i)\b(?:http(?:/\d(?:\.\d)?)?\s+|status(?:\s+code)?\s*[:=]?\s*)(?:429|503)\b`)

// isTransientError classifies typed network failures first. A narrow text
// fallback remains necessary because several proxy protocols wrap remote HTTP
// status and transport errors without exposing a structured cause.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *probetarget.HTTPStatusError
	if errors.As(err, &statusErr) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	if transientHTTPStatusPattern.MatchString(message) {
		return true
	}
	for _, marker := range []string{
		// Remote proxy failures can arrive as plain text, without a net.Error
		// or a wrapped context cause. Keep the fallback specific to actual
		// timeout messages, not arbitrary mentions of timeout configuration.
		"i/o timeout", "context deadline exceeded",
		"too many requests", "service unavailable", "connection reset",
		"reset by peer", "temporarily unavailable", "connection refused",
		"broken pipe", "use of closed network connection",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
