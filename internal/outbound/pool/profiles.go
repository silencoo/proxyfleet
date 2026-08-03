package pool

import (
	"context"
	"fmt"
	"strings"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

type compiledProfile struct {
	name       string
	allowed    map[string]struct{}
	minQuality float64
}

func compileProfiles(options []ProfileOptions, metadata map[string]MemberMeta) (map[string]*compiledProfile, error) {
	profiles := make(map[string]*compiledProfile, len(options))
	for _, option := range options {
		name := strings.ToLower(strings.TrimSpace(option.Name))
		if name == "" {
			return nil, fmt.Errorf("pool profile name is empty")
		}
		if _, exists := profiles[name]; exists {
			return nil, fmt.Errorf("duplicate pool profile %q", name)
		}
		nameMatcher, err := config.CompileProfileNameMatcher(config.ProfileConfig{
			Name: name, NameRegex: option.NameRegex, TagRules: option.TagRules,
		})
		if err != nil {
			return nil, fmt.Errorf("compile pool profile %q: %w", name, err)
		}
		profile := &compiledProfile{name: name, allowed: make(map[string]struct{}), minQuality: option.MinQuality}
		regions := stringSet(option.Regions)
		protocols := stringSet(option.Protocols)
		sources := stringSet(option.Sources)
		for tag, member := range metadata {
			region := monitor.ResolveDisplayRegion(member.Name, tag, member.Country, member.Region)
			if len(regions) > 0 {
				if _, ok := regions[region]; !ok {
					continue
				}
			}
			if len(protocols) > 0 {
				if _, ok := protocols[strings.ToLower(member.Protocol)]; !ok {
					continue
				}
			}
			if len(sources) > 0 {
				if _, ok := sources[strings.ToLower(member.Source)]; !ok {
					continue
				}
			}
			if !nameMatcher.Match(member.Name).Allowed {
				continue
			}
			profile.allowed[tag] = struct{}{}
		}
		profiles[name] = profile
	}
	return profiles, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func profileAllowsMember(profile *compiledProfile, member *memberState) bool {
	if profile == nil {
		return true
	}
	if member == nil {
		return false
	}
	if _, ok := profile.allowed[member.tag]; !ok {
		return false
	}
	return profile.minQuality <= 0 || memberQualityScore(member) >= profile.minQuality
}

func (p *poolOutbound) profileFromContext(ctx context.Context) *compiledProfile {
	if p == nil || len(p.profiles) == 0 {
		return nil
	}
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		return nil
	}
	if profileName := strings.ToLower(strings.TrimSpace(p.options.EndpointProfiles[metadata.Inbound])); profileName != "" {
		return p.profiles[profileName]
	}
	username := strings.TrimSpace(metadata.User)
	separator := strings.LastIndexByte(username, '@')
	if separator < 0 || separator == len(username)-1 {
		return nil
	}
	return p.profiles[strings.ToLower(username[separator+1:])]
}

func destinationLatencyKey(destination M.Socksaddr) string {
	if domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(destination.Fqdn)), "."); domain != "" {
		return domain
	}
	if destination.Addr.IsValid() {
		return destination.Addr.String()
	}
	return ""
}
