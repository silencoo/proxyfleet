package buildinfo

import "testing"

func TestCurrentReturnsIsolatedCapabilityMap(t *testing.T) {
	first := Current()
	first.Capabilities["quic"] = !first.Capabilities["quic"]
	second := Current()
	if first.Capabilities["quic"] == second.Capabilities["quic"] {
		t.Fatal("Current returned shared capability storage")
	}
	if second.Product != "ProxyFleet" || second.GOOS == "" || second.GOARCH == "" {
		t.Fatalf("incomplete build info: %+v", second)
	}
}

func TestCurrentAlwaysReportsBaseProtocols(t *testing.T) {
	info := Current()
	required := map[string]bool{"http": false, "socks5": false, "vless": false, "vmess": false}
	for _, protocol := range info.Protocols {
		if _, exists := required[protocol]; exists {
			required[protocol] = true
		}
	}
	for protocol, present := range required {
		if !present {
			t.Errorf("base protocol %q missing from %+v", protocol, info.Protocols)
		}
	}
}
