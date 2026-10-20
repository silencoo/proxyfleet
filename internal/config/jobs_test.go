package config

import (
	"encoding/json"
	"testing"
)

func jobTestConfig() *Config {
	return &Config{Mode: "hybrid", Profiles: []ProfileConfig{{Name: "fast"}}, Endpoints: []EndpointConfig{{Name: "catalog", Address: "127.0.0.1", Port: 2324, Profile: "fast"}}, Jobs: []JobConfig{{Name: "catalog", Mode: "pooled", Selection: "auto", Profile: "fast", Endpoint: "catalog", TargetURL: "https://example.com/catalog"}, {Name: "accounts", Mode: "pinned", Selection: "auto", Profile: "fast", TargetURL: "https://example.com/"}}}
}
func TestJobsDefaultsValidationAndClone(t *testing.T) {
	c := jobTestConfig()
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	if c.Jobs[0].Size != 5 || c.Jobs[0].RequestTimeout() == 0 || c.Jobs[1].LeaseTTL() != 0 || c.Jobs[1].SessionTTL != "0" {
		t.Fatal("missing defaults")
	}
	clone := c.Clone()
	clone.Jobs[0].Name = "changed"
	if c.Jobs[0].Name != "catalog" {
		t.Fatal("clone aliases job configuration")
	}
	for name, mutate := range map[string]func(*Config){
		"duplicate":            func(c *Config) { c.Jobs[1].Name = "CATALOG" },
		"missing profile":      func(c *Config) { c.Jobs[0].Profile = "missing" },
		"unbound endpoint":     func(c *Config) { c.Endpoints[0].Profile = "" },
		"endpoint reused":      func(c *Config) { c.Jobs = append(c.Jobs, c.Jobs[0]); c.Jobs[2].Name = "other" },
		"pinned pool-only":     func(c *Config) { c.Mode = "pool" },
		"pinned endpoint":      func(c *Config) { c.Jobs[1].Endpoint = "catalog" },
		"target credentials":   func(c *Config) { c.Jobs[0].TargetURL = "https://user:password@example.com" },
		"unsupported target":   func(c *Config) { c.Jobs[0].TargetURL = "file:///etc/passwd" },
		"unbounded timeout":    func(c *Config) { c.Jobs[0].Timeout = "1h" },
		"undersized batch":     func(c *Config) { c.Jobs[0].ProbeBatchSize = 1 },
		"unknown selection":    func(c *Config) { c.Jobs[0].Selection = "typo" },
		"auto without target":  func(c *Config) { c.Jobs[0].TargetURL = "" },
		"negative node limit":  func(c *Config) { c.Jobs[1].MaxAccountsPerNode = -1 },
		"excessive node limit": func(c *Config) { c.Jobs[1].MaxAccountsPerNode = 4097 },
		"negative lifetime":    func(c *Config) { c.Jobs[1].SessionTTL = "-1h" },
		"too short lifetime":   func(c *Config) { c.Jobs[1].SessionTTL = "1s" },
	} {
		t.Run(name, func(t *testing.T) {
			c := jobTestConfig()
			mutate(c)
			if c.NormalizeJobs() == nil {
				t.Fatal("accepted invalid job")
			}
		})
	}
}

func TestPermanentJobConfigRoundTrip(t *testing.T) {
	c := jobTestConfig()
	c.Jobs[1].SessionTTL, c.Jobs[1].MaxAccountsPerNode = "0s", 1
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c.Jobs)
	var restored []JobConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored[1].SessionTTL != "0" || restored[1].MaxAccountsPerNode != 1 || c.Clone().Jobs[1].MaxAccountsPerNode != 1 {
		t.Fatal("permanent binding configuration lost")
	}
}

func TestJobsManualSelectionDefaultsWithoutBenchmark(t *testing.T) {
	c := jobTestConfig()
	for i := range c.Jobs {
		c.Jobs[i].Selection, c.Jobs[i].TargetURL = "", ""
	}
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	for _, j := range c.Jobs {
		if j.Selection != JobSelectionManual || j.AutomaticSelection() || j.Size != 0 || j.RequestTimeout() == 0 {
			t.Fatalf("incorrect manual defaults: %+v", j)
		}
	}
	// An old target URL alone must never implicitly enable automatic selection.
	c.Jobs[0].TargetURL, c.Jobs[0].Size = "https://example.com/", 1
	c.Jobs[1].Selection = " MANUAL "
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	if c.Jobs[0].AutomaticSelection() {
		t.Fatal("target_url enabled automatic selection")
	}
	encoded, err := json.Marshal(c.Jobs)
	if err != nil {
		t.Fatal(err)
	}
	var restored []JobConfig
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored[0].Selection != JobSelectionManual || c.Clone().Jobs[0].Selection != JobSelectionManual {
		t.Fatal("settings round trip lost selection")
	}
	c.Jobs[0].Selection = " AUTO "
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	if !c.Jobs[0].AutomaticSelection() || c.Jobs[1].AutomaticSelection() {
		t.Fatal("selection did not remain independent per job")
	}
}

