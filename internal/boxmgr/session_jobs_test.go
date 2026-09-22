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
	"strings"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestPinnedSessionEndpointWithoutManagementCalls(t *testing.T) {
	for _, selection := range []string{"manual", "auto"} {
		t.Run(selection, func(t *testing.T) { testPinnedSessionEndpoint(t, selection) })
	}
}

func TestPinnedHTTPPreservesRequest(t *testing.T) {
	type receivedRequest struct {
		method, uri, body, authorization, cookie, proxyAuthorization string
	}
	received := make(chan receivedRequest, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
		}
		received <- receivedRequest{r.Method, r.RequestURI, string(body), r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("Proxy-Authorization")}
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	endpoint := config.EndpointConfig{Name: "accounts", Address: "127.0.0.1", Port: findManagerTestPort(t), Profile: "fast", Username: "worker", Password: "secret"}
	cfg := &config.Config{Mode: "pool", LogLevel: "error", Profiles: []config.ProfileConfig{{Name: "fast"}}, Endpoints: []config.EndpointConfig{endpoint}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Jobs: []config.JobConfig{{Name: "accounts", Mode: "pinned", Profile: "fast", Endpoint: "accounts"}}, Nodes: []config.NodeConfig{{Name: "one", URI: "socks5://" + startTestSOCKS5Proxy(t)}}}
	cfg.SetFilePath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatal(err)
	}
	cfg.SubscriptionRefresh.MinAvailableNodes = 0
	m := New(cfg, monitor.Config{ProbeTarget: target.URL, ProbeMode: "manual"})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	proxy := &url.URL{Scheme: "http", Host: net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))), User: url.UserPassword("worker-session-account", endpoint.Password)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	for _, tc := range []struct{ name, method, uri, body string }{
		{"semicolon", http.MethodGet, "/catalog%2Fspecial?z=last&filter=a;b&a=hello%20world&tag=one&tag=two", ""},
		{"opaque-query", http.MethodPost, "/submit?token=abc%ZZ&next=%2fpath&tag=two&tag=one", "name=hello+world&value=a%3Bb"},
		{"empty-query", http.MethodGet, "/catalog?", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, target.URL+tc.uri, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer target-token")
			request.Header.Set("Cookie", "account=same-session")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("proxy request: status=%d err=%v", response.StatusCode, err)
			}
			select {
			case got := <-received:
				want := receivedRequest{tc.method, tc.uri, tc.body, "Bearer target-token", "account=same-session", ""}
				if got != want {
					t.Errorf("forwarded request changed:\n got %+v\nwant %+v", got, want)
				}
			case <-time.After(time.Second):
				t.Fatal("target did not receive the request")
			}
		})
	}
}

