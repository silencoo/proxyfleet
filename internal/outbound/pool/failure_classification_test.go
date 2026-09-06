package pool

import (
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/sagernet/ws"
	"github.com/silencoo/proxyfleet/internal/probetarget"
)

func TestConnectionClosuresAndRefusalsAreTransient(t *testing.T) {
	for _, cause := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, syscall.ECONNREFUSED, syscall.EPIPE} {
		if !isTransientError(fmt.Errorf("probe transport: %w", cause)) {
			t.Errorf("connection fault was treated as a permanent protocol failure: %v", cause)
		}
	}
	for _, message := range []string{"invalid protocol handshake", "authentication rejected", "x509: certificate signed by unknown authority"} {
		if isTransientError(errors.New(message)) {
			t.Errorf("durable fault was treated as transient: %s", message)
		}
	}
}

func TestTransientHTTPStatusClassificationUsesStatusBoundaries(t *testing.T) {
	for _, message := range []string{"HTTP/1.1 429 Too Many Requests", "unexpected status: 503", "status code=429"} {
		if !isTransientError(errors.New(message)) {
			t.Errorf("expected transient classification for %q", message)
		}
	}
	for _, message := range []string{"node id 4291 failed", "connected to port 5030", "certificate serial 429"} {
		if isTransientError(errors.New(message)) {
			t.Errorf("numeric substring was misclassified as transient: %q", message)
		}
	}
}

func TestWebSocketHTTPFailuresKeepTransientStatusPolicy(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 503} {
		wantTransient := code == 429 || code == 503
		for _, err := range []error{ws.StatusError(code), fmt.Errorf("upgrade: %w", ws.StatusError(code)), fmt.Errorf("upgrade: %v", ws.StatusError(code))} {
			if got := isTransientError(err); got != wantTransient {
				t.Errorf("%q: transient=%v, want %v", err, got, wantTransient)
			}
		}
	}
}

func TestRemoteTimeoutTextRemainsTransient(t *testing.T) {
	for _, message := range []string{
		"read HTTP response: remote error: dial tcp 192.0.2.1:80: i/o timeout",
		"remote error: context deadline exceeded",
	} {
		if !isTransientError(errors.New(message)) {
			t.Errorf("remote timeout caused permanent classification: %q", message)
		}
	}
	for _, message := range []string{"invalid timeout setting", "timeout is not supported by protocol", "certificate expired"} {
		if isTransientError(errors.New(message)) {
			t.Errorf("durable configuration failure was treated as transient: %q", message)
		}
	}
}

func TestProbeTargetHTTPFailuresAreNotPermanentProxyFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		err := fmt.Errorf("probe: %w", &probetarget.HTTPStatusError{Code: status})
		if !isTransientError(err) {
			t.Errorf("target HTTP %d caused permanent classification", status)
		}
	}
}
