package pool

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestTransientFailureDoesNotRenewBlacklist(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, traffic := range []bool{false, true} {
			name := "automatic/probe"
			if manual {
				name = "manual/probe"
			}
			if traffic {
				name += "/traffic"
			}
			t.Run(name, func(t *testing.T) {
				ResetSharedStateStore()
				t.Cleanup(ResetSharedStateStore)
				manager, err := monitor.NewManager(monitor.Config{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(manager.Stop)
				state := acquireSharedState("existing-ban")
				handle := manager.Register(monitor.NodeInfo{Tag: state.tag})
				state.attachEntry(handle)
				member := &memberState{tag: state.tag, shared: state, entry: handle}
				proxyPool := newIndexedTestPool(member, Options{})
				t.Cleanup(func() { _ = proxyPool.Close() })
				if manual {
					blacklistSharedMember(state.tag, time.Hour)
				} else {
					state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
				}
				state.mu.Lock()
				originalUntil, originalTimer, originalStrikes := state.blacklistedUntil, state.blacklistTimer, state.failures
				state.mu.Unlock()
				for attempt := 0; attempt < 3; attempt++ {
					if traffic {
						state.recordFailure(context.DeadlineExceeded, 1, 24*time.Hour, time.Minute)
					} else {
						state.recordProbeFailure(context.DeadlineExceeded, 24*time.Hour, time.Minute)
					}
				}
				state.mu.Lock()
				until, timer, strikes, manualAfter := state.blacklistedUntil, state.blacklistTimer, state.failures, state.manualBlacklist
				state.mu.Unlock()
				if !until.Equal(originalUntil) || timer != originalTimer || strikes != originalStrikes || manualAfter != manual {
					t.Fatalf("timeouts changed the existing ban: until=%v (was %v), timerChanged=%t, strikes=%d (was %d), manual=%t", until, originalUntil, timer != originalTimer, strikes, originalStrikes, manualAfter)
				}
				if proxyPool.selectEligibleMember("") != nil {
					t.Fatal("timeout prematurely released a blacklisted node")
				}
				snapshot := manager.Snapshot()[0]
				wantFailures := 0
				if traffic {
					wantFailures = 3
				}
				if snapshot.FailureCount != wantFailures || !snapshot.Blacklisted || !snapshot.BlacklistedUntil.Equal(originalUntil) {
					t.Fatalf("incorrect monitor state: %+v", snapshot)
				}
			})
		}
	}
}

func TestRemoteTimeoutDoesNotCreateOrRenewDurableBan(t *testing.T) {
	for _, existingBan := range []bool{false, true} {
		ResetSharedStateStore()
		t.Cleanup(ResetSharedStateStore)
		state := acquireSharedState("remote-timeout")
		var originalUntil time.Time
		if existingBan {
			originalUntil = state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute).Until
		}
		for attempt := 0; attempt < 5; attempt++ {
			decision := state.recordProbeFailure(errors.New("read HTTP response: remote error: dial tcp 192.0.2.1:80: i/o timeout"), 24*time.Hour, time.Minute)
			if !decision.Cooldown || decision.Blacklisted || decision.Failures != 0 {
				t.Fatalf("remote timeout caused a permanent-failure strike: %+v", decision)
			}
		}
		state.mu.Lock()
		until, blacklisted := state.blacklistedUntil, state.blacklisted
		state.mu.Unlock()
		if !until.Equal(originalUntil) || blacklisted != existingBan {
			t.Fatalf("remote timeout created/renewed a ban: %v (was %v)", until, originalUntil)
		}
	}
}

func TestTimeoutAfterUnprocessedBlacklistExpiryUsesOnlyCooldown(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	manager, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	state := acquireSharedState("expired-before-timeout")
	state.attachEntry(manager.Register(monitor.NodeInfo{Tag: state.tag}))
	state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
	state.mu.Lock()
	state.blacklistTimer.Stop()
	state.blacklistTimer = nil
	state.blacklistedUntil = time.Now().Add(-time.Second)
	state.mu.Unlock()

	decision := state.recordProbeFailure(context.DeadlineExceeded, 24*time.Hour, time.Minute)
	if !decision.Cooldown || decision.Blacklisted {
		t.Fatalf("expired ban was renewed by a timeout: %+v", decision)
	}
	snapshot := manager.Snapshot()[0]
	if snapshot.Blacklisted || !snapshot.CoolingDown || snapshot.Available {
		t.Fatalf("expired ban survived in monitor state: %+v", snapshot)
	}
}

