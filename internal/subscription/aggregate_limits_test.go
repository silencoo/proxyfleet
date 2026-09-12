package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func aggregateTestNodes(prefix string, count int, uriBytes int) []config.NodeConfig {
	nodes := make([]config.NodeConfig, count)
	for i := range nodes {
		uri := fmt.Sprintf("socks5://%s-%d.example.test:1080", prefix, i)
		if uriBytes > len(uri) {
			uri += "#" + strings.Repeat("x", uriBytes-len(uri)-1)
		}
		nodes[i] = config.NodeConfig{URI: uri}
	}
	return nodes
}

func TestRefreshAggregateLimitsIncludeAllFallbackPaths(t *testing.T) {
	for _, mode := range []string{"fetch-limit", "source-cache", "selected-cache", "byte-cache", "aggregate-file", "within-limit"} {
		t.Run(mode, func(t *testing.T) {
			count := 20000
			if mode == "byte-cache" || mode == "aggregate-file" {
				count = 1
			}
			if mode == "within-limit" {
				count = 10
			}
			fresh := aggregateTestNodes("fresh", count, 0)
			var body strings.Builder
			for _, node := range fresh {
				fmt.Fprintln(&body, node.URI)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "aggregate-file" || (r.URL.Path == "/c" && mode != "fetch-limit") {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = io.WriteString(w, body.String())
			}))
			defer server.Close()
			cfg := &config.Config{}
			if err := cfg.SetSubscriptionSources([]config.SubscriptionSourceConfig{{Name: "a", URL: server.URL + "/a"}, {Name: "b", URL: server.URL + "/b"}, {Name: "c", URL: server.URL + "/c"}}); err != nil {
				t.Fatal(err)
			}
			cfg.SubscriptionRefresh.AllowPrivateNetworks = true
			cfg.SubscriptionRefresh.Timeout = 5 * time.Second
			cache := map[string][]config.NodeConfig{cfg.SubscriptionSources[2].Key(): aggregateTestNodes("cached-c", count, 0)}
			var selected map[string]struct{}
			if mode == "selected-cache" || mode == "byte-cache" {
				selected = map[string]struct{}{cfg.SubscriptionSources[0].Key(): {}}
				cache[cfg.SubscriptionSources[1].Key()] = aggregateTestNodes("cached-b", count, 0)
				if mode == "byte-cache" {
					for key := range cache {
						cache[key] = aggregateTestNodes(key[:8], 1600, config.MaxSubscriptionNodeURIBytes)
					}
				}
			}
			path := filepath.Join(t.TempDir(), "nodes.txt")
			original := "socks5://unchanged.example.test:1080\n"
			if mode == "aggregate-file" {
				original = strings.Repeat(original, config.MaxSubscriptionNodesTotal+1)
				cache = nil
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			manager := &Manager{sourceCache: cache, logger: defaultLogger{}}
			beforeCache := make(map[string][]config.NodeConfig)
			for key, nodes := range cache {
				beforeCache[key] = append([]config.NodeConfig(nil), nodes...)
			}
			plan, err := manager.fetchAllSubscriptions(context.Background(), cfg, path, mode == "aggregate-file", selected)
			if mode == "within-limit" {
				if err != nil || len(plan.nodes) != 20 {
					t.Fatalf("valid fallback rejected: nodes=%d err=%v", len(plan.nodes), err)
				}
			} else if !errors.Is(err, config.ErrSubscriptionAggregateLimit) {
				t.Fatalf("oversized %s plan accepted: nodes=%d err=%v", mode, len(plan.nodes), err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatal("rejected refresh modified restart cache")
			}
			for key, nodes := range beforeCache {
				if !reflect.DeepEqual(manager.sourceCache[key], nodes) {
					t.Fatal("source cache changed")
				}
			}
		})
	}
}

func TestCommitRejectsOversizedCandidateBeforeRuntimeMutation(t *testing.T) {
	cfg := &config.Config{Nodes: []config.NodeConfig{{URI: "socks5://unchanged.example.test:1080"}}}
	fake := newFakeBoxManager(cfg)
	manager := New(cfg, fake)
	defer manager.Stop()
	_, _, err := manager.commitRefreshPlan(context.Background(), cfg, make([]config.NodeConfig, config.MaxSubscriptionNodesTotal+1), 0, 0, nil)
	if !errors.Is(err, config.ErrSubscriptionAggregateLimit) {
		t.Fatalf("oversized commit accepted: %v", err)
	}
	snapshot, revision := fake.ConfigSnapshot()
	if revision != 1 || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].URI != cfg.Nodes[0].URI {
		t.Fatal("rejected commit modified active configuration")
	}
}
