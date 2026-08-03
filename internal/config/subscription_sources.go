package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// SubscriptionSourceConfig is one independently managed provider. An omitted
// enabled value defaults to true; a zero refresh interval inherits the global
// subscription_refresh.interval.
type SubscriptionSourceConfig struct {
	Name            string            `yaml:"name,omitempty" json:"name"`
	URL             string            `yaml:"url" json:"url"`
	Enabled         *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	RefreshInterval time.Duration     `yaml:"refresh_interval,omitempty" json:"refresh_interval,omitempty"`
	Headers         map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// UnmarshalYAML accepts both the historical scalar URL and the object form.
func (s *SubscriptionSourceConfig) UnmarshalYAML(unmarshal func(any) error) error {
	var legacy string
	if err := unmarshal(&legacy); err == nil {
		s.URL = legacy
		return nil
	}
	type plain SubscriptionSourceConfig
	var object plain
	if err := unmarshal(&object); err != nil {
		return errors.New("subscription must be a URL string or source object")
	}
	*s = SubscriptionSourceConfig(object)
	return nil
}

func (s SubscriptionSourceConfig) EnabledValue() bool {
	return s.Enabled == nil || *s.Enabled
}

func (s SubscriptionSourceConfig) IntervalOrDefault(fallback time.Duration) time.Duration {
	if s.RefreshInterval > 0 {
		return s.RefreshInterval
	}
	if fallback > 0 {
		return fallback
	}
	return time.Hour
}

func (s SubscriptionSourceConfig) Key() string {
	return subscriptionURLKey(strings.TrimSpace(s.URL))
}

func cloneSubscriptionSources(sources []SubscriptionSourceConfig) []SubscriptionSourceConfig {
	cloned := make([]SubscriptionSourceConfig, len(sources))
	for index := range sources {
		cloned[index] = sources[index]
		cloned[index].Enabled = cloneBool(sources[index].Enabled)
		if sources[index].Headers != nil {
			cloned[index].Headers = make(map[string]string, len(sources[index].Headers))
			for name, value := range sources[index].Headers {
				cloned[index].Headers[name] = value
			}
		}
	}
	return cloned
}

func SubscriptionSourcesFromURLs(urls []string) []SubscriptionSourceConfig {
	sources := make([]SubscriptionSourceConfig, 0, len(urls))
	for index, rawURL := range urls {
		if rawURL = strings.TrimSpace(rawURL); rawURL != "" {
			sources = append(sources, SubscriptionSourceConfig{Name: fmt.Sprintf("source-%d", index+1), URL: rawURL})
		}
	}
	return sources
}

func (c *Config) EffectiveSubscriptionSources() []SubscriptionSourceConfig {
	if c == nil {
		return nil
	}
	if len(c.SubscriptionSources) > 0 {
		return cloneSubscriptionSources(c.SubscriptionSources)
	}
	return SubscriptionSourcesFromURLs(c.Subscriptions)
}

func (c *Config) EnabledSubscriptionURLs() []string {
	sources := c.EffectiveSubscriptionSources()
	urls := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.EnabledValue() {
			urls = append(urls, source.URL)
		}
	}
	return urls
}

func (c *Config) subscriptionHeadersBySourceKey() map[string]map[string]string {
	result := make(map[string]map[string]string)
	for _, source := range c.EffectiveSubscriptionSources() {
		if source.EnabledValue() && len(source.Headers) > 0 {
			result[source.Key()] = source.Headers
		}
	}
	return result
}

func (c *Config) SetSubscriptionSources(sources []SubscriptionSourceConfig) error {
	if c == nil {
		return errors.New("config is nil")
	}
	c.SubscriptionSources = cloneSubscriptionSources(sources)
	c.Subscriptions = nil
	return c.normalizeSubscriptionSources()
}

func (c *Config) normalizeSubscriptionSources() error {
	if len(c.SubscriptionSources) == 0 && len(c.Subscriptions) > 0 {
		c.SubscriptionSources = SubscriptionSourcesFromURLs(c.Subscriptions)
	}
	if len(c.SubscriptionSources) == 0 {
		c.Subscriptions = nil
		return nil
	}
	if len(c.SubscriptionSources) > MaxSubscriptionURLs {
		return fmt.Errorf("too many subscription sources (maximum %d)", MaxSubscriptionURLs)
	}
	seenNames := make(map[string]struct{}, len(c.SubscriptionSources))
	seenURLs := make(map[string]struct{}, len(c.SubscriptionSources))
	allURLs := make([]string, 0, len(c.SubscriptionSources))
	for index := range c.SubscriptionSources {
		source := &c.SubscriptionSources[index]
		source.Name = strings.ToLower(strings.TrimSpace(source.Name))
		if source.Name == "" {
			source.Name = fmt.Sprintf("source-%d", index+1)
		}
		if !profileNamePattern.MatchString(source.Name) {
			return fmt.Errorf("subscription source %d name must match %s", index, profileNamePattern.String())
		}
		if _, exists := seenNames[source.Name]; exists {
			return fmt.Errorf("duplicate subscription source name %q", source.Name)
		}
		seenNames[source.Name] = struct{}{}
		source.URL = strings.TrimSpace(source.URL)
		validated, err := ValidateSubscriptionURLs([]string{source.URL})
		if err != nil || len(validated) != 1 {
			if err == nil {
				err = errors.New("URL is empty")
			}
			return fmt.Errorf("subscription source %q: %w", source.Name, err)
		}
		source.URL = validated[0]
		key := source.Key()
		if _, exists := seenURLs[key]; exists {
			return fmt.Errorf("duplicate subscription URL in source %q", source.Name)
		}
		seenURLs[key] = struct{}{}
		allURLs = append(allURLs, source.URL)
		if source.RefreshInterval < 0 || (source.RefreshInterval > 0 && source.RefreshInterval < 10*time.Second) {
			return fmt.Errorf("subscription source %q refresh_interval must be zero or at least 10s", source.Name)
		}
		normalizedHeaders := make(map[string]string, len(source.Headers))
		for name, value := range source.Headers {
			name = strings.TrimSpace(name)
			if name == "" || containsControlCharacter(name) || containsControlCharacter(value) {
				return fmt.Errorf("subscription source %q contains an invalid request header", source.Name)
			}
			if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") {
				return fmt.Errorf("subscription source %q header %q is not allowed", source.Name, name)
			}
			normalizedHeaders[name] = strings.TrimSpace(value)
		}
		if len(normalizedHeaders) == 0 {
			source.Headers = nil
		} else {
			source.Headers = normalizedHeaders
		}
	}
	if _, err := ValidateSubscriptionURLs(allURLs); err != nil {
		return err
	}
	c.Subscriptions = c.EnabledSubscriptionURLs()
	return nil
}