func TestAutomaticBlacklistExpiryPreservesLaterCooldown(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	manager, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	state := acquireSharedState("overlapping-expiry")
	handle := manager.Register(monitor.NodeInfo{Tag: state.tag})
	state.attachEntry(handle)
	member := &memberState{tag: state.tag, shared: state, entry: handle}
	proxyPool := newIndexedTestPool(member, Options{})
	t.Cleanup(func() { _ = proxyPool.Close() })
	state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
	state.recordProbeFailure(context.DeadlineExceeded, 24*time.Hour, time.Minute)

	expired := time.Now().Add(-time.Second)
	state.mu.Lock()
	state.blacklistTimer.Stop()
	state.blacklistTimer = nil
	state.blacklistedUntil = expired
	state.mu.Unlock()
	state.expireBlacklist(expired)
	if proxyPool.selectEligibleMember("") != nil {
		t.Fatal("blacklist expiry bypassed the newer cooldown")
	}
	snapshot := manager.Snapshot()[0]
	if snapshot.Blacklisted || !snapshot.CoolingDown || snapshot.Available {
		t.Fatalf("incorrect overlapping-expiry state: %+v", snapshot)
	}
	state.mu.Lock()
	state.cooldownTimer.Stop()
	state.cooldownTimer = nil
	state.cooldownUntil = expired
	state.mu.Unlock()
	state.expireCooldown(expired)
	if proxyPool.selectEligibleMember("") != member || manager.Snapshot()[0].Available {
		t.Fatal("final cooldown expiry failed to restore retry eligibility without claiming recovery")
	}
}

func TestTransientFailurePreservesBlacklistDeadlineAcrossRestart(t *testing.T) {
	ResetSharedStateStore()
	resetHealthPersistenceForTest()
	t.Cleanup(func() {
		ResetSharedStateStore()
		resetHealthPersistenceForTest()
	})
	path := filepath.Join(t.TempDir(), "health-state.yaml")
	if err := ConfigureHealthPersistence(path); err != nil {
		t.Fatal(err)
	}
	state := acquireSharedState("persisted-ban")
	original := state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
	state.recordProbeFailure(context.DeadlineExceeded, 24*time.Hour, 2*time.Hour)
	if err := PersistHealthStateNow(); err != nil {
		t.Fatal(err)
	}
	ResetSharedStateStore()
	resetHealthPersistenceForTest()
	if err := ConfigureHealthPersistence(path); err != nil {
		t.Fatal(err)
	}
	restored := acquireSharedState("persisted-ban")
	restored.mu.Lock()
	until, cooldown, strikes := restored.blacklistedUntil, restored.cooldownUntil, restored.failures
	restored.mu.Unlock()
	if !until.Equal(original.Until) || !cooldown.After(until) || strikes != 0 {
		t.Fatalf("restored timeout extended/lost the original policy: until=%v, cooldown=%v, strikes=%d", until, cooldown, strikes)
	}
}

func TestPermanentBanReplacesCooldownInSharedAndMonitorState(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	manager, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	state := acquireSharedState("cooldown-to-ban")
	state.attachEntry(manager.Register(monitor.NodeInfo{Tag: state.tag}))
	state.recordProbeFailure(context.DeadlineExceeded, time.Hour, time.Minute)
	if !manager.Snapshot()[0].CoolingDown {
		t.Fatal("initial timeout did not establish cooldown")
	}
	state.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
	state.mu.Lock()
	cooldown, timer := state.cooldownUntil, state.cooldownTimer
	state.mu.Unlock()
	snapshot := manager.Snapshot()[0]
	if !cooldown.IsZero() || timer != nil || snapshot.CoolingDown || !snapshot.CooldownUntil.IsZero() || !snapshot.Blacklisted {
		t.Fatalf("replaced cooldown was left behind: shared=%v, timer=%v, monitor=%+v", cooldown, timer, snapshot)
	}
}

func TestFailureDecisionSummaryUsesEffectiveDeadlines(t *testing.T) {
	ban := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	cooldown := ban.Add(time.Minute)
	for _, test := range []struct {
		decision failureDecision
		want     string
	}{
		{failureDecision{Blacklisted: true, Until: ban, ExistingBlacklistUntil: ban}, "existing blacklist unchanged until 2026-01-02T12:00:00Z"},
		{failureDecision{Cooldown: true, Until: cooldown, ExistingBlacklistUntil: ban}, "existing blacklist unchanged until 2026-01-02T12:00:00Z; cooldown until 2026-01-02T12:01:00Z"},
		{failureDecision{Cooldown: true, Until: cooldown}, "cooling down until 2026-01-02T12:01:00Z"},
		{failureDecision{Blacklisted: true, Until: ban}, "blacklisted until 2026-01-02T12:00:00Z"},
		{failureDecision{Failures: 2}, "failure 2/3"},
	} {
		if got := failureDecisionSummary(test.decision, 3); got != test.want || strings.Contains(got, "24h") {
			t.Fatalf("decision summary=%q, want %q", got, test.want)
		}
	}
}