func TestUncommittedRuntimeCannotPersistNewJobSessions(t *testing.T) {
	for _, name := range []string{"in-place", "full-handoff", "first-runtime"} {
		t.Run(name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
			defer target.Close()
			endpoint := config.EndpointConfig{Name: "accounts", Address: "127.0.0.1", Port: findManagerTestPort(t), Profile: "fast", Username: "worker", Password: "secret"}
			cfg := &config.Config{Mode: "pool", LogLevel: "error", Profiles: []config.ProfileConfig{{Name: "fast"}}, Endpoints: []config.EndpointConfig{endpoint}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Jobs: []config.JobConfig{{Name: "accounts", Mode: "pinned", Profile: "fast", Endpoint: "accounts"}}, Nodes: []config.NodeConfig{{Name: "one", URI: "socks5://" + startTestSOCKS5Proxy(t)}}}
			nodes := cfg.Nodes
			if name == "first-runtime" {
				cfg.Nodes = nil
				enabled := true
				cfg.Management.Enabled = &enabled
				cfg.Management.Listen = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(findManagerTestPort(t))))
			}
			cfg.SetFilePath(filepath.Join(t.TempDir(), "config.yaml"))
			if err := cfg.NormalizeWithPortMap(nil); err != nil {
				t.Fatal(err)
			}
			cfg.SubscriptionRefresh.MinAvailableNodes = 0
			request := func(session string) (int, error) {
				proxy := &url.URL{Scheme: "http", Host: net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))), User: url.UserPassword("worker-session-"+session, endpoint.Password)}
				transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
				defer transport.CloseIdleConnections()
				response, err := (&http.Client{Transport: transport, Timeout: 2 * time.Second}).Get(target.URL)
				if err != nil {
					return 0, err
				}
				defer response.Body.Close()
				_, err = io.Copy(io.Discard, response.Body)
				return response.StatusCode, err
			}
			m := New(cfg, monitor.Config{Enabled: name == "first-runtime", Listen: cfg.Management.Listen, ProbeTarget: target.URL, ProbeMode: "manual"})
			startupStatus := 0
			m.beforeStartPublish = func() { startupStatus, _ = request("startup-pending") }
			if err := m.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.beforeStartPublish = nil
			if name != "first-runtime" {
				if startupStatus != http.StatusBadGateway {
					t.Errorf("uncommitted startup returned %d, want 502", startupStatus)
				}
				if status, err := request("existing"); err != nil || status != http.StatusOK {
					t.Fatalf("initial session: status=%d err=%v", status, err)
				}
			}
			original, _ := m.jobStore.Lease("accounts", "existing")
			candidate, revision := m.ConfigSnapshot()
			candidate.Nodes = nodes
			if name == "in-place" {
				candidate.Nodes = append(append([]config.NodeConfig(nil), nodes...), config.NodeConfig{Name: "candidate-only", URI: "socks5://" + startTestSOCKS5Proxy(t)})
			} else {
				candidate.Jobs[0].Concurrency++
			}
			if name == "full-handoff" {
				candidate.LogLevel = "warn"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			uncommittedStatus := 0
			existingStatus := 0
			m.beforeRuntimeCommit = func() {
				uncommittedStatus, _ = request("uncommitted")
				if name == "in-place" {
					existingStatus, _ = request("existing")
				}
				cancel()
			}
			err := m.CommitConfig(ctx, revision, candidate, nil)
			m.beforeRuntimeCommit = nil
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("reload should roll back: %v", err)
			}
			if uncommittedStatus != http.StatusBadGateway {
				t.Errorf("uncommitted runtime returned %d, want 502", uncommittedStatus)
			}
			if name == "in-place" && existingStatus != http.StatusOK {
				t.Errorf("node-only cutover blocked a valid existing session: %d", existingStatus)
			}
			if lease, exists := m.jobStore.Lease("accounts", "uncommitted"); exists {
				t.Errorf("rolled-back runtime left a durable binding: %+v", lease)
			}
			if restored, _ := m.jobStore.Lease("accounts", "existing"); restored != original {
				t.Error("rollback changed an existing assignment")
			}
			reopened, err := jobs.Open(cfg.ResolveManagementPath("", "job-sessions.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, session := range []string{"startup-pending", "uncommitted"} {
				if _, exists := reopened.Lease("accounts", session); exists {
					t.Errorf("uncommitted session %s survived on disk", session)
				}
			}
			if name != "first-runtime" {
				if status, err := request("after-rollback"); err != nil || status != http.StatusOK {
					t.Fatalf("rollback did not restore acquisitions: status=%d err=%v", status, err)
				}
			}
			if err := m.CommitConfig(context.Background(), revision, candidate, nil); err != nil {
				t.Fatal(err)
			}
			if status, err := request("after-commit"); err != nil || status != http.StatusOK {
				t.Fatalf("commit did not restore acquisitions: status=%d err=%v", status, err)
			}
		})
	}
}

