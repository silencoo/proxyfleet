package config

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxProfileRules      = 64
	maxProfileRuleBytes  = 4096
	maxProfileRulesBytes = 64 * 1024
)

// ProfileTagRules combines node-name regular expressions. ANY requires at
// least one match, MUST requires every match, and MUST_NOT rejects any match.
type ProfileTagRules struct {
	Any     []string `yaml:"any,omitempty" json:"any,omitempty"`
	Must    []string `yaml:"must,omitempty" json:"must,omitempty"`
	MustNot []string `yaml:"must_not,omitempty" json:"must_not,omitempty"`
}

// ProfileRuleMatch explains the first rule group that accepted or rejected a
// node name. Pattern never contains node credentials because rules only see
// the display name.
type ProfileRuleMatch struct {
	Allowed bool
	Group   string
	Index   int
	Pattern string
}

type compiledProfileRule struct {
	pattern string
	regex   *regexp.Regexp
}

// ProfileNameMatcher is immutable and safe to reuse across all members in a
// large pool.
type ProfileNameMatcher struct {
	any     []compiledProfileRule
	must    []compiledProfileRule
	mustNot []compiledProfileRule
}

// CompileProfileNameMatcher compiles both the legacy name_regex and the new
// composable tag_rules representation. name_regex remains an additional MUST
// rule so old configuration keeps identical behavior.
func CompileProfileNameMatcher(profile ProfileConfig) (*ProfileNameMatcher, error) {
	matcher := &ProfileNameMatcher{}
	var err error
	matcher.any, err = compileProfileRuleGroup(profile.Name, "any", profile.TagRules.Any)
	if err != nil {
		return nil, err
	}
	must := append([]string(nil), profile.TagRules.Must...)
	if legacy := strings.TrimSpace(profile.NameRegex); legacy != "" {
		must = append([]string{legacy}, must...)
	}
	matcher.must, err = compileProfileRuleGroup(profile.Name, "must", must)
	if err != nil {
		return nil, err
	}
	matcher.mustNot, err = compileProfileRuleGroup(profile.Name, "must_not", profile.TagRules.MustNot)
	if err != nil {
		return nil, err
	}
	return matcher, nil
}

func compileProfileRuleGroup(profileName, group string, patterns []string) ([]compiledProfileRule, error) {
	compiled := make([]compiledProfileRule, 0, len(patterns))
	for index, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("profile %q tag_rules.%s[%d]: %w", profileName, group, index, err)
		}
		compiled = append(compiled, compiledProfileRule{pattern: pattern, regex: re})
	}
	return compiled, nil
}

// Match evaluates one display name in deterministic ANY, MUST, MUST_NOT order.
func (m *ProfileNameMatcher) Match(name string) ProfileRuleMatch {
	if m == nil {
		return ProfileRuleMatch{Allowed: true, Index: -1}
	}
	if len(m.any) > 0 {
		matched := false
		for _, rule := range m.any {
			if rule.regex.MatchString(name) {
				matched = true
				break
			}
		}
		if !matched {
			return ProfileRuleMatch{Allowed: false, Group: "any", Index: -1}
		}
	}
	for index, rule := range m.must {
		if !rule.regex.MatchString(name) {
			return ProfileRuleMatch{Allowed: false, Group: "must", Index: index, Pattern: rule.pattern}
		}
	}
	for index, rule := range m.mustNot {
		if rule.regex.MatchString(name) {
			return ProfileRuleMatch{Allowed: false, Group: "must_not", Index: index, Pattern: rule.pattern}
		}
	}
	return ProfileRuleMatch{Allowed: true, Index: -1}
}

func normalizeProfileRules(profileName, group string, values []string) ([]string, error) {
	if len(values) > maxProfileRules {
		return nil, fmt.Errorf("profile %q tag_rules.%s exceeds %d entries", profileName, group, maxProfileRules)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	totalBytes := 0
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxProfileRuleBytes {
			return nil, fmt.Errorf("profile %q tag_rules.%s[%d] exceeds %d bytes", profileName, group, index, maxProfileRuleBytes)
		}
		totalBytes += len(value)
		if totalBytes > maxProfileRulesBytes {
			return nil, fmt.Errorf("profile %q tag_rules.%s exceeds %d total bytes", profileName, group, maxProfileRulesBytes)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		if _, err := regexp.Compile(value); err != nil {
			return nil, fmt.Errorf("profile %q tag_rules.%s[%d]: %w", profileName, group, index, err)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
