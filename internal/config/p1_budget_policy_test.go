package config

import "testing"

func TestAdaptiveProbeBudgetsReceiveSafeDefaults(t *testing.T) {
	cfg := &Config{Management: ManagementConfig{ProbeMode: "adaptive"}}
	if err := cfg.normalizeManagementProbeConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.ProbeMaxPerHourOrDefault() != 600 || cfg.ProbeMaxPerDayOrDefault() != 5000 {
		t.Fatalf("unexpected adaptive budgets: hour=%d day=%d", cfg.ProbeMaxPerHourOrDefault(), cfg.ProbeMaxPerDayOrDefault())
	}
}

func TestSubscriptionNodeFailurePolicyNormalization(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"", "skip"},
		{"SKIP", "skip"},
		{" strict ", "strict"},
	} {
		cfg := &Config{SubscriptionRefresh: SubscriptionRefreshConfig{NodeFailurePolicy: test.input}}
		if err := cfg.normalizeSubscriptionSafetyConfig(); err != nil {
			t.Fatalf("normalize %q: %v", test.input, err)
		}
		if got := cfg.SubscriptionNodeFailurePolicyOrDefault(); got != test.want {
			t.Fatalf("policy %q normalized to %q, want %q", test.input, got, test.want)
		}
	}
	invalid := &Config{SubscriptionRefresh: SubscriptionRefreshConfig{NodeFailurePolicy: "best-effort"}}
	if err := invalid.normalizeSubscriptionSafetyConfig(); err == nil {
		t.Fatal("invalid node failure policy was accepted")
	}
}
