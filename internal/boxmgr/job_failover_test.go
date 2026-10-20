package boxmgr

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

// Real HTTP, HTTPS CONNECT and SOCKS5 clients retain their original credentials
// and session ID. No client management-API call is needed before or after failure.
func TestApplicationFailoverWithRealProxyClients(t *testing.T) {
	for _, ingress := range []string{"endpoint", "shared"} {
		t.Run(ingress, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked")
				}
				if r.URL.Path == "/challenge" {
					w.WriteHeader(403)
					return
				}
				_, _ = io.WriteString(w, "ok")
			})
			target := httptest.NewServer(handler)
			defer target.Close()
			secure := httptest.NewTLSServer(handler)
			defer secure.Close()
			endpoint := config.EndpointConfig{Name: "browser", Address: "127.0.0.1", Port: findManagerTestPort(t), Profile: "browser", Username: "worker", Password: "proxy-secret"}
			job := config.JobConfig{Name: "browser", Mode: "pinned", Profile: "browser", Endpoint: "browser", FailurePolicy: "failover", SessionTTL: "48h", MaxAccountsPerNode: 1}
			username, password := "worker-session-stable-session", endpoint.Password
			if ingress == "shared" {
				endpoint.Profile = ""
				job.Ingress = "shared"
				job.Endpoint = ""
				job.Password = "browser-application-secret"
				username, password = "stable-session", job.Password
			}
			cfg := &config.Config{Mode: "pool", LogLevel: "error", Profiles: []config.ProfileConfig{{Name: "browser"}}, Endpoints: []config.EndpointConfig{endpoint}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Jobs: []config.JobConfig{job}, Nodes: []config.NodeConfig{{Name: "first", URI: "socks5://" + startTestSOCKS5Proxy(t)}, {Name: "second", URI: "socks5://" + startTestSOCKS5Proxy(t)}}}
			cfg.SetFilePath(filepath.Join(t.TempDir(), "config.yaml"))
			if err := cfg.NormalizeWithPortMap(nil); err != nil {
				t.Fatal(err)
			}
			cfg.SubscriptionRefresh.MinAvailableNodes = 0
			start := func() *Manager {
				m := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: "manual"})
				if err := m.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = m.Close() })
				return m
			}
			request := func(scheme, destination string) int {
				t.Helper()
				proxy := &url.URL{Scheme: scheme, Host: net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))), User: url.UserPassword(username, password)}
				tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				defer tr.CloseIdleConnections()
				resp, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get(destination)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				_, err = io.Copy(io.Discard, resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode
			}
			m := start()
			if status := request("http", target.URL); status != 200 {
				t.Fatal(status)
			}
			original, ok := m.jobStore.Lease("browser", "stable-session")
			if !ok {
				t.Fatal("client did not create binding")
			}
			if status := request("http", secure.URL+"/challenge"); status != 403 {
				t.Fatal(status)
			}
			unchanged, _ := m.jobStore.Lease("browser", "stable-session")
			if unchanged.Version() != original.Version() {
				t.Fatal("HTTP challenge triggered failover")
			}
			if err := m.MonitorManager().ManualBlacklist(original.Node, time.Hour); err != nil {
				t.Fatal(err)
			}
			for _, scheme := range []string{"http", "socks5"} {
				for _, destination := range []string{secure.URL, target.URL} {
					if status := request(scheme, destination); status != 200 {
						t.Fatal("client did not recover", scheme, status)
					}
				}
			}
			recovered, _ := m.jobStore.Lease("browser", "stable-session")
			if recovered.Node == original.Node || recovered.FailoverCount != 1 || !recovered.ExpiresAt.Equal(original.ExpiresAt) || recovered.Policy != original.Policy {
				t.Fatal("recovery changed identity or lifetime", recovered)
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			m = start()
			if status := request("socks5", secure.URL); status != 200 {
				t.Fatal(status)
			}
			restored, _ := m.jobStore.Lease("browser", "stable-session")
			if restored.Node != recovered.Node || restored.Version() != recovered.Version() || restored.FailoverCount != 1 {
				t.Fatal("restart lost recovered identity")
			}
		})
	}
}
