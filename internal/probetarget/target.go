// Package probetarget provides one strict parser shared by configuration,
// runtime health checks, and the management API.
package probetarget

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Target is a validated HTTP health-check destination.
type Target struct {
	Host       string
	Port       uint16
	TLS        bool
	RequestURI string
}

// HTTPAuthority retains IPv6 brackets and any port that is not the default for
// the request scheme. The TLS server name remains the unbracketed Host.
func HTTPAuthority(host string, port uint16, tls bool) string {
	if (tls && port == 443) || (!tls && port == 80) {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// Parse accepts an HTTP(S) URL or an explicit host:port. An empty value is a
// deliberate disabled state and is reported with ready=false.
func Parse(value string) (target Target, ready bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Target{}, false, nil
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return Target{}, false, errors.New("probe target contains control characters")
		}
	}

	host := ""
	portText := ""
	target.RequestURI = "/"
	if strings.Contains(value, "://") {
		parsed, parseErr := url.Parse(value)
		if parseErr != nil {
			return Target{}, false, fmt.Errorf("invalid probe target URL: %w", parseErr)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http":
		case "https":
			target.TLS = true
		default:
			return Target{}, false, fmt.Errorf("unsupported probe target scheme %q", parsed.Scheme)
		}
		if parsed.User != nil {
			return Target{}, false, errors.New("probe target must not contain userinfo")
		}
		host = parsed.Hostname()
		portText = parsed.Port()
		target.RequestURI = parsed.RequestURI()
		if strings.HasSuffix(parsed.Host, ":") {
			return Target{}, false, errors.New("probe target has an empty port")
		}
		if portText == "" {
			if target.TLS {
				portText = "443"
			} else {
				portText = "80"
			}
		}
	} else {
		var splitErr error
		host, portText, splitErr = net.SplitHostPort(value)
		if splitErr != nil {
			return Target{}, false, errors.New("probe target must be an HTTP(S) URL or host:port")
		}
	}

	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || strings.ContainsAny(host, " /\\@") {
		return Target{}, false, errors.New("probe target has an invalid host")
	}
	port, parseErr := strconv.Atoi(portText)
	if parseErr != nil || port < 1 || port > 65535 {
		return Target{}, false, fmt.Errorf("probe target port %q is outside 1..65535", portText)
	}
	target.Host = host
	target.Port = uint16(port)
	return target, true, nil
}