func TestJobsAllowPinnedOnlyProfilesWithoutPoolEndpoints(t *testing.T) {
	c := jobTestConfig()
	c.Mode = "multi-port"
	c.Jobs = c.Jobs[1:]
	c.Endpoints = nil
	if err := c.NormalizeProfiles(); err != nil {
		t.Fatal(err)
	}
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedJobSessionEndpointValidation(t *testing.T) {
	fixture := func() *Config {
		c := jobTestConfig()
		c.Mode = "pool"
		c.Endpoints = append(c.Endpoints, EndpointConfig{Name: "accounts", Address: "127.0.0.1", Port: 2325, Profile: "fast", Username: "scraper", Password: "secret"})
		c.Jobs[1].Endpoint = "accounts"
		return c
	}
	if err := fixture().NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no auth":              func(c *Config) { c.Endpoints[1].Username = "" },
		"empty password":       func(c *Config) { c.Endpoints[1].Password = "" },
		"ambiguous basic auth": func(c *Config) { c.Endpoints[1].Username = "name:password" },
		"wrong profile":        func(c *Config) { c.Endpoints[1].Profile = "" },
		"no pool listener":     func(c *Config) { c.Mode = "multi-port" },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture()
			mutate(c)
			if c.NormalizeJobs() == nil {
				t.Fatal("accepted invalid endpoint")
			}
		})
	}
	for _, username := range []string{"scraper", "scraper-session-", "scraper-session-a b", "other-session-a", "scraper-session-中文"} {
		if _, ok := ProxySessionID("scraper", username); ok {
			t.Fatalf("accepted %q", username)
		}
	}
	if id, ok := ProxySessionID("name-session-base", "name-session-base-session-account_A-1"); !ok || id != "account_A-1" {
		t.Fatal("base username parsed incorrectly")
	}
}

func sharedJobConfig() *Config {
	return &Config{Mode: "pool", Profiles: []ProfileConfig{{Name: "fast"}}, Endpoints: []EndpointConfig{{Name: "default", Address: "127.0.0.1", Port: 2323, Username: "fleet", Password: "legacy-password"}}, Jobs: []JobConfig{{Name: "a", Mode: "pinned", Profile: "fast", Ingress: "shared", Password: "application-a-secret"}, {Name: "b", Mode: "pooled", Profile: "fast", Password: "application-b-secret"}}}
}
func TestSharedApplicationValidation(t *testing.T) {
	c := sharedJobConfig()
	if err := c.NormalizeJobs(); err != nil {
		t.Fatal(err)
	}
	if !c.Jobs[0].SharedIngress() || !c.Jobs[1].SharedIngress() {
		t.Fatal("shared ingress lost")
	}
	for name, change := range map[string]func(*Config){
		"duplicate password": func(c *Config) { c.Jobs[1].Password = c.Jobs[0].Password },
		"legacy collision":   func(c *Config) { c.Endpoints[0].Password = c.Jobs[0].Password },
		"missing password":   func(c *Config) { c.Jobs[0].Password = "" },
		"short password":     func(c *Config) { c.Jobs[0].Password = "short" },
		"control password":   func(c *Config) { c.Jobs[0].Password = "long-enough-secret\n" },
		"ambiguous endpoint": func(c *Config) { c.Jobs[0].Endpoint = "default" },
		"filtered primary":   func(c *Config) { c.Endpoints[0].Profile = "fast" },
		"anonymous primary":  func(c *Config) { c.Endpoints[0].Username = ""; c.Endpoints[0].Password = "" },
		"multi port":         func(c *Config) { c.Mode = "multi-port" },
		"unknown ingress":    func(c *Config) { c.Jobs[0].Ingress = "mystery" },
	} {
		t.Run(name, func(t *testing.T) {
			c := sharedJobConfig()
			change(c)
			if err := c.NormalizeJobs(); err == nil {
				t.Fatal("invalid shared configuration accepted")
			}
		})
	}
	data, _ := json.Marshal(c.Jobs)
	var restored []JobConfig
	if err := json.Unmarshal(data, &restored); err != nil || restored[0].Password != c.Jobs[0].Password || !restored[0].SharedIngress() {
		t.Fatal("credential round trip failed")
	}
}

func TestIdleRetentionValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"", true}, {"0", true}, {"24h", true}, {"720h", true}, {"30s", false}, {"-1h", false}, {"721h", false}, {"oops", false}} {
		t.Run(tc.value, func(t *testing.T) {
			c := jobTestConfig()
			c.Jobs = c.Jobs[:1]
			c.Jobs[0].Mode = "pinned"
			c.Jobs[0].SessionIdleTimeout = tc.value
			c.Endpoints[0].Username, c.Endpoints[0].Password = "worker", "secret"
			if err := c.NormalizeJobs(); (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	for _, kind := range []string{"direct", "pooled"} {
		c := jobTestConfig()
		if kind == "direct" {
			c.Jobs = c.Jobs[1:]
		} else {
			c.Jobs = c.Jobs[:1]
		}
		c.Jobs[0].SessionIdleTimeout = "24h"
		if c.NormalizeJobs() == nil {
			t.Fatal("accepted idle retention for", kind)
		}
	}
}
