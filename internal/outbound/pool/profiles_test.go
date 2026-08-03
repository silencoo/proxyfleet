package pool

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"

	"github.com/sagernet/sing-box/adapter"
)

func TestCompileProfilesPrecomputesStaticMemberView(t *testing.T) {
	profiles, err := compileProfiles([]ProfileOptions{{
		Name: "hk-fast", Regions: []string{"hk"}, Protocols: []string{"vless"},
		Sources: []string{"subscription"}, NameRegex: "(?i)residential",
	}}, map[string]MemberMeta{
		"matching":     {Name: "HK Residential 01", Region: "hk", Protocol: "vless", Source: "subscription"},
		"wrong-region": {Name: "JP Premium", Region: "jp", Protocol: "vless", Source: "subscription"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles["hk-fast"]
	if !profileAllowsMember(profile, &memberState{tag: "matching"}) {
		t.Fatal("matching member was excluded")
	}
	if profileAllowsMember(profile, &memberState{tag: "wrong-region"}) {
		t.Fatal("non-matching member was included")
	}
}

func TestCompileProfilesUsesComposableTagRules(t *testing.T) {
	profiles, err := compileProfiles([]ProfileOptions{{
		Name: "premium", TagRules: config.ProfileTagRules{
			Any: []string{`HK|JP`}, Must: []string{`Premium`}, MustNot: []string{`Expired`},
		},
	}}, map[string]MemberMeta{
		"hk": {Name: "HK Premium 01"}, "shared": {Name: "HK Shared"},
		"expired": {Name: "JP Premium Expired"}, "us": {Name: "US Premium"},
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := profiles["premium"].allowed
	if len(allowed) != 1 {
		t.Fatalf("allowed=%v", allowed)
	}
	if _, ok := allowed["hk"]; !ok {
		t.Fatal("composable rule match was not precomputed")
	}
}

func TestCompileProfilesUsesNameRegionFallback(t *testing.T) {
	profiles, err := compileProfiles([]ProfileOptions{{Name: "hk", Regions: []string{"hk"}}}, map[string]MemberMeta{
		"node-a": {Name: "🇭🇰 香港 Premium", Region: "other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles["hk"].allowed["node-a"]; !ok {
		t.Fatal("name-based HK fallback was not included in the profile")
	}
}

func TestProfileFromContextUsesAuthenticatedUsernameSuffix(t *testing.T) {
	profile := &compiledProfile{name: "hk-fast"}
	pool := &poolOutbound{profiles: map[string]*compiledProfile{"hk-fast": profile}}
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{User: "fleet@HK-FAST"})
	if got := pool.profileFromContext(ctx); got != profile {
		t.Fatalf("profileFromContext() = %p, want %p", got, profile)
	}
	base := adapter.WithContext(context.Background(), &adapter.InboundContext{User: "fleet"})
	if got := pool.profileFromContext(base); got != nil {
		t.Fatalf("base username unexpectedly selected profile %#v", got)
	}
}

func TestProfileFromContextUsesEndpointBindingBeforeUsername(t *testing.T) {
	profile := &compiledProfile{name: "hk-fast"}
	pool := &poolOutbound{
		profiles: map[string]*compiledProfile{"hk-fast": profile},
		options:  Options{EndpointProfiles: map[string]string{"endpoint-hk": "hk-fast"}},
	}
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{Inbound: "endpoint-hk", User: "fleet"})
	if got := pool.profileFromContext(ctx); got != profile {
		t.Fatalf("profileFromContext() = %p, want endpoint-bound profile %p", got, profile)
	}
}

func TestTargetAwareLatencyOverridesGlobalLatency(t *testing.T) {
	resetHealthPersistenceForTest()
	t.Cleanup(resetHealthPersistenceForTest)
	manager, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	firstEntry := manager.Register(monitor.NodeInfo{Tag: "first"})
	secondEntry := manager.Register(monitor.NodeInfo{Tag: "second"})
	firstEntry.RecordSuccessWithLatency(20 * time.Millisecond)
	secondEntry.RecordSuccessWithLatency(200 * time.Millisecond)
	recordDomainLatency("first", "api.example.com", 300*time.Millisecond)
	recordDomainLatency("second", "api.example.com", 30*time.Millisecond)

	first := &memberState{tag: "first", entry: firstEntry, shared: acquireSharedState("first")}
	second := &memberState{tag: "second", entry: secondEntry, shared: acquireSharedState("second")}
	pool := &poolOutbound{
		options: normalizeOptions(Options{LatencySampleSize: 2, LatencyTolerance: time.Millisecond}),
		rng:     rand.New(rand.NewSource(1)),
	}
	candidates := []*memberState{first, second}
	if got := pool.selectLatencyCandidate(candidates, func(*memberState) bool { return true }); got != first {
		t.Fatalf("global latency selected %v, want first", got)
	}
	if got := pool.selectLatencyCandidateForTarget(candidates, func(*memberState) bool { return true }, "api.example.com"); got != second {
		t.Fatalf("target latency selected %v, want second", got)
	}
}

func TestDomainLatencyStateIsBoundedPerNode(t *testing.T) {
	resetHealthPersistenceForTest()
	t.Cleanup(resetHealthPersistenceForTest)
	for index := 0; index < maxDomainLatencyEntriesPerNode+5; index++ {
		recordDomainLatency("node", string(rune('a'+index))+".example", time.Duration(index+1)*time.Millisecond)
	}
	healthPersistence.mu.Lock()
	count := len(healthPersistence.domains["node"])
	healthPersistence.mu.Unlock()
	if count != maxDomainLatencyEntriesPerNode {
		t.Fatalf("domain latency entries = %d, want %d", count, maxDomainLatencyEntriesPerNode)
	}
}
