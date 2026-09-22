package boxmgr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestJobsHybridTrafficRestartAndNodeRemoval(t *testing.T) {
	for _, selection := range []string{"manual", "auto"} {
		t.Run(selection, func(t *testing.T) { testJobsHybridTraffic(t, selection) })
	}
}

func TestJobsRemainVisibleWithoutProxyRuntime(t *testing.T) {
	m := &Manager{cfg: &config.Config{Jobs: []config.JobConfig{{Name: "manual", Mode: "pinned"}, {Name: "auto", Mode: "pooled", Selection: "auto", TargetURL: "https://example.com", Size: 5}}}}
	statuses := m.ListJobs()
	if len(statuses) != 2 || statuses[0].Selection != "manual" || statuses[0].Selected == nil || len(statuses[0].Selected) != 0 || statuses[1].Size != 5 {
		t.Fatalf("configured jobs disappeared without an active pool: %+v", statuses)
	}
}

func testJobsHybridTraffic(t *testing.T, selection string) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "representative page") }))
	defer target.Close()
	cfg := &config.Config{Mode: "hybrid", LogLevel: "error", Profiles: []config.ProfileConfig{{Name: "fast"}}, Endpoints: []config.EndpointConfig{{Name: "catalog", Address: "127.0.0.1", Port: findManagerTestPort(t), Profile: "fast"}}, MultiPort: config.MultiPortConfig{Address: "127.0.0.1", BasePort: 24000}, Pool: config.PoolConfig{Mode: "sequential", Sticky: config.StickyConfig{Enabled: true}}, Management: config.ManagementConfig{ProbeTarget: target.URL}, Jobs: []config.JobConfig{
		{Name: "catalog", Mode: "pooled", Selection: "auto", Profile: "fast", Endpoint: "catalog", TargetURL: target.URL, Size: 2, Timeout: "2s", BodyContains: "representative"},
		{Name: "accounts", Mode: "pinned", Selection: "auto", Profile: "fast", TargetURL: target.URL, Size: 2, Timeout: "2s", BodyContains: "representative"},
	}}
	wantSelected := 2
	if selection == "manual" {
		wantSelected = 3
		for i := range cfg.Jobs {
			cfg.Jobs[i].Selection, cfg.Jobs[i].TargetURL = "manual", ""
		}
	}
	for _, name := range []string{"one", "two", "three"} {
		cfg.Nodes = append(cfg.Nodes, config.NodeConfig{Name: name, URI: "socks5://" + startTestSOCKS5Proxy(t), Port: findManagerTestPort(t)})
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
			statuses := m.ListJobs()
			if len(statuses) == 2 && len(statuses[0].Selected) == wantSelected && len(statuses[1].Selected) == wantSelected {
				return m
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("job benchmark did not warm up: %+v", m.ListJobs())
		return nil
	}
	m := start()
	pooled, err := m.AcquireJob("catalog", "")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := m.AcquireJob("accounts", "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Mode != "pinned" || pinned.State != "ready" || pinned.ProxyURL == pooled.ProxyURL {
		t.Fatalf("wrong access: %+v %+v", pooled, pinned)
	}
	for _, access := range []jobs.Access{pooled, pinned} {
		proxy, _ := url.Parse(access.ProxyURL)
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		for range 2 {
			response, err := client.Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "representative page" {
				t.Fatal("proxy did not forward scrape")
			}
		}
		transport.CloseIdleConnections()
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = start()
	restored, err := m.AcquireJob("accounts", "account-a")
	if err != nil || restored.Node != pinned.Node || restored.ProxyURL != pinned.ProxyURL {
		t.Fatalf("restart changed assignment: %+v %v", restored, err)
	}
	candidate, _ := m.ConfigSnapshot()
	kept := candidate.Nodes[:0]
	for _, node := range candidate.Nodes {
		if "node-"+node.NodeKey() != pinned.Node {
			kept = append(kept, node)
		}
	}
	candidate.Nodes = kept
	if err := normalizeAndReload(t, m, candidate); err != nil {
		t.Fatal(err)
	}
	paused, err := m.AcquireJob("accounts", "account-a")
	if err != nil || paused.Node != pinned.Node || paused.State != "paused" || paused.ProxyURL != "" {
		t.Fatalf("removed node silently rebound: %+v %v", paused, err)
	}
	if err := m.ReleaseJob("accounts", "account-a"); err != nil {
		t.Fatal(err)
	}
	recovered, err := m.AcquireJob("accounts", "account-a")
	if err != nil || recovered.Node == pinned.Node || recovered.State != "ready" {
		t.Fatalf("explicit recovery failed: %+v %v", recovered, err)
	}
}
