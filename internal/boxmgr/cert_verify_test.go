package boxmgr

import (
	"context"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestCertificateVerificationModeRebuildsRuntime(t *testing.T) {
	cfg := newDrainTestConfig(t, 10*time.Millisecond)
	manager := New(cfg, monitor.Config{Enabled: false})
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.mu.RLock()
	original := manager.currentBox
	manager.mu.RUnlock()
	candidate := cfg.Clone()
	candidate.SkipCertVerifyMode = config.CertVerifyOverride
	if canReloadNodesInPlace(cfg, candidate) {
		t.Fatal("mode change would reuse old outbound TLS settings")
	}
	if err := normalizeDrainAndReload(t, manager, candidate); err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	current := manager.currentBox
	manager.mu.RUnlock()
	if original == current {
		t.Fatal("mode change did not replace runtime")
	}
}
