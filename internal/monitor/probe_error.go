package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/sagernet/ws"
	"github.com/silencoo/proxyfleet/internal/probetarget"
)

var probeURLPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)
var sensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|auth|authorization|uuid|secret|api[_-]?key)\s*[:=]\s*[^\s,;]+`)
var userInfoPattern = regexp.MustCompile(`[^\s/@:]+:[^\s/@]+@`)
var longOpaqueValuePattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}={0,2}\b`)
var websocketProbeStatusPattern = regexp.MustCompile(`(?i)\bunexpected HTTP response status:\s*([1-5][0-9]{2})\b`)

const maxSanitizedProbeErrorLength = 512

// nodeBrief returns only protocol and endpoint. Userinfo, path, query and
// fragment can contain UUIDs, tokens or subscription credentials and are never
// copied into diagnostics.
func nodeBrief(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return ""
	}
	if port := u.Port(); port != "" {
		return fmt.Sprintf("%s@[redacted-host]:%s", strings.ToLower(u.Scheme), port)
	}
	return fmt.Sprintf("%s@[redacted-host]", strings.ToLower(u.Scheme))
}

// SanitizeProbeError removes credentials and opaque URI components from an
// error before it is persisted in health-state.yaml or exposed by the API.
func SanitizeProbeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, err.Error())
	message = probeURLPattern.ReplaceAllStringFunc(message, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,;)]}")
		suffix := raw[len(trimmed):]
		u, parseErr := url.Parse(trimmed)
		if parseErr != nil || u.Scheme == "" || u.Host == "" {
			return "[redacted-uri]" + suffix
		}
		redacted := strings.ToLower(u.Scheme) + "://[redacted-host]"
		if port := u.Port(); port != "" {
			redacted += ":" + port
		}
		return redacted + suffix
	})
	message = sensitiveAssignmentPattern.ReplaceAllString(message, "$1=[redacted]")
	message = userInfoPattern.ReplaceAllString(message, "[redacted]@")
	message = longOpaqueValuePattern.ReplaceAllString(message, "[redacted]")
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > maxSanitizedProbeErrorLength {
		message = message[:maxSanitizedProbeErrorLength] + "..."
	}
	return message
}

func classifyProbeError(err error) (category, summary string) {
	if err == nil {
		return "", ""
	}
	// The probe target's response and the proxy's WebSocket handshake are
	// different stages. Prefer their typed causes before inspecting wrappers.
	var targetStatus *probetarget.HTTPStatusError
	if errors.As(err, &targetStatus) {
		return "http_status", "Probe target returned " + probeHTTPStatusLabel(targetStatus.Code)
	}
	var websocketStatus ws.StatusError
	if errors.As(err, &websocketStatus) {
		return "transport_handshake", websocketProbeStatusSummary(int(websocketStatus))
	}
	// Some transport wrappers flatten the original error instead of using %w.
	if match := websocketProbeStatusPattern.FindStringSubmatch(err.Error()); match != nil {
		code, _ := strconv.Atoi(match[1])
		return "transport_handshake", websocketProbeStatusSummary(code)
	}
	var netErr net.Error
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ws.ErrHandshakeBadStatus):
		return "transport_handshake", "WebSocket handshake returned an unexpected HTTP status; check the proxy server or CDN"
	case strings.Contains(lower, "unexpected http status"):
		return "http_status", "Probe target returned an unsuccessful HTTP status"
	case strings.Contains(lower, "127.127.127.1"):
		return "addr_invalid", "Invalid node address (possibly a subscription placeholder or parsing error)"
	case strings.Contains(lower, "http-upgrade"), strings.Contains(lower, "httpupgrade"),
		strings.Contains(lower, "unexpected status"), strings.Contains(lower, "v2ray-"):
		return "transport_handshake", "Transport handshake failed (possible server or CDN rejection)"
	case strings.Contains(lower, "tls:"), strings.Contains(lower, "tls handshake"),
		strings.Contains(lower, "certificate"), strings.Contains(lower, "x509:"):
		return "tls_failed", tlsProbeErrorSummary(lower)
	case strings.Contains(lower, "unknown version"), strings.Contains(lower, "malformed"):
		return "proto_mismatch", "Unexpected protocol response"
	case strings.Contains(lower, "connection refused"):
		return "dial_refused", "Node port refused the connection"
	case strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		return "dial_no_route", "Node network is unreachable"
	case strings.Contains(lower, "dial tcp"), strings.Contains(lower, "dial udp"):
		if errors.As(err, &netErr) && netErr.Timeout() || strings.Contains(lower, "timeout") {
			return "dial_timeout", "Connection to the node timed out"
		}
		return "dial_failed", "Connection to the node failed"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout(),
		strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"):
		return "read_timeout", "Node response timed out or the connection is unstable"
	case strings.Contains(lower, "eof"), strings.Contains(lower, "reset by peer"),
		strings.Contains(lower, "broken pipe"), strings.Contains(lower, "connection reset"):
		return "conn_reset", "Probe connection was closed by the peer"
	case errors.Is(err, context.Canceled):
		return "cancelled", "Probe was cancelled"
	default:
		return "other", "Probe failed (unclassified error)"
	}
}

func probeHTTPStatusLabel(code int) string {
	label := fmt.Sprintf("HTTP %d", code)
	if reason := http.StatusText(code); reason != "" {
		label += " (" + reason + ")"
	}
	return label
}

func websocketProbeStatusSummary(code int) string {
	return "WebSocket handshake rejected with " + probeHTTPStatusLabel(code) + "; check the proxy server or CDN"
}

func tlsProbeErrorSummary(message string) string {
	switch {
	case strings.Contains(message, "legacy common name"):
		return "Certificate lacks subject alternative names (SANs); the provider must replace it"
	case strings.Contains(message, "doesn't contain any ip sans"):
		return "Certificate does not cover the node IP; check the configured TLS server name (SNI)"
	case strings.Contains(message, "not valid for any names"),
		strings.Contains(message, "certificate is valid for"):
		return "Certificate hostname mismatch; check the configured TLS server name (SNI) and provider certificate"
	case strings.Contains(message, "expired or is not yet valid"):
		return "Certificate is expired or not yet valid; check the system clock and provider certificate"
	case strings.Contains(message, "unknown authority"):
		return "Certificate issuer is not trusted; check the provider certificate chain or configured trust roots"
	default:
		return "TLS handshake or certificate verification failed"
	}
}

// FormatProbeFailure produces a credential-free, single-line diagnostic.
func FormatProbeFailure(tag, uri string, err error) string {
	category, summary := classifyProbeError(err)
	var b strings.Builder
	b.WriteString(tag)
	if brief := nodeBrief(uri); brief != "" {
		b.WriteString(" [")
		b.WriteString(brief)
		b.WriteByte(']')
	}
	b.WriteString(" (")
	b.WriteString(category)
	b.WriteString(") ")
	b.WriteString(summary)
	if detail := SanitizeProbeError(err); detail != "" {
		b.WriteString(" | ")
		b.WriteString(detail)
	}
	return b.String()
}
