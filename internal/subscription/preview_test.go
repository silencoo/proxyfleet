package subscription

import (
	"errors"
	"strings"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

func TestSubscriptionPreviewReportsStableDiffAndRequiresRiskConfirmation(t *testing.T) {
	current := []config.NodeConfig{
		{Name: "keep", URI: "vless://one@example.test:443", Source: config.NodeSourceSubscription},
		{Name: "remove-a", URI: "vless://two@example.test:443", Source: config.NodeSourceSubscription},
		{Name: "remove-b", URI: "vless://three@example.test:443", Source: config.NodeSourceSubscription},
	}
	candidate := []config.NodeConfig{
		{Name: "keep-renamed", URI: "vless://one@example.test:443"},
		{Name: "added", URI: "vless://four@example.test:443"},
	}
	cfg := &config.Config{SubscriptionRefresh: config.SubscriptionRefreshConfig{MaxRemovedRatio: 0.5}}
	preview, err := newSubscriptionPreview(current, candidate, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Added != 1 || preview.Removed != 2 || preview.Unchanged != 1 || !preview.Risky || !preview.RequiresConfirmation {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if preview.Token == "" || preview.ExpiresAt.IsZero() {
		t.Fatal("preview token or expiry missing")
	}
	for _, name := range append(preview.AddedNames, preview.RemovedNames...) {
		if strings.Contains(name, "://") {
			t.Fatalf("preview exposed node URI: %q", name)
		}
	}
}

func TestAutomaticSubscriptionRemovalGuardLeavesLegacyZeroThresholdDisabled(t *testing.T) {
	current := []config.NodeConfig{{Name: "old", URI: "vless://old@example.test:443", Source: config.NodeSourceSubscription}}
	legacy := &config.Config{Nodes: current}
	if err := validateAutomaticSubscriptionChange(legacy, nil); err != nil {
		t.Fatalf("zero threshold should preserve legacy caller behavior: %v", err)
	}
	guarded := legacy.Clone()
	guarded.SubscriptionRefresh.MaxRemovedRatio = 0.5
	if err := validateAutomaticSubscriptionChange(guarded, nil); !errors.Is(err, monitor.ErrSubscriptionChangeGuard) {
		t.Fatalf("guard error=%v, want ErrSubscriptionChangeGuard", err)
	}
}
