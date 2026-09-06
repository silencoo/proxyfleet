package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/sagernet/ws"
	"github.com/silencoo/proxyfleet/internal/probetarget"
)

func TestFormatProbeFailureRedactsCredentialsAndOpaqueURIData(t *testing.T) {
	err := errors.New("request https://api-user:api-pass@probe.example/private/token?key=secret: context deadline exceeded")
	formatted := FormatProbeFailure("node-a", "vless://uuid:password@node.example:443/private?token=secret", err)
	for _, secret := range []string{"uuid", "password", "api-user", "api-pass", "/private", "key=secret", "token=secret"} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, formatted)
		}
	}
	for _, host := range []string{"node.example", "probe.example"} {
		if strings.Contains(formatted, host) {
			t.Fatalf("diagnostic leaked hostname %q: %s", host, formatted)
		}
	}
	if !strings.Contains(formatted, "vless@[redacted-host]:443") || !strings.Contains(formatted, "https://[redacted-host]") {
		t.Fatalf("diagnostic lost protocol/port context: %s", formatted)
	}
}

func TestFormatProbeFailureRedactsBase64StyleURIHost(t *testing.T) {
	const payload = "eyJhZGQiOiJzZWNyZXQuZXhhbXBsZSIsInBzIjoic2VjcmV0LW5hbWUifQ=="
	formatted := FormatProbeFailure("node-a", "vmess://"+payload, errors.New("failed vmess://"+payload))
	if strings.Contains(formatted, payload) || strings.Contains(formatted, "secret") {
		t.Fatalf("diagnostic leaked opaque vmess payload: %s", formatted)
	}
}

func TestSanitizeProbeErrorRemovesControlCharactersAndBareSecrets(t *testing.T) {
	secret := "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	got := SanitizeProbeError(fmt.Errorf("failed\r\nfor token=%s password:plain-text user:pass@example.test", secret))
	for _, forbidden := range []string{"\r", "\n", secret, "plain-text", "user:pass@"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized error still contains %q: %q", forbidden, got)
		}
	}
}

func TestSanitizeProbeErrorIsBounded(t *testing.T) {
	got := SanitizeProbeError(errors.New(strings.Repeat("failure ", 200)))
	if len(got) > maxSanitizedProbeErrorLength+3 {
		t.Fatalf("sanitized error length = %d", len(got))
	}
}

func TestProbeErrorClassification(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "read_timeout"},
		{context.Canceled, "cancelled"},
		{errors.New("x509: certificate signed by unknown authority"), "tls_failed"},
		{errors.New("dial tcp 203.0.113.1:443: connection refused"), "dial_refused"},
	}
	for _, test := range tests {
		got, _ := classifyProbeError(test.err)
		if got != test.want {
			t.Errorf("classifyProbeError(%q) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestProbeLogSummariesUseEnglish(t *testing.T) {
	for _, message := range []string{
		"127.127.127.1", "unexpected status: 503", "tls: bad certificate",
		"malformed response", "connection refused", "network is unreachable",
		"dial tcp: i/o timeout", "dial udp: failed", "i/o timeout", "EOF", "unrecognized failure",
	} {
		formatted := FormatProbeFailure("node-a", "socks5://example.test:1080", errors.New(message))
		if strings.ContainsFunc(formatted, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("Chinese summary in backend log: %s", formatted)
		}
	}
	_, summary := classifyProbeError(context.DeadlineExceeded)
	if summary != "Node response timed out or the connection is unstable" {
		t.Fatalf("unexpected timeout summary: %q", summary)
	}
}

func TestProbeHTTPStatusLogsIdentifyFailureStage(t *testing.T) {
	for _, code := range []int{403, 429, 503, 599} {
		for _, test := range []struct {
			name, category, summary string
			err                     error
		}{
			{"target", "http_status", "Probe target returned", fmt.Errorf("probe: %w", &probetarget.HTTPStatusError{Code: code})},
			{"websocket", "transport_handshake", "WebSocket handshake rejected", fmt.Errorf("connect: %w", ws.StatusError(code))},
			{"flattened websocket", "transport_handshake", "WebSocket handshake rejected", fmt.Errorf("connect: %v", ws.StatusError(code))},
		} {
			t.Run(fmt.Sprintf("%s/%d", test.name, code), func(t *testing.T) {
				category, summary := classifyProbeError(test.err)
				if category != test.category || !strings.Contains(summary, test.summary) || !strings.Contains(summary, fmt.Sprintf("HTTP %d", code)) {
					t.Fatalf("wrong HTTP failure stage: category=%q summary=%q", category, summary)
				}
				if code == 503 && !strings.Contains(summary, "Service Unavailable") {
					t.Fatalf("missing status explanation: %q", summary)
				}
			})
		}
	}
	category, _ := classifyProbeError(fmt.Errorf("upgrade: %w", ws.ErrHandshakeBadStatus))
	if category != "transport_handshake" {
		t.Fatalf("WebSocket handshake sentinel confused with probe target: %q", category)
	}
	category, _ = classifyProbeError(errors.New("unexpected HTTP response status: 5030"))
	if category != "other" {
		t.Fatalf("non-status number misclassified: %q", category)
	}
}

func TestWebSocket503DiagnosticExplainsAndRedactsFailure(t *testing.T) {
	err := fmt.Errorf("connect wss://user:secret@proxy.example/private?token=hidden: %w", ws.StatusError(503))
	formatted := FormatProbeFailure("node-a", "vless://uuid@proxy.example:443?path=/private", err)
	for _, want := range []string{"(transport_handshake)", "WebSocket handshake rejected with HTTP 503 (Service Unavailable)", "proxy server or CDN"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("diagnostic missing %q: %s", want, formatted)
		}
	}
	for _, forbidden := range []string{"secret", "hidden", "uuid", "/private", "proxy.example", "Probe target returned", "unclassified"} {
		if strings.Contains(formatted, forbidden) {
			t.Errorf("diagnostic contains %q: %s", forbidden, formatted)
		}
	}
}

func TestTLSProbeSummariesExplainCertificateFailures(t *testing.T) {
	for _, test := range []struct{ message, want string }{
		{"x509: certificate relies on legacy Common Name field, use SANs instead", "provider must replace it"},
		{"x509: cannot validate certificate for 192.0.2.1 because it doesn't contain any IP SANs", "configured TLS server name (SNI)"},
		{"x509: certificate is not valid for any names, but wanted to match example.test", "hostname mismatch"},
		{"x509: certificate is valid for other.test, not example.test", "hostname mismatch"},
		{"x509: certificate has expired or is not yet valid", "system clock"},
		{"x509: certificate signed by unknown authority", "certificate chain"},
		{"tls: handshake failure", "TLS handshake or certificate verification failed"},
	} {
		category, summary := classifyProbeError(errors.New(test.message))
		if category != "tls_failed" || !strings.Contains(summary, test.want) {
			t.Errorf("%q: category=%q, summary=%q", test.message, category, summary)
		}
		if strings.ContainsFunc(summary, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("non-English TLS summary: %s", summary)
		}
	}
}
