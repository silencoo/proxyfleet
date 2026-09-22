package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/silencoo/proxyfleet/internal/probetarget"
)

// JobConfig separates a workload's candidate filter from its connection policy.
// Durations are strings in both YAML and the management JSON API.
type JobConfig struct {
	Name             string `yaml:"name" json:"name"`
	Mode             string `yaml:"mode" json:"mode"`
	Selection        string `yaml:"selection" json:"selection"`
	Profile          string `yaml:"profile" json:"profile"`
	Endpoint         string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	TargetURL        string `yaml:"target_url" json:"target_url"`
	Size             int    `yaml:"size" json:"size"`
	Concurrency      int    `yaml:"concurrency" json:"concurrency"`
	Timeout          string `yaml:"timeout" json:"timeout"`
	RefreshInterval  string `yaml:"refresh_interval" json:"refresh_interval"`
	SessionTTL       string `yaml:"session_ttl" json:"session_ttl"`
	MaxSessions      int    `yaml:"max_sessions" json:"max_sessions"`
	ProbeBatchSize   int    `yaml:"probe_batch_size" json:"probe_batch_size"`
	ProbeConcurrency int    `yaml:"probe_concurrency" json:"probe_concurrency"`
	MaxResponseBytes int64  `yaml:"max_response_bytes" json:"max_response_bytes"`
	ExpectedStatus   int    `yaml:"expected_status" json:"expected_status"`
	BodyContains     string `yaml:"body_contains,omitempty" json:"body_contains,omitempty"`
}

const (
	JobSelectionManual = "manual"
	JobSelectionAuto   = "auto"
)

func (j JobConfig) EffectiveSelection() string {
	if j.Selection == "" {
		return JobSelectionManual
	}
	return j.Selection
}
func (j JobConfig) AutomaticSelection() bool { return j.EffectiveSelection() == JobSelectionAuto }

func (j JobConfig) RequestTimeout() time.Duration { d, _ := time.ParseDuration(j.Timeout); return d }
func (j JobConfig) RefreshEvery() time.Duration {
	d, _ := time.ParseDuration(j.RefreshInterval)
	return d
}
func (j JobConfig) LeaseTTL() time.Duration { d, _ := time.ParseDuration(j.SessionTTL); return d }

