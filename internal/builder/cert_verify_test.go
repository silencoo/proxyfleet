package builder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/silencoo/proxyfleet/internal/config"
)

func TestCertificateVerificationPrecedenceAcrossProtocols(t *testing.T) {
	for _, uri := range []string{
		"anytls://pass@example.com:443?", "anytls://pass@example.com:443?security=none&", "anytls://pass@example.com:443?security=tls&",
		"vless://uuid@example.com:443?security=tls&", "vmess://uuid@example.com:443?security=tls&", "trojan://pass@example.com:443?",
		"hysteria://example.com:443?auth=pass&", "hysteria2://pass@example.com:443?", "tuic://uuid:pass@example.com:443?", "https://example.com:443?",
	} {
		for _, global := range []bool{false, true} {
			for _, mode := range []string{config.CertVerifyDefault, config.CertVerifyOverride} {
				for _, node := range []string{"", "allowInsecure=1", "insecure=true", "allowInsecure=0", "insecure=false", "skip-cert-verify=false"} {
					t.Run(fmt.Sprintf("%s%s/global=%t/%s", uri, node, global, mode), func(t *testing.T) {
						out, err := buildNodeOutboundWithPolicy("node", uri+node, global, mode)
						if err != nil {
							t.Fatal(err)
						}
						want := global
						if mode == config.CertVerifyDefault && node != "" {
							want = node == "allowInsecure=1" || node == "insecure=true"
						}
						tls := out.Options.(option.OutboundTLSOptionsWrapper).TakeOutboundTLSOptions()
						if tls == nil || !tls.Enabled || tls.Insecure != want {
							t.Fatalf("TLS=%+v want insecure=%t", tls, want)
						}
					})
				}
			}
		}
	}
}

func TestVMessJSONVerificationPrecedence(t *testing.T) {
	for _, field := range []string{"allowInsecure", "insecure"} {
		for _, node := range []any{nil, false, true, "0", "1", "false", "true", 0, 1} {
			for _, global := range []bool{false, true} {
				for _, mode := range []string{"default", "override"} {
					data := map[string]any{"add": "example.com", "port": 443, "id": "uuid", "tls": "tls"}
					if node != nil {
						data[field] = node
					}
					encoded, _ := json.Marshal(data)
					out, err := buildNodeOutboundWithPolicy("node", "vmess://"+base64.StdEncoding.EncodeToString(encoded), global, mode)
					if err != nil {
						t.Fatal(err)
					}
					want := global
					if mode == "default" && node != nil {
						want = fmt.Sprint(node) == "1" || fmt.Sprint(node) == "true"
					}
					if out.Options.(*option.VMessOutboundOptions).TLS.Insecure != want {
						t.Fatalf("field=%s node=%v global=%t mode=%s", field, node, global, mode)
					}
				}
			}
		}
	}
}

func TestClashExplicitVerificationValuesSurviveImport(t *testing.T) {
	for _, protocol := range []string{"anytls", "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic"} {
		for _, node := range []string{"", "    skip-cert-verify: false\n", "    skip-cert-verify: true\n"} {
			nodes, err := config.ParseSubscriptionContent(fmt.Sprintf("proxies:\n  - name: test\n    type: %s\n    server: example.com\n    port: 443\n    uuid: uuid\n    password: pass\n    tls: true\n%s", protocol, node))
			if err != nil || len(nodes) != 1 {
				t.Fatalf("%s: parse=%v nodes=%d", protocol, err, len(nodes))
			}
			for _, global := range []bool{false, true} {
				out, err := buildNodeOutbound("node", nodes[0].URI, global)
				if err != nil {
					t.Fatal(err)
				}
				want := global
				if node != "" {
					want = node == "    skip-cert-verify: true\n"
				}
				if out.Options.(option.OutboundTLSOptionsWrapper).TakeOutboundTLSOptions().Insecure != want {
					t.Fatalf("%s node=%q global=%t", protocol, node, global)
				}
			}
		}
	}
}

func TestBuildUsesGlobalVerificationOverride(t *testing.T) {
	cfg := &config.Config{Mode: "pool", SkipCertVerifyMode: "override", Nodes: []config.NodeConfig{{Name: "tls", URI: "anytls://pass@example.com:443?insecure=1"}}}
	opts, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, out := range opts.Outbounds {
		if tls, ok := out.Options.(*option.AnyTLSOutboundOptions); ok {
			found = true
			if tls.TLS.Insecure {
				t.Fatal("Build ignored override")
			}
		}
	}
	if !found {
		t.Fatal("missing AnyTLS outbound")
	}
}
