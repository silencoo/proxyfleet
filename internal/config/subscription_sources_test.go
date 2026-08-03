package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSubscriptionSourcesAcceptLegacyScalarsAndObjects(t *testing.T) {
	var cfg Config
	err := yaml.Unmarshal([]byte(`subscriptions:
  - https://example.com/legacy
  - name: provider-b
    url: https://example.org/sub
    enabled: false
    refresh_interval: 30m
    headers:
      User-Agent: ProxyFleet-Test
`), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.normalizeSubscriptionSources(); err != nil {
		t.Fatal(err)
	}
	sources := cfg.EffectiveSubscriptionSources()
	if len(sources) != 2 || sources[0].Name != "source-1" || sources[1].EnabledValue() || sources[1].RefreshInterval != 30*time.Minute {
		t.Fatalf("unexpected sources: %#v", sources)
	}
	if len(cfg.Subscriptions) != 1 || cfg.Subscriptions[0] != sources[0].URL {
		t.Fatalf("enabled compatibility view=%v", cfg.Subscriptions)
	}
}

func TestSubscriptionSourcesRejectDuplicateAndSensitiveHeaders(t *testing.T) {
	enabled := true
	cfg := &Config{}
	err := cfg.SetSubscriptionSources([]SubscriptionSourceConfig{
		{Name: "a", URL: "https://example.com/sub", Enabled: &enabled},
		{Name: "a", URL: "https://example.org/sub", Enabled: &enabled},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate subscription source name") {
		t.Fatalf("duplicate name error=%v", err)
	}
	err = cfg.SetSubscriptionSources([]SubscriptionSourceConfig{{Name: "a", URL: "https://example.com/sub", Headers: map[string]string{"Authorization": "secret"}}})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("sensitive header error=%v", err)
	}
}

func TestTrafficLogDefaultsAndRelativePath(t *testing.T) {
	cfg := &Config{filePath: `C:\configs\proxyfleet\config.yaml`}
	if err := cfg.normalizeTrafficLogConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.TrafficLog.Enabled || cfg.TrafficLog.Retention != 24*time.Hour || cfg.TrafficLog.MaxEntries != 100_000 || !cfg.TrafficLog.RedactDestinationValue() {
		t.Fatalf("unexpected traffic defaults: %#v", cfg.TrafficLog)
	}
	if path := strings.ToLower(cfg.TrafficLogPath()); !strings.HasSuffix(path, `proxyfleet\traffic-log.db`) {
		t.Fatalf("traffic log path=%q", path)
	}
}

func TestSettingsTransformPersistsSourceEntitiesAndTrafficLog(t *testing.T) {
	redact := false
	cfg := &Config{TrafficLog: TrafficLogConfig{
		Enabled: true, File: "custom-traffic.db", Retention: 2 * time.Hour,
		MaxEntries: 321, RedactDestination: &redact,
	}}
	if err := cfg.SetSubscriptionSources([]SubscriptionSourceConfig{{Name: "provider-a", URL: "https://example.com/sub", RefreshInterval: 30 * time.Minute}}); err != nil {
		t.Fatal(err)
	}
	transformed, err := cfg.transformSettingsData([]byte("mode: pool\nnodes: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := yaml.Unmarshal(transformed, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.SubscriptionSources) != 1 || saved.SubscriptionSources[0].Name != "provider-a" {
		t.Fatalf("saved sources=%#v", saved.SubscriptionSources)
	}
	if !saved.TrafficLog.Enabled || saved.TrafficLog.File != "custom-traffic.db" || saved.TrafficLog.MaxEntries != 321 || saved.TrafficLog.RedactDestinationValue() {
		t.Fatalf("saved traffic log=%#v", saved.TrafficLog)
	}
}