func (c *Config) NormalizeJobs() error {
	if len(c.Jobs) > 64 {
		return fmt.Errorf("at most 64 jobs are supported")
	}
	profiles := make(map[string]bool)
	for _, p := range c.Profiles {
		profiles[p.Name] = true
	}
	endpoints := make(map[string]EndpointConfig)
	for _, e := range c.EffectiveEndpoints() {
		endpoints[e.Name] = e
	}
	seen, usedEndpoints := make(map[string]bool), make(map[string]bool)
	for i := range c.Jobs {
		j := &c.Jobs[i]
		j.Name = strings.ToLower(strings.TrimSpace(j.Name))
		j.Mode = strings.ToLower(strings.TrimSpace(j.Mode))
		j.Selection = strings.ToLower(strings.TrimSpace(j.Selection))
		j.Selection = j.EffectiveSelection()
		if j.Selection != JobSelectionManual && j.Selection != JobSelectionAuto {
			return fmt.Errorf("job %q selection must be manual or auto", j.Name)
		}
		j.Profile = strings.ToLower(strings.TrimSpace(j.Profile))
		j.Endpoint = strings.ToLower(strings.TrimSpace(j.Endpoint))
		if !profileNamePattern.MatchString(j.Name) || seen[j.Name] {
			return fmt.Errorf("job %d has an invalid or duplicate name", i)
		}
		seen[j.Name] = true
		if !profiles[j.Profile] {
			return fmt.Errorf("job %q requires an existing profile", j.Name)
		}
		switch j.Mode {
		case "pooled":
			e, ok := endpoints[j.Endpoint]
			if (c.Mode != "pool" && c.Mode != "hybrid") || !ok || !e.EnabledValue() || e.Profile != j.Profile || usedEndpoints[j.Endpoint] {
				return fmt.Errorf("pooled job %q requires its own enabled endpoint bound to the same profile in pool/hybrid mode", j.Name)
			}
			usedEndpoints[j.Endpoint] = true
		case "pinned":
			if j.Endpoint == "" {
				if c.Mode != "multi-port" && c.Mode != "hybrid" {
					return fmt.Errorf("pinned job %q requires an endpoint in pool mode", j.Name)
				}
			} else {
				e, ok := endpoints[j.Endpoint]
				if (c.Mode != "pool" && c.Mode != "hybrid") || !ok || !e.EnabledValue() || e.Profile != j.Profile || usedEndpoints[j.Endpoint] {
					return fmt.Errorf("pinned job %q requires its own enabled endpoint bound to the same profile in pool/hybrid mode", j.Name)
				}
				if !ValidSessionCredentials(e.Username, e.Password) {
					return fmt.Errorf("pinned job %q endpoint requires a username (1-118 bytes, no colon/control characters) and password (1-255 bytes)", j.Name)
				}
				usedEndpoints[j.Endpoint] = true
			}
		default:
			return fmt.Errorf("job %q mode must be pooled or pinned", j.Name)
		}
		j.TargetURL = strings.TrimSpace(j.TargetURL)
		if j.AutomaticSelection() || j.TargetURL != "" {
			u, err := url.Parse(j.TargetURL)
			if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || len(j.TargetURL) > 4096 {
				return fmt.Errorf("job %q requires an HTTP(S) target_url without credentials or fragment for automatic selection", j.Name)
			}
			if _, ready, err := probetarget.Parse(j.TargetURL); err != nil || !ready {
				return fmt.Errorf("job %q has an invalid target_url", j.Name)
			}
		}
		if j.AutomaticSelection() && j.Size == 0 {
			j.Size = 5
		}
		if j.Concurrency == 0 {
			j.Concurrency = 8
		}
		if j.MaxSessions == 0 {
			j.MaxSessions = 1024
		}
		if j.ProbeBatchSize == 0 {
			j.ProbeBatchSize = 32
		}
		if j.ProbeConcurrency == 0 {
			j.ProbeConcurrency = 4
		}
		if j.MaxResponseBytes == 0 {
			j.MaxResponseBytes = 2 << 20
		}
		if j.ExpectedStatus == 0 {
			j.ExpectedStatus = 200
		}
		if j.Size < 0 || j.Size > 64 || j.Concurrency < 1 || j.Concurrency > 1024 || j.MaxSessions < 1 || j.MaxSessions > 4096 || j.ProbeBatchSize < 1 || (j.AutomaticSelection() && j.ProbeBatchSize < j.Size) || j.ProbeBatchSize > 256 || j.ProbeConcurrency < 1 || j.ProbeConcurrency > 16 || j.MaxResponseBytes < 1 || j.MaxResponseBytes > 8<<20 || j.ExpectedStatus < 200 || j.ExpectedStatus > 299 || len(j.BodyContains) > 4096 {
			return fmt.Errorf("job %q has out-of-range limits (probe_batch_size must also be >= size)", j.Name)
		}
		for _, field := range []struct {
			value    *string
			fallback string
			min, max time.Duration
		}{
			{&j.Timeout, "15s", 100 * time.Millisecond, 2 * time.Minute},
			{&j.RefreshInterval, "10m", time.Minute, 24 * time.Hour},
			{&j.SessionTTL, "30m", time.Minute, 7 * 24 * time.Hour},
		} {
			if *field.value == "" {
				*field.value = field.fallback
			}
			d, err := time.ParseDuration(*field.value)
			if err != nil || d < field.min || d > field.max {
				return fmt.Errorf("job %q has an invalid duration", j.Name)
			}
		}
	}
	return nil
}

const SessionUsernameSeparator = "-session-"

// Session IDs are portable across HTTP Basic auth, SOCKS5, and proxy URLs.
func ValidProxySessionID(session string) bool {
	if len(session) < 1 || len(session) > 128 {
		return false
	}
	for _, c := range session {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func ProxySessionID(base, username string) (string, bool) {
	session, ok := strings.CutPrefix(username, base+SessionUsernameSeparator)
	return session, ok && base != "" && ValidProxySessionID(session)
}

func ValidSessionCredentials(username, password string) bool {
	if len(username) == 0 || len(username)+len(SessionUsernameSeparator)+128 > 255 || len(password) == 0 || len(password) > 255 || strings.Contains(username, ":") {
		return false
	}
	for _, c := range username + password {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
