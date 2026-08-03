package subscription

import (
	"errors"
	"testing"

	"github.com/silencoo/proxyfleet/internal/config"
)

type testCandidateNodeError struct {
	tag string
}

func (e testCandidateNodeError) Error() string            { return "candidate node construction failed" }
func (e testCandidateNodeError) CandidateNodeTag() string { return e.tag }

func TestRequiredBuildCapability(t *testing.T) {
	tests := map[string]string{
		"tuic://id@example.com:443":                                      "quic",
		"hysteria2://id@example.com:443":                                 "quic",
		"wireguard://secret@example.com:51820":                           "wireguard",
		"vless://id@example.com:443?type=grpc&security=tls":              "grpc",
		"vless://id@example.com:443?transport=grpc&security=tls":         "grpc",
		"vless://id@example.com:443?type=ws&security=tls#ordinary-vless": "",
	}
	for rawURI, want := range tests {
		if got := requiredBuildCapability(rawURI); got != want {
			t.Errorf("requiredBuildCapability(%q) = %q, want %q", rawURI, got, want)
		}

	}
}

func TestFilterSubscriptionNodesForBuildIsolatesMalformedURI(t *testing.T) {
	nodes := []config.NodeConfig{
		{Name: "ordinary", URI: "socks5://example.com:1080"},
		{Name: "malformed", URI: "socks5://user:secret@example.com%zz:1080"},
	}
	accepted, failures, err := filterSubscriptionNodesForBuild(nodes, false, "skip")
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 1 || accepted[0].Name != "ordinary" || len(failures) != 1 {
		t.Fatalf("unexpected validation result: accepted=%+v failures=%+v", accepted, failures)
	}
	if failures[0].Name != "malformed" || failures[0].Error == "" {
		t.Fatalf("missing safe malformed-node diagnostic: %+v", failures[0])
	}
	if failures[0].Error == nodes[1].URI || failures[0].Error == "secret" {
		t.Fatalf("validation failure leaked URI credentials: %q", failures[0].Error)
	}
}

func TestSubscriptionNodeFailureMapsStableTagWithoutURI(t *testing.T) {
	nodes := []config.NodeConfig{{Name: "candidate", URI: "socks5://user:secret@example.com:1080"}}
	tag := "node-" + nodes[0].NodeKey()
	err := errors.New("outer: " + testCandidateNodeError{tag: tag}.Error())
	err = testWrappedCandidateError{cause: err, tagged: testCandidateNodeError{tag: tag}}
	index, failure, ok := subscriptionNodeFailure(err, nodes)
	if !ok || index != 0 || failure.Tag != tag || failure.NodeKey != nodes[0].NodeKey() {
		t.Fatalf("unexpected mapped failure: index=%d ok=%t failure=%+v", index, ok, failure)
	}
	if failure.Error == "" || failure.Error == nodes[0].URI {
		t.Fatalf("failure message missing or leaked URI: %q", failure.Error)
	}
}

type testWrappedCandidateError struct {
	cause  error
	tagged testCandidateNodeError
}

func (e testWrappedCandidateError) Error() string            { return e.cause.Error() }
func (e testWrappedCandidateError) Unwrap() error            { return e.tagged }
func (e testWrappedCandidateError) CandidateNodeTag() string { return e.tagged.tag }
