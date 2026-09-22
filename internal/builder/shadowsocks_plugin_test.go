package builder

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/silencoo/proxyfleet/internal/config"
)

func TestShadowsocksObfsPluginOptions(t *testing.T) {
	for _, tc := range []struct{ plugin, want string }{
		{"obfs-http;host=cdn.example", "obfs=http;obfs-host=cdn.example"},
		{"obfs-tls;obfs-host=cdn.example", "obfs=tls;obfs-host=cdn.example"},
		{"obfs-local;obfs=tls;obfs-host=cdn.example", "obfs=tls;obfs-host=cdn.example"},
		{"simple-obfs;obfs=http;obfs-host=cdn.example", "obfs=http;obfs-host=cdn.example"},
		{"obfs;mode=tls;host=cdn.example", "obfs=tls;obfs-host=cdn.example"},
		{"obfs-local", "obfs=http;obfs-host=example.com"},
		{"obfs-tls", "obfs=tls;obfs-host=example.com"},
		{"obfs-http;host=例子.test", "obfs=http;obfs-host=xn--fsqu00a.test"},
		{`obfs-http;host=\[2001\:db8\:\:1\]`, `obfs=http;obfs-host=[2001\:db8\:\:1]`},
		{`obfs-tls;host=2001\:db8\:\:1`, `obfs=tls;obfs-host=2001\:db8\:\:1`},
	} {
		t.Run(tc.plugin, func(t *testing.T) {
			for _, path := range []string{"", "/"} {
				opts, err := buildShadowsocksOptions("ss://aes-128-gcm:test@example.com:8388" + path + "?plugin=" + url.QueryEscape(tc.plugin))
				if err != nil {
					t.Fatal(err)
				}
				if opts.Plugin != "obfs-local" || opts.PluginOptions != tc.want {
					t.Fatalf("plugin=%q options=%q", opts.Plugin, opts.PluginOptions)
				}
			}
		})
	}
}

func TestShadowsocksObfsSubscriptionFormats(t *testing.T) {
	plugin := url.QueryEscape("obfs-local;obfs=tls;obfs-host=cdn.example")
	for _, link := range []string{
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:test")) + "@example.com:8388/?plugin=" + plugin,
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:test@example.com:8388")) + "?plugin=" + plugin,
		"shadowsocks://aes-128-gcm:test@example.com:8388/?plugin=" + plugin,
	} {
		for _, content := range []string{link, base64.StdEncoding.EncodeToString([]byte(link))} {
			nodes, err := config.ParseSubscriptionContent(content)
			if err != nil || len(nodes) != 1 {
				t.Fatalf("subscription parse: %v, count=%d", err, len(nodes))
			}
			opts, err := buildShadowsocksOptions(nodes[0].URI)
			if err != nil {
				t.Fatal(err)
			}
			if opts.Plugin != "obfs-local" || opts.PluginOptions != "obfs=tls;obfs-host=cdn.example" {
				t.Fatalf("options=%+v", opts)
			}
		}
	}
}

func TestClashShadowsocksPluginOptionsCannotInjectModes(t *testing.T) {
	nodes, err := config.ParseSubscriptionContent(`proxies:
  - name: malformed-obfs-host
    type: ss
    server: example.com
    port: 8388
    cipher: aes-128-gcm
    password: test
    plugin: obfs
    plugin-opts:
      host: 'cdn.example;mode=tls'
`)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("parse: %v", err)
	}
	if _, err := buildShadowsocksOptions(nodes[0].URI); err == nil {
		t.Fatal("host changed the protocol through an unescaped option delimiter")
	}
}

func TestShadowsocksObfsRejectsInvalidOptions(t *testing.T) {
	for _, plugin := range []string{
		"obfs-tls;mode=http", "obfs;mode=tls;obfs=http", "obfs;mode=invalid-secret",
		"obfs;mode=tls;mode=http", "obfs;host=a;obfs-host=b", "obfs;host=", "obfs;host=bad\r\nHost:secret",
		"obfs;host=http://secret/path", "obfs;host=secret/path", "obfs;host=secret@host",
		"obfs;host=secret\\", "obfs;host=secret\\;mode=tls", "obfs;unsupported=secret", "v2ray-plugin;host=secret",
		"obfs;mode=", "obfs;host=" + strings.Repeat("x", 256),
	} {
		t.Run(plugin, func(t *testing.T) {
			_, err := buildShadowsocksOptions("ss://aes-128-gcm:password@example.com:8388?plugin=" + url.QueryEscape(plugin))
			if err == nil {
				t.Fatal("accepted invalid plugin")
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password") {
				t.Fatalf("error leaked credentials/options: %s", err)
			}
		})
	}
	for _, query := range []string{"plugin=", "plugin=obfs-http&plugin=obfs-tls"} {
		if _, err := buildShadowsocksOptions("ss://aes-128-gcm:test@example.com:8388?" + query); err == nil {
			t.Fatalf("accepted ambiguous query %q", query)
		}
	}
}

func TestClashShadowsocksObfsSubscriptionBuild(t *testing.T) {
	for _, plugin := range []string{"obfs", "obfs-http", "obfs-tls", "obfs-local", "simple-obfs"} {
		mode := "http"
		if plugin == "obfs-tls" || plugin == "obfs" {
			mode = "tls"
		}
		t.Run(plugin, func(t *testing.T) {
			nodes, err := config.ParseSubscriptionContent(fmt.Sprintf(`proxies:
  - name: obfs-node
    type: ss
    server: 127.0.0.1
    port: 8388
    cipher: aes-128-gcm
    password: test-password
    plugin: %s
    plugin-opts:
      mode: %s
      host: cdn.example
`, plugin, mode))
			if err != nil || len(nodes) != 1 {
				t.Fatalf("nodes=%v err=%v", nodes, err)
			}
			out, err := buildNodeOutbound("ss", nodes[0].URI, false)
			if err != nil {
				t.Fatal(err)
			}
			opts := out.Options.(*option.ShadowsocksOutboundOptions)
			if opts.Plugin != "obfs-local" || opts.PluginOptions != "obfs="+mode+";obfs-host=cdn.example" {
				t.Fatalf("options=%+v", opts)
			}
		})
	}
}
