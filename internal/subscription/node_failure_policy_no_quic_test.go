//go:build !with_quic

package subscription

import (
	"testing"

	"github.com/silencoo/proxyfleet/internal/config"
)

func TestFilterNodesForBuildCapabilitiesSkipsOnlyUnsupportedNodes(t *testing.T) {
	nodes := []config.NodeConfig{
		{Name: "ordinary", URI: "socks5://example.com:1080"},
		{Name: "needs-quic", URI: "tuic://id@example.com:443"},
	}
	accepted, failures, err := filterSubscriptionNodesForBuild(nodes, false, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0].Name != "ordinary" {
		t.Fatalf("accepted nodes = %+v, want only ordinary node", accepted)
	}
	if len(failures) != 1 || failures[0].Name != "needs-quic" || failures[0].Error == "" {
		t.Fatalf("failures = %+v, want one safe QUIC capability failure", failures)
	}
	if failures[0].NodeKey == "" || failures[0].Tag != "node-"+failures[0].NodeKey {
		t.Fatalf("failure identity is not stable: %+v", failures[0])
	}

	if _, strictFailures, strictErr := filterSubscriptionNodesForBuild(nodes, false, "strict"); strictErr == nil || len(strictFailures) != 1 {
		t.Fatalf("strict policy should reject the batch: err=%v failures=%+v", strictErr, strictFailures)
	}
}
