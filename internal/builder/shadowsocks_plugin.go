package builder

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/sagernet/sing-box/transport/sip003"
	"golang.org/x/net/idna"
)

// Normalize the supported SIP003 aliases to sing-box's built-in transport.
// Never return raw plugin options in an error: share links can contain secrets.
func shadowsocksPlugin(query url.Values, server string) (string, string, error) {
	values, present := query["plugin"]
	if !present {
		return "", "", nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", "", errors.New("invalid Shadowsocks plugin parameter")
	}
	name, rawOptions, _ := strings.Cut(values[0], ";")
	mode := ""
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "obfs-http":
		mode = "http"
	case "obfs-tls":
		mode = "tls"
	case "obfs", "obfs-local", "simple-obfs":
	default:
		return "", "", errors.New("Shadowsocks plugin is not supported; supported plugins: obfs-http, obfs-tls, obfs-local, simple-obfs, obfs")
	}
	args, err := sip003.ParsePluginOptions(rawOptions)
	if err != nil {
		return "", "", errors.New("invalid Shadowsocks obfs options")
	}
	host, hostSet := server, false
	for key, values := range args {
		if len(values) != 1 {
			return "", "", errors.New("duplicate Shadowsocks obfs option")
		}
		value := values[0]
		switch key {
		case "obfs", "mode":
			if value != "http" && value != "tls" {
				return "", "", errors.New("Shadowsocks obfs mode must be http or tls")
			}
			if mode != "" && mode != value {
				return "", "", errors.New("conflicting Shadowsocks obfs modes")
			}
			mode = value
		case "obfs-host", "host":
			if hostSet && host != value {
				return "", "", errors.New("conflicting Shadowsocks obfs hosts")
			}
			host, hostSet = value, true
		default:
			return "", "", errors.New("unsupported Shadowsocks obfs option; supported options: obfs/mode, obfs-host/host")
		}
	}
	if mode == "" {
		mode = "http"
	}
	host, err = obfsHost(host, mode)
	if err != nil {
		return "", "", err
	}
	// The validated host contains no SIP003 delimiters except IPv6 colons.
	host = strings.ReplaceAll(host, ":", `\:`)
	return "obfs-local", "obfs=" + mode + ";obfs-host=" + host, nil
}

func obfsHost(host, mode string) (string, error) {
	invalid := errors.New("invalid Shadowsocks obfs host; expected a hostname or IP address without a port")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Zone() == "" {
		if ip.Is6() && mode == "http" {
			return "[" + ip.String() + "]", nil
		}
		return ip.String(), nil
	}
	host, err := idna.Lookup.ToASCII(host)
	if err != nil || len(host) == 0 || len(host) > 253 {
		return "", invalid
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalid
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return "", invalid
			}
		}
	}
	return host, nil
}