func testPinnedSessionEndpoint(t *testing.T, selection string) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached target")
		}
		_, _ = io.WriteString(w, "session target")
	})
	target := httptest.NewServer(handler)
	defer target.Close()
	secureTarget := httptest.NewTLSServer(handler)
	defer secureTarget.Close()
	endpoint := config.EndpointConfig{Name: "accounts", Address: "127.0.0.1", Port: findManagerTestPort(t), Profile: "fast", Username: "worker", Password: "p@ss?/秘密"}
	cfg := &config.Config{Mode: "pool", LogLevel: "error", Profiles: []config.ProfileConfig{{Name: "fast"}}, Endpoints: []config.EndpointConfig{endpoint}, Pool: config.PoolConfig{Mode: "sequential", RetryAttempts: 3}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Jobs: []config.JobConfig{{Name: "accounts", Mode: "pinned", Selection: "auto", Profile: "fast", Endpoint: "accounts", TargetURL: target.URL, Size: 2, Timeout: "2s"}}}
	if selection == "manual" {
		cfg.Jobs[0].Selection, cfg.Jobs[0].TargetURL, cfg.Jobs[0].Size = "", "", 0
		cfg.Profiles[0].NameRegex = "^(one|two)$"
	}
	for _, name := range []string{"one", "two", "three"} {
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
		t.Cleanup(func() { _ = m.Close() })
		if selection == "manual" {
			s := m.ListJobs()[0]
			if s.Selection != "manual" || len(s.Selected) != 2 || s.Measured != 0 || s.Refreshing {
				t.Fatalf("manual job needed benchmark warm-up: %+v", s)
			}
			return m
		}
		for _, node := range m.MonitorManager().Snapshot() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := m.MonitorManager().Probe(ctx, node.Tag)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if len(m.ListJobs()[0].Selected) == 2 {
				return m
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("session job did not warm up")
		return nil
	}
	request := func(scheme, username, password, destination string, wantSuccess bool) {
		t.Helper()
		proxy := &url.URL{Scheme: scheme, Host: net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))), User: url.UserPassword(username, password)}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		response, err := client.Get(destination)
		if err != nil {
			if wantSuccess {
				t.Fatalf("%s request failed: %v", scheme, err)
			}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if wantSuccess {
			if err != nil || response.StatusCode != 200 || string(body) != "session target" {
				t.Fatalf("unexpected %s response: %d %q %v", scheme, response.StatusCode, body, err)
			}
		} else if response.StatusCode == 200 {
			t.Fatal("unavailable/unauthenticated session forwarded")
		}
	}
	m := start()
	for _, scheme := range []string{"http", "socks5"} {
		request(scheme, "worker-session-unauthorized", "wrong", target.URL, false)
		request(scheme, "worker", endpoint.Password, target.URL, false)
		request(scheme, "worker-session-", endpoint.Password, target.URL, false)
	}
	if _, ok := m.jobStore.Lease("accounts", "unauthorized"); ok {
		t.Fatal("wrong credentials allocated a lease")
	}
	for _, session := range []string{"account-a", "account-b"} {
		for _, scheme := range []string{"http", "socks5"} {
			for _, destination := range []string{target.URL, secureTarget.URL} {
				request(scheme, "worker-session-"+session, endpoint.Password, destination, true)
			}
		}
	}
	a, exists := m.jobStore.Lease("accounts", "account-a")
	b, _ := m.jobStore.Lease("accounts", "account-b")
	if !exists || a.Node == b.Node {
		t.Fatalf("first connections did not assign independent sessions: %+v %+v", a, b)
	}
	// HTTP keep-alive must authenticate each request independently, including
	// when a client changes session or omits credentials on a reused connection.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	for _, session := range []string{"account-a", "account-b", ""} {
		r, _ := http.NewRequest(http.MethodGet, target.URL, nil)
		want := http.StatusProxyAuthRequired
		if session != "" {
			r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("worker-session-"+session+":"+endpoint.Password)))
			want = http.StatusOK
		}
		if err := r.WriteProxy(conn); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("reused HTTP connection returned %d, want %d", response.StatusCode, want)
		}
	}
	_ = conn.Close()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = start()
	request("http", "worker-session-account-a", endpoint.Password, secureTarget.URL, true)
	if restored, _ := m.jobStore.Lease("accounts", "account-a"); restored.Node != a.Node {
		t.Fatal("restart changed node")
	}
	// A health failure must block every new protocol connection while
	// preserving the old node, even with global retries enabled and healthy spares.
	if selection == "auto" {
		m.jobStore.Record(a.Policy, jobs.Measurement{Node: a.Node, Success: false})
	} else if err := m.MonitorManager().ManualBlacklist(a.Node, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, scheme := range []string{"http", "socks5"} {
		for _, destination := range []string{target.URL, secureTarget.URL} {
			request(scheme, "worker-session-account-a", endpoint.Password, destination, false)
		}
	}
	if paused, _ := m.jobStore.Lease("accounts", "account-a"); paused.Node != a.Node {
		t.Fatal("failure changed assignment")
	}
	candidate, _ := m.ConfigSnapshot()
	kept := candidate.Nodes[:0]
	for _, node := range candidate.Nodes {
		if "node-"+node.NodeKey() != a.Node {
			kept = append(kept, node)
		}
	}
	candidate.Nodes = kept
	if err := normalizeAndReload(t, m, candidate); err != nil {
		t.Fatal(err)
	}
	request("http", "worker-session-account-a", endpoint.Password, target.URL, false)
	request("http", "worker-session-account-b", endpoint.Password, secureTarget.URL, true)
	if err := m.ReleaseJob("accounts", "account-a"); err != nil {
		t.Fatal(err)
	}
	request("http", "worker-session-account-a", endpoint.Password, secureTarget.URL, true)
	if recovered, _ := m.jobStore.Lease("accounts", "account-a"); recovered.Node == a.Node {
		t.Fatal("explicit recovery did not assign a remaining node")
	}
}
