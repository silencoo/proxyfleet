package config

// Clone returns an independently mutable copy of the configuration.
//
// Most configuration fields are values. The slices and optional booleans need
// explicit copies so runtime components never share mutable storage. The
// unexported source path is intentionally preserved because clones are used by
// transactional persistence callbacks.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}

	clone := *c
	clone.Nodes = append([]NodeConfig(nil), c.Nodes...)
	clone.Subscriptions = append([]string(nil), c.Subscriptions...)
	clone.SubscriptionSources = cloneSubscriptionSources(c.SubscriptionSources)
	clone.Endpoints = make([]EndpointConfig, len(c.Endpoints))
	for index := range c.Endpoints {
		clone.Endpoints[index] = c.Endpoints[index]
		clone.Endpoints[index].Enabled = cloneBool(c.Endpoints[index].Enabled)
	}
	clone.Profiles = make([]ProfileConfig, len(c.Profiles))
	for index := range c.Profiles {
		clone.Profiles[index] = c.Profiles[index]
		clone.Profiles[index].Regions = append([]string(nil), c.Profiles[index].Regions...)
		clone.Profiles[index].Protocols = append([]string(nil), c.Profiles[index].Protocols...)
		clone.Profiles[index].Sources = append([]string(nil), c.Profiles[index].Sources...)
		clone.Profiles[index].TagRules.Any = append([]string(nil), c.Profiles[index].TagRules.Any...)
		clone.Profiles[index].TagRules.Must = append([]string(nil), c.Profiles[index].TagRules.Must...)
		clone.Profiles[index].TagRules.MustNot = append([]string(nil), c.Profiles[index].TagRules.MustNot...)
	}
	clone.Pool.RetryEnabled = cloneBool(c.Pool.RetryEnabled)
	clone.TrafficLog.RedactDestination = cloneBool(c.TrafficLog.RedactDestination)
	clone.Management.Enabled = cloneBool(c.Management.Enabled)
	clone.filePath = c.filePath
	return &clone
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
