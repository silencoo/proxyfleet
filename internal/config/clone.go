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
	clone.Profiles = make([]ProfileConfig, len(c.Profiles))
	for index := range c.Profiles {
		clone.Profiles[index] = c.Profiles[index]
		clone.Profiles[index].Regions = append([]string(nil), c.Profiles[index].Regions...)
		clone.Profiles[index].Protocols = append([]string(nil), c.Profiles[index].Protocols...)
		clone.Profiles[index].Sources = append([]string(nil), c.Profiles[index].Sources...)
	}
	clone.Pool.RetryEnabled = cloneBool(c.Pool.RetryEnabled)
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
