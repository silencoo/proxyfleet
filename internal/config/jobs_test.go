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
	if c.Jobs[0].Size != 5 || c.Jobs[0].RequestTimeout() == 0 || c.Jobs[1].LeaseTTL() == 0 {
		t.Fatal("missing defaults")
	}
	clone := c.Clone()
	clone.Jobs[0].Name = "changed"
	if c.Jobs[0].Name != "catalog" {
		t.Fatal("clone aliases job configuration")
	}
	for name, mutate := range map[string]func(*Config){
		"duplicate":           func(c *Config) { c.Jobs[1].Name = "CATALOG" },
		"missing profile":     func(c *Config) { c.Jobs[0].Profile = "missing" },
		"unbound endpoint":    func(c *Config) { c.Endpoints[0].Profile = "" },
		"endpoint reused":     func(c *Config) { c.Jobs = append(c.Jobs, c.Jobs[0]); c.Jobs[2].Name = "other" },
		"pinned pool-only":    func(c *Config) { c.Mode = "pool" },
		"pinned endpoint":     func(c *Config) { c.Jobs[1].Endpoint = "catalog" },
		"target credentials":  func(c *Config) { c.Jobs[0].TargetURL = "https://user:password@example.com" },
		"unsupported target":  func(c *Config) { c.Jobs[0].TargetURL = "file:///etc/passwd" },
		"unbounded timeout":   func(c *Config) { c.Jobs[0].Timeout = "1h" },
		"undersized batch":    func(c *Config) { c.Jobs[0].ProbeBatchSize = 1 },
		"unknown selection":   func(c *Config) { c.Jobs[0].Selection = "typo" },
		"auto without target": func(c *Config) { c.Jobs[0].TargetURL = "" },
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
