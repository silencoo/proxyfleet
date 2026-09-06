package config

import (
	"net/url"
	"testing"
)

func TestRegressionClashShadowsocksMustNotSilentlyDropPlugin(t *testing.T) {
	nodes, err := parseClashYAML(`proxies:
  - name: synthetic-plugin-node
    type: ss
    server: example.test
    port: 443
    cipher: aes-128-gcm
    password: synthetic-test-only
    plugin: obfs
    plugin-opts:
      mode: tls
      host: example.test
`)
	if err != nil || len(nodes) == 0 {
		return // Explicit rejection of an unsupported plugin is safe.
	}
	u, err := url.Parse(nodes[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("plugin") == "" {
		t.Fatal("accepted plugin-required Shadowsocks node as plain Shadowsocks; plugin and plugin-opts were lost")
	}
}
