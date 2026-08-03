package config

import (
	"strings"
	"testing"
)

func endpointEnabled(value bool) *bool { return &value }

func TestEffectiveEndpointsKeepsLegacyListenerCompatible(t *testing.T) {
	cfg := &Config{Listener: ListenerConfig{Address: "127.0.0.1", Port: 2323, Username: "fleet", Password: "secret"}}
	endpoints := cfg.EffectiveEndpoints()
	if len(endpoints) != 1 || endpoints[0].Name != "default" || !endpoints[0].EnabledValue() {
		t.Fatalf("unexpected legacy endpoint view: %#v", endpoints)
	}
	if endpoints[0].Address != cfg.Listener.Address || endpoints[0].Port != cfg.Listener.Port {
		t.Fatalf("legacy listener was not mirrored: endpoint=%#v listener=%#v", endpoints[0], cfg.Listener)
	}
}

func TestNormalizeEndpointsDefaultsEnabledAndMirrorsPrimary(t *testing.T) {
	cfg := &Config{
		Mode:     "pool",
		Profiles: []ProfileConfig{{Name: "hk-fast"}},
		Endpoints: []EndpointConfig{
			{Name: " HK-ONLY ", Enabled: endpointEnabled(false), Address: "[::1]", Port: 2424, Profile: "hk-fast"},
			{Name: " Public ", Address: "127.0.0.1", Port: 2323, Username: "fleet", Password: "secret"},
		},
	}
	if err := cfg.NormalizeEndpoints(); err != nil {
		t.Fatalf("normalize endpoints: %v", err)
	}
	if cfg.Endpoints[0].Name != "hk-only" || cfg.Endpoints[0].Address != "::1" || cfg.Endpoints[0].EnabledValue() {
		t.Fatalf("first endpoint was not normalized: %#v", cfg.Endpoints[0])
	}
	if cfg.Endpoints[1].Name != "public" || !cfg.Endpoints[1].EnabledValue() {
		t.Fatalf("default enabled endpoint was not normalized: %#v", cfg.Endpoints[1])
	}
	if cfg.Listener.Address != "127.0.0.1" || cfg.Listener.Port != 2323 || cfg.Listener.Username != "fleet" {
		t.Fatalf("primary endpoint did not update the listener compatibility mirror: %#v", cfg.Listener)
	}
}

func TestNormalizeEndpointsRejectsConflictsAndInvalidReferences(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []EndpointConfig
		want      string
	}{
		{name: "duplicate name", endpoints: []EndpointConfig{{Name: "one", Address: "127.0.0.1", Port: 2323}, {Name: "ONE", Address: "127.0.0.1", Port: 2324}}, want: "duplicate endpoint name"},
		{name: "duplicate socket", endpoints: []EndpointConfig{{Name: "one", Address: "127.0.0.1", Port: 2323}, {Name: "two", Address: "127.0.0.1", Port: 2323}}, want: "same listener"},
		{name: "unknown profile", endpoints: []EndpointConfig{{Name: "one", Address: "127.0.0.1", Port: 2323, Profile: "missing"}}, want: "unknown profile"},
		{name: "partial credentials", endpoints: []EndpointConfig{{Name: "one", Address: "127.0.0.1", Port: 2323, Username: "fleet"}}, want: "username and password"},
		{name: "hostname", endpoints: []EndpointConfig{{Name: "one", Address: "localhost", Port: 2323}}, want: "unable to parse IP"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{Mode: "pool", Endpoints: test.endpoints}
			err := cfg.NormalizeEndpoints()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCloneDeepCopiesEndpointEnabledFlag(t *testing.T) {
	cfg := &Config{Endpoints: []EndpointConfig{{Name: "one", Enabled: endpointEnabled(true), Address: "127.0.0.1", Port: 2323}}}
	clone := cfg.Clone()
	*clone.Endpoints[0].Enabled = false
	if !cfg.Endpoints[0].EnabledValue() {
		t.Fatal("mutating cloned endpoint enabled flag changed the source config")
	}
}

func TestProfilesRemainValidWhileManagedEndpointsAreInactive(t *testing.T) {
	cfg := &Config{
		Mode:      "multi-port",
		Profiles:  []ProfileConfig{{Name: "hk-fast", Regions: []string{"hk"}}},
		Endpoints: []EndpointConfig{{Name: "hk-only", Address: "127.0.0.1", Port: 2323, Profile: "hk-fast"}},
	}
	if err := cfg.NormalizeProfiles(); err != nil {
		t.Fatalf("multi-port mode should preserve endpoint profiles: %v", err)
	}
	if err := cfg.NormalizeEndpoints(); err != nil {
		t.Fatalf("multi-port mode should preserve endpoints: %v", err)
	}
}
