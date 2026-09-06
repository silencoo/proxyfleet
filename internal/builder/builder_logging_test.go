package builder

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/silencoo/proxyfleet/internal/config"
	poolout "github.com/silencoo/proxyfleet/internal/outbound/pool"
)

func TestStartupNodeInventoryIsBoundedUnlessVerbose(t *testing.T) {
	for _, mode := range []string{"pool", "multi-port", "hybrid"} {
		for _, level := range []string{"", "info", "DEBUG", "trace"} {
			t.Run(mode+"/"+level, func(t *testing.T) {
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				t.Cleanup(func() { log.SetOutput(previous) })
				cfg := &config.Config{Mode: mode, LogLevel: level}
				metadata := make(map[string]poolout.MemberMeta)
				for index := 0; index < 421; index++ {
					name := fmt.Sprintf("test-node-%03d", index)
					metadata[name] = poolout.MemberMeta{Name: name}
					cfg.Nodes = append(cfg.Nodes, config.NodeConfig{Name: name, Port: uint16(24000 + index)})
				}
				printProxyLinks(cfg, metadata)
				logged := output.String()
				want := startupNodeLogLimit
				if level == "DEBUG" || level == "trace" {
					want = 421
				} else if !strings.Contains(logged, "401 more") {
					t.Fatal("summary omitted the remaining node count")
				}
				if mode == "hybrid" {
					want *= 2
				}
				if got := strings.Count(logged, "test-node-"); got != want {
					t.Fatalf("printed %d nodes, want %d", got, want)
				}
				if mode != "multi-port" && strings.Index(logged, "test-node-000") > strings.Index(logged, "test-node-001") {
					t.Fatal("pool inventory is not in a deterministic order")
				}
			})
		}
	}
}

func TestPrintProxyLinksOmitsCredentials(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	cfg := &config.Config{
		Mode: "hybrid",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     2323,
			Username: "pool-test-user",
			Password: "pool-test-password",
		},
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			Username: "fallback-test-user",
			Password: "fallback-test-password",
		},
		Nodes: []config.NodeConfig{
			{
				Name:     "first-node",
				Port:     24001,
				Username: "node-test-user",
				Password: "node-test-password",
			},
			{Name: "fallback-node", Port: 24002},
		},
	}
	metadata := map[string]poolout.MemberMeta{
		"node-one": {Name: "first-node"},
	}

	printProxyLinks(cfg, metadata)
	logged := output.String()
	for _, credential := range []string{
		"pool-test-user",
		"pool-test-password",
		"node-test-user",
		"node-test-password",
		"fallback-test-user",
		"fallback-test-password",
	} {
		if strings.Contains(logged, credential) {
			t.Fatalf("startup proxy links exposed a credential")
		}
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:2323",
		"socks5://127.0.0.1:2323",
		"http://127.0.0.1:24001",
		"socks5://127.0.0.1:24002",
	} {
		if !strings.Contains(logged, endpoint) {
			t.Fatalf("startup proxy links missing endpoint %q", endpoint)
		}
	}
	if count := strings.Count(logged, "Authentication: configured (credentials omitted)"); count != 3 {
		t.Fatalf("configured authentication status count = %d, want 3", count)
	}
}
