package config

import "fmt"

const (
	CertVerifyDefault  = "default"
	CertVerifyOverride = "override"
)

func (c *Config) CertVerifyModeOrDefault() string {
	if c == nil || c.SkipCertVerifyMode == "" {
		return CertVerifyDefault
	}
	return c.SkipCertVerifyMode
}

func (c *Config) NormalizeCertVerifyMode() error {
	mode := c.CertVerifyModeOrDefault()
	if mode != CertVerifyDefault && mode != CertVerifyOverride {
		return fmt.Errorf("skip_cert_verify_mode must be default or override")
	}
	c.SkipCertVerifyMode = mode
	return nil
}
