package boxmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

// Exercise real HTTP requests through the mixed listener, pool and local
// SOCKS5 upstreams. No production configuration or external service is used.
// A refresh removes one upstream while every client has a request in flight.
func TestPoolConcurrentScrapeTrafficAcrossRefresh(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		t.Run(fmt.Sprintf("keepalive=%v", keepAlive), func(t *testing.T) {
			const concurrency, perWorker = 32, 16
			const totalRequests = concurrency * perWorker
			payload := strings.Repeat("scrape-body-", 256)
			arrived := make(chan struct{}, concurrency)
			releaseRequests := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseRequests) }) }
			var requests atomic.Int32
			healthTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer healthTarget.Close()
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				select {
				case arrived <- struct{}{}:
				default:
				}
				select {
				case <-releaseRequests:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, r.URL.Query().Get("id")+":"+payload)
			}))
			defer target.Close()
			defer release()
			port := findManagerTestPort(t)
			cfg := &config.Config{
				Mode:                "pool",
				LogLevel:            "error",
				Endpoints:           []config.EndpointConfig{{Name: "scrape-test", Address: "127.0.0.1", Port: port}},
				Pool:                config.PoolConfig{Mode: "sequential"},
				Management:          config.ManagementConfig{ProbeTarget: healthTarget.URL},
				SubscriptionRefresh: config.SubscriptionRefreshConfig{MinAvailableNodes: 1, HealthCheckTimeout: 2 * time.Second},
			}
			for i := 0; i < 4; i++ {
				cfg.Nodes = append(cfg.Nodes, config.NodeConfig{Name: fmt.Sprintf("local-%d", i), URI: "socks5://" + startTestSOCKS5Proxy(t)})
			}
			cfg.SetFilePath(filepath.Join(t.TempDir(), "config.yaml"))
			if err := cfg.NormalizeWithPortMap(nil); err != nil {
				t.Fatal(err)
			}
			// Prime health explicitly below; do not wait for automatic checks in
			// manual mode. Candidate validation is re-enabled for the refresh.
			cfg.SubscriptionRefresh.MinAvailableNodes = 0
			manager := New(cfg, monitor.Config{ProbeTarget: healthTarget.URL, ProbeMode: "manual"})
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			for _, node := range manager.MonitorManager().Snapshot() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := manager.MonitorManager().Probe(ctx, node.Tag)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
			}
			proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(target.Certificate())
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: !keepAlive, MaxIdleConns: concurrency, MaxIdleConnsPerHost: concurrency, TLSClientConfig: &tls.Config{RootCAs: roots}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			errors := make(chan error, totalRequests)
			latencies := make(chan time.Duration, totalRequests)
			var workers sync.WaitGroup
			started := time.Now()
			for worker := 0; worker < concurrency; worker++ {
				workers.Add(1)
				go func(worker int) {
					defer workers.Done()
					for request := 0; request < perWorker; request++ {
						id := fmt.Sprintf("%d-%d", worker, request)
						before := time.Now()
						response, err := client.Get(target.URL + "/scrape?id=" + id)
						if err != nil {
							errors <- err
							continue
						}
						body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+128)))
						_ = response.Body.Close()
						if readErr != nil || response.StatusCode != http.StatusOK || string(body) != id+":"+payload {
							errors <- fmt.Errorf("request %s: status=%d body length=%d read error=%v", id, response.StatusCode, len(body), readErr)
							continue
						}
						latencies <- time.Since(before)
					}
				}(worker)
			}
			defer workers.Wait()
			defer release()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for i := 0; i < concurrency; i++ {
				select {
				case <-arrived:
				case <-deadline.C:
					t.Fatal("concurrent requests did not reach the target before refresh")
				}
			}
			candidate, _ := manager.ConfigSnapshot()
			candidate.SubscriptionRefresh.MinAvailableNodes = 1
			candidate.Nodes = append(candidate.Nodes[1:], config.NodeConfig{Name: "added-during-scrape", URI: "socks5://" + startTestSOCKS5Proxy(t)})
			if err := normalizeAndReload(t, manager, candidate); err != nil {
				t.Fatalf("refresh under active traffic: %v", err)
			}
			for _, node := range manager.MonitorManager().Snapshot() {
				if !node.InitialCheckDone || !node.Available {
					t.Fatalf("refresh lost healthy local upstream: %+v", node)
				}
			}
			release()
			workers.Wait()
			elapsed := time.Since(started)
			close(errors)
			for err := range errors {
				t.Error(err)
			}
			close(latencies)
			observed := make([]time.Duration, 0, totalRequests)
			for latency := range latencies {
				observed = append(observed, latency)
			}
			if len(observed) != totalRequests || requests.Load() != totalRequests {
				t.Fatalf("completed=%d target requests=%d, want %d", len(observed), requests.Load(), totalRequests)
			}
			sort.Slice(observed, func(i, j int) bool { return observed[i] < observed[j] })
			transport.CloseIdleConnections()
			eventuallyBoxManager(t, 3*time.Second, func() bool {
				for _, node := range manager.MonitorManager().Snapshot() {
					if node.ActiveConnections != 0 {
						return false
					}
				}
				return true
			}, "active connection counters did not drain")
			var passiveSuccesses int64
			for _, node := range manager.MonitorManager().Snapshot() {
				passiveSuccesses += node.SuccessCount
				if node.FailureCount != 0 {
					t.Fatalf("successful local workload recorded passive failures: %+v", node)
				}
			}
			if passiveSuccesses == 0 {
				t.Fatal("real HTTPS responses never confirmed passive transport health")
			}
			t.Logf("local workload: %d/%d successful, concurrency=%d, elapsed=%v, %.0f req/s, p50=%v, p95=%v (includes refresh)", len(observed), totalRequests, concurrency, elapsed, float64(totalRequests)/elapsed.Seconds(), observed[len(observed)/2], observed[len(observed)*95/100])
		})
	}
}
