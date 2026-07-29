package config

import (
	"testing"
	"time"
)

func TestNormalizePoolConfigAppliesAdaptiveDefaults(t *testing.T) {
	cfg := &Config{}
	if err := cfg.normalizePoolConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.Pool.Mode != "sequential" || !cfg.Pool.RetryEnabledValue() || cfg.Pool.RetryAttempts != 3 {
		t.Fatalf("unexpected pool defaults: %#v", cfg.Pool)
	}
	if cfg.Pool.TransientCooldown != time.Minute || cfg.Pool.LatencySampleSize != 4 || cfg.Pool.LatencyTolerance != 50*time.Millisecond {
		t.Fatalf("unexpected adaptive defaults: %#v", cfg.Pool)
	}
	if cfg.Pool.Sticky.TTL != 30*time.Minute || cfg.Pool.Sticky.MaxEntries != 4096 {
		t.Fatalf("unexpected sticky defaults: %#v", cfg.Pool.Sticky)
	}
}

func TestNormalizeProfilesCanonicalizesFiltersAndRejectsInvalidConfiguration(t *testing.T) {
	cfg := &Config{
		Mode:     "pool",
		Listener: ListenerConfig{Username: "fleet", Password: "secret"},
		Profiles: []ProfileConfig{{
			Name: " HK-Fast ", Regions: []string{"HK", " hk ", "SG"},
			Protocols: []string{"VLESS"}, Sources: []string{"Subscription"},
			NameRegex: "(?i)premium", MinQuality: 75,
		}},
	}
	if err := cfg.NormalizeProfiles(); err != nil {
		t.Fatalf("valid profiles rejected: %v", err)
	}
	profile := cfg.Profiles[0]
	if profile.Name != "hk-fast" || len(profile.Regions) != 2 || profile.Regions[0] != "hk" || profile.Protocols[0] != "vless" || profile.Sources[0] != "subscription" {
		t.Fatalf("profiles were not canonicalized: %#v", profile)
	}

	cfg.Profiles = []ProfileConfig{{Name: "bad", NameRegex: "["}}
	if err := cfg.NormalizeProfiles(); err == nil {
		t.Fatal("invalid profile regex was accepted")
	}
	cfg.Profiles = []ProfileConfig{{Name: "same"}, {Name: "SAME"}}
	if err := cfg.NormalizeProfiles(); err == nil {
		t.Fatal("duplicate profile names were accepted")
	}
	cfg.Listener.Password = ""
	cfg.Profiles = []ProfileConfig{{Name: "valid"}}
	if err := cfg.NormalizeProfiles(); err == nil {
		t.Fatal("profiles without unified listener authentication were accepted")
	}
}

func TestNormalizePoolConfigAcceptsRoundRobinAliasAndRejectsUnknown(t *testing.T) {
	cfg := &Config{Pool: PoolConfig{Mode: "round-robin"}}
	if err := cfg.normalizePoolConfig(); err != nil || cfg.Pool.Mode != "sequential" {
		t.Fatalf("round-robin alias was not normalized: mode=%q err=%v", cfg.Pool.Mode, err)
	}
	cfg.Pool.Mode = "fastest-at-all-costs"
	if err := cfg.normalizePoolConfig(); err == nil {
		t.Fatal("unknown pool mode was accepted")
	}
}
