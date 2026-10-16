package boxmgr

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
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

func TestSharedApplicationsHTTPAndSOCKS(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacy), func(t *testing.T) { testSharedApplications(t, legacy) })
	}
}
func testSharedApplications(t *testing.T, legacy bool) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy password leaked upstream")
		}
		io.WriteString(w, "shared target")
	})
	target := httptest.NewServer(handler)
	defer target.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	port := findManagerTestPort(t)
	endpoint := config.EndpointConfig{Name: "default", Address: "127.0.0.1", Port: port, Username: "fleet", Password: "legacy-secret"}
	cfg := &config.Config{Mode: "pool", LogLevel: "error", Listener: config.ListenerConfig{Address: endpoint.Address, Port: port, Username: endpoint.Username, Password: endpoint.Password}, Endpoints: []config.EndpointConfig{endpoint}, Profiles: []config.ProfileConfig{{Name: "one", NameRegex: "^one$"}, {Name: "two", NameRegex: "^two$"}}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Pool: config.PoolConfig{RetryAttempts: 3}, Jobs: []config.JobConfig{
		{Name: "a", Mode: "pinned", Ingress: "shared", Password: "application-a-secret", Profile: "one", MaxAccountsPerNode: 1},
		{Name: "b", Mode: "pinned", Ingress: "shared", Password: "application-b-secret", Profile: "one", MaxAccountsPerNode: 1},
		{Name: "c", Mode: "pooled", Ingress: "shared", Password: "application-c-secret", Profile: "two"},
	}}
	dedicatedPort := findManagerTestPort(t)
	if legacy {
		cfg.Endpoints = nil
	} else {
		cfg.Endpoints = append(cfg.Endpoints, config.EndpointConfig{Name: "dedicated", Address: "127.0.0.1", Port: dedicatedPort, Profile: "two", Username: "worker", Password: "dedicated-secret"})
		cfg.Jobs = append(cfg.Jobs, config.JobConfig{Name: "dedicated", Mode: "pinned", Endpoint: "dedicated", Profile: "two"})
	}
	for _, name := range []string{"one", "two"} {
		cfg.Nodes = append(cfg.Nodes, config.NodeConfig{Name: name, URI: "socks5://" + startTestSOCKS5Proxy(t)})
	}
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
		t.Cleanup(func() { m.Close() })
		return m
	}
	m := start()
	requestAt := func(proxyPort uint16, scheme, user, password, destination string, want bool) {
		t.Helper()
		proxy := &url.URL{Scheme: scheme, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(proxyPort))), User: url.UserPassword(user, password)}
		tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		defer tr.CloseIdleConnections()
		r, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get(destination)
		if err != nil {
			if want {
				t.Fatal(err)
			}
			return
		}
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if want {
			if err != nil || r.StatusCode != 200 || string(body) != "shared target" {
				t.Fatalf("%s status=%d error=%v", scheme, r.StatusCode, err)
			}
		} else if r.StatusCode == 200 {
			t.Fatal("rejected identity reached upstream")
		}
	}
	request := func(scheme, user, password, destination string, want bool) {
		t.Helper()
		requestAt(port, scheme, user, password, destination, want)
	}
	if !legacy {
		for _, scheme := range []string{"http", "socks5"} {
			requestAt(dedicatedPort, scheme, "worker-session-account-01", "dedicated-secret", secure.URL, true)
			requestAt(dedicatedPort, scheme, "account-01", cfg.Jobs[0].Password, target.URL, false)
			request(scheme, "worker-session-account-01", "dedicated-secret", target.URL, false)
		}
	}
	for _, scheme := range []string{"http", "socks5"} {
		for _, dest := range []string{target.URL, secure.URL} {
			request(scheme, "account-01", cfg.Jobs[0].Password, dest, true)
			request(scheme, "account-01", cfg.Jobs[1].Password, dest, true)
			request(scheme, "pool", cfg.Jobs[2].Password, dest, true)
			request(scheme, "fleet", endpoint.Password, dest, true)
			request(scheme, "fleet@two", endpoint.Password, dest, true)
			request(scheme, "account-02", cfg.Jobs[0].Password, dest, false)
			request(scheme, "account-01", "wrong-application-secret", dest, false)
			request(scheme, "invalid@two", cfg.Jobs[0].Password, dest, false)
			request(scheme, "fleet", "wrong-application-secret", dest, false)
		}
	}
	a, ok := m.jobStore.Lease("a", "account-01")
	b, bok := m.jobStore.Lease("b", "account-01")
	if !ok || !bok || a.Node != b.Node || !a.Permanent || !b.Permanent {
		t.Fatal("per-application identity/capacity lost")
	}
	access, err := m.AcquireJob("a", "account-01")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(access.ProxyURL)
	password, _ := parsed.User.Password()
	if parsed.Port() != strconv.Itoa(int(port)) || parsed.User.Username() != "account-01" || password != cfg.Jobs[0].Password {
		t.Fatal("wrong shared application URI")
	}
	// Authentication is per HTTP request, not cached on the socket.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	for _, key := range []string{cfg.Jobs[0].Password, cfg.Jobs[1].Password, "wrong-secret", ""} {
		r, _ := http.NewRequest("GET", target.URL, nil)
		want := 407
		if key != "" {
			r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("account-01:"+key)))
			if key != "wrong-secret" {
				want = 200
			}
		}
		if err := r.WriteProxy(conn); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(reader, r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("keep-alive auth: %d, want %d", resp.StatusCode, want)
		}
	}
	conn.Close()
	// A failed transactional password change restores authentication and bindings.
	rejected, revision := m.ConfigSnapshot()
	rejected.Jobs[0].Password = "rejected-application-a-secret"
	ctx, cancel := context.WithCancel(context.Background())
	m.beforeRuntimeCommit = cancel
	err = m.CommitConfig(ctx, revision, rejected, nil)
	m.beforeRuntimeCommit = nil
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rotation rollback: %v", err)
	}
	for _, scheme := range []string{"http", "socks5"} {
		request(scheme, "account-01", cfg.Jobs[0].Password, target.URL, true)
		request(scheme, "account-01", rejected.Jobs[0].Password, target.URL, false)
	}
	if after, _ := m.jobStore.Lease("a", "account-01"); after != a {
		t.Fatal("rejected password change altered binding")
	}
	// Rotation must apply atomically without changing node, revision or lifetime.
	candidate, _ := m.ConfigSnapshot()
	oldPassword := candidate.Jobs[0].Password
	candidate.Jobs[0].Password = "rotated-application-a-secret"
	if err := normalizeAndReload(t, m, candidate); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "socks5"} {
		request(scheme, "account-01", oldPassword, target.URL, false)
		request(scheme, "account-01", candidate.Jobs[0].Password, secure.URL, true)
	}
	if after, _ := m.jobStore.Lease("a", "account-01"); after != a {
		t.Fatal("rotation altered durable binding")
	}
	cfg, _ = m.ConfigSnapshot()
	m.Close()
	m = start()
	request("socks5", "account-01", cfg.Jobs[0].Password, secure.URL, true)
	if after, _ := m.jobStore.Lease("a", "account-01"); after != a {
		t.Fatal("restart altered binding")
	}
	if err := m.MonitorManager().ManualBlacklist(a.Node, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "socks5"} {
		request(scheme, "account-01", cfg.Jobs[0].Password, target.URL, false)
		request(scheme, "pool", cfg.Jobs[2].Password, target.URL, true)
	}
	if after, _ := m.jobStore.Lease("a", "account-01"); after.Node != a.Node {
		t.Fatal("failed node silently changed")
	}
	// Last application removed: old credentials still cannot fall back to legacy pool.
	candidate, _ = m.ConfigSnapshot()
	candidate.Jobs = nil
	if err := normalizeAndReload(t, m, candidate); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "socks5"} {
		request(scheme, "account-01", cfg.Jobs[0].Password, target.URL, false)
		request(scheme, "fleet", endpoint.Password, target.URL, true)
	}
}
