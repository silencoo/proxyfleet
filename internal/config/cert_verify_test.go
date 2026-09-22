package config

import "testing"

func TestCertificateVerificationModeDefaultsAndValidation(t *testing.T) {
	for _, mode := range []string{"", "default", "override", "invalid"} {
		cfg := &Config{SkipCertVerifyMode: mode}
		err := cfg.NormalizeCertVerifyMode()
		if (err != nil) != (mode == "invalid") {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
		if mode == "" && cfg.SkipCertVerifyMode != "default" {
			t.Fatal("missing mode did not default")
		}
	}
	for _, normalize := range []func(*Config) error{func(c *Config) error { return c.normalize() }, func(c *Config) error { return c.NormalizeWithPortMap(nil) }} {
		if err := normalize(&Config{SkipCertVerifyMode: "invalid"}); err == nil {
			t.Fatal("normalization accepted an unknown verification policy")
		}
	}
}
