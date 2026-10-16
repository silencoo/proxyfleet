package pool

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestAutomaticRecoveryPreservesProtectedBlocks(t *testing.T) {
	for _, block := range []string{"manual", "cooldown", "automatic-and-cooldown"} {
		for _, recovery := range []string{"probe", "traffic", "fail-open", "validated-probe"} {
			t.Run(block+"/"+recovery, func(t *testing.T) {
				p, member, mgr := newTrafficHealthFixture(t)
				p.monitor = mgr
				if err := mgr.SetProbeTarget("http://example.test/check", false); err != nil {
					t.Fatal(err)
				}
				p.options.FailOpen = true
				member.outbound = &probeTransportOutbound{}
				p.registerProbe(member)
				member.entry.MarkInitialCheckDone(true)
				s := member.shared
				if block == "manual" {
					blacklistSharedMember(member.tag, time.Hour)
				} else {
					if block == "automatic-and-cooldown" {
						s.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
					}
					s.recordFailure(context.DeadlineExceeded, 1, time.Hour, time.Hour)
				}
				before := mgr.Snapshot()[0]
				switch recovery {
				case "probe":
					s.releaseAfterProbe()
				case "traffic":
					s.recordSuccessWithLatency(time.Millisecond)
				case "fail-open":
					if p.releaseIfAllBlacklisted() {
						t.Error("fail_open claimed a protected node was released")
					}
				case "validated-probe":
					target, _ := mgr.DestinationForProbe()
					result := ValidatedProbe{Tag: member.tag, Outbound: member.outbound, Target: target, StartedAt: time.Now(), Latency: time.Millisecond}
					result.CompletedAt = time.Now()
					if ApplyValidatedProbes(p, []ValidatedProbe{result}) != 1 {
						t.Fatal("fresh candidate measurement was not accepted")
					}
				}
				after := mgr.Snapshot()[0]
				if !s.isBlocked(time.Now()) || !s.blacklistedFast.Load() || p.selectEligibleMember("tcp") != nil || after.Available {
					t.Fatal("automatic recovery made a protected node routable")
				}
				if block == "manual" {
					if !after.Blacklisted || !after.BlacklistedUntil.Equal(before.BlacklistedUntil) {
						t.Fatal("manual ban changed")
					}
				} else if !after.CoolingDown || !after.CooldownUntil.Equal(before.CooldownUntil) {
					t.Fatal("active cooldown changed")
				}
				// Only explicit release may clear these live protections.
				s.forceRelease()
				if s.isBlocked(time.Now()) || p.selectEligibleMember("tcp") != member {
					t.Fatal("explicit release failed to restore retry eligibility")
				}
			})
		}
	}
}

func TestPermanentAccountStaysPausedOnProtectedNode(t *testing.T) {
	for _, block := range []string{"manual", "cooldown"} {
		t.Run(block, func(t *testing.T) {
			p := jobPoolFixture(t)
			p.options.FailOpen = true
			p.options.Jobs[1].SessionTTL = "0"
			original, err := p.AcquireJob("accounts", "account-1")
			if err != nil || !original.Permanent || original.State != "ready" {
				t.Fatalf("initial permanent binding: %+v %v", original, err)
			}
			for _, member := range p.members {
				if block == "manual" {
					blacklistSharedMember(member.tag, time.Hour)
				} else {
					member.shared.recordFailure(context.DeadlineExceeded, 1, time.Hour, time.Hour)
				}
			}
			p.releaseIfAllBlacklisted()
			member := p.memberByTag[original.Node]
			member.shared.releaseAfterProbe()
			member.shared.recordSuccessWithLatency(time.Millisecond)
			paused, err := p.AcquireJob("accounts", "account-1")
			if err != nil || paused.State != "paused" || paused.Node != original.Node || paused.Revision != original.Revision || !paused.Permanent {
				t.Fatalf("protected binding changed or resumed: %+v %v", paused, err)
			}
			member.shared.forceRelease()
			ready, err := p.AcquireJob("accounts", "account-1")
			if err != nil || ready.State != "ready" || ready.Node != original.Node || ready.Revision != original.Revision {
				t.Fatalf("explicit release did not retain the same binding: %+v %v", ready, err)
			}
		})
	}
}

func TestProtectedBlocksSurviveRecoveryAndSQLiteRestart(t *testing.T) {
	for _, block := range []string{"manual", "cooldown"} {
		t.Run(block, func(t *testing.T) {
			ResetSharedStateStore()
			resetHealthPersistenceForTest()
			t.Cleanup(func() {
				ResetSharedStateStore()
				_ = CloseRuntimeState()
				resetHealthPersistenceForTest()
			})
			path := filepath.Join(t.TempDir(), "runtime-state.db")
			if err := ConfigureRuntimeState(path, ""); err != nil {
				t.Fatal(err)
			}
			s := acquireSharedState("protected-restart")
			if block == "manual" {
				blacklistSharedMember(s.tag, time.Hour)
			} else {
				s.recordFailure(context.DeadlineExceeded, 1, time.Hour, time.Hour)
			}
			s.releaseAfterProbe()
			s.recordSuccess()
			s.mu.Lock()
			ban, cooldown := s.blacklistedUntil, s.cooldownUntil
			s.mu.Unlock()
			ResetSharedStateStore()
			if err := CloseRuntimeState(); err != nil {
				t.Fatal(err)
			}
			resetHealthPersistenceForTest()
			if err := ConfigureRuntimeState(path, ""); err != nil {
				t.Fatal(err)
			}
			restored := acquireSharedState(s.tag)
			if !restored.isBlocked(time.Now()) || restored.releaseAutomaticBlacklist() {
				t.Fatal("protected state was lost or released after restart")
			}
			restored.mu.Lock()
			defer restored.mu.Unlock()
			if !restored.blacklistedUntil.Equal(ban) || !restored.cooldownUntil.Equal(cooldown) || restored.manualBlacklist != (block == "manual") {
				t.Fatal("restart changed protected deadlines or ban origin")
			}
		})
	}
}

func TestFailOpenMixedPoolKeepsProtectedNodes(t *testing.T) {
	p, first, mgr := newTrafficHealthFixture(t)
	p.options.FailOpen = true
	first.shared.recordProbeFailure(errors.New("invalid protocol"), time.Hour, time.Minute)
	for _, tag := range []string{"manual", "cooldown"} {
		s := acquireSharedState(tag)
		h := mgr.Register(monitor.NodeInfo{Tag: tag})
		s.attachEntry(h)
		member := &memberState{tag: tag, shared: s, entry: h}
		p.members = append(p.members, member)
		p.memberByTag[tag] = member
		member.unwatch = s.subscribeBlacklist(func(blocked bool) { p.setMemberEligible(member, !blocked) })
		if tag == "manual" {
			blacklistSharedMember(tag, time.Hour)
		} else {
			s.recordFailure(context.DeadlineExceeded, 1, time.Hour, time.Hour)
		}
	}
	if !p.releaseIfAllBlacklisted() {
		t.Fatal("explicit fail_open did not release the automatic ban")
	}
	for i := 0; i < 20; i++ {
		if p.selectEligibleMember("tcp") != first {
			t.Fatal("fail_open admitted a protected member in a mixed pool")
		}
	}
	for _, tag := range []string{"manual", "cooldown"} {
		if !p.memberByTag[tag].shared.isBlocked(time.Now()) {
			t.Fatalf("fail_open released %s", tag)
		}
	}
}

func TestAutomaticBanCannotEraseAnActiveCooldown(t *testing.T) {
	p, member, mgr := newTrafficHealthFixture(t)
	s := member.shared
	deadline := s.recordFailure(context.DeadlineExceeded, 1, time.Hour, time.Hour).Until
	s.recordProbeFailure(errors.New("invalid protocol"), 24*time.Hour, time.Minute)
	s.releaseAfterProbe()
	after := mgr.Snapshot()[0]
	if !s.isBlocked(time.Now()) || p.selectEligibleMember("tcp") != nil || !after.CoolingDown || !after.CooldownUntil.Equal(deadline) {
		t.Fatal("an automatic blacklist followed by a healthy probe bypassed the original cooldown")
	}
}

func TestProbeStartedBeforeAdministrativeChangeIsRejected(t *testing.T) {
	for _, operation := range []string{"ban", "release"} {
		for _, outcome := range []string{"success", "failure"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				p, member, mgr := newTrafficHealthFixture(t)
				p.monitor = mgr
				if err := mgr.SetProbeTarget("http://example.test/check", false); err != nil {
					t.Fatal(err)
				}
				started, finish := make(chan struct{}), make(chan struct{})
				var once sync.Once
				t.Cleanup(func() { once.Do(func() { close(finish) }) })
				member.outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) {
					close(started)
					select {
					case <-finish:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if outcome == "failure" {
						return nil, errors.New("obsolete protocol failure")
					}
					client, peer := net.Pipe()
					go func() {
						defer peer.Close()
						if _, err := http.ReadRequest(bufio.NewReader(peer)); err == nil {
							_, _ = io.WriteString(peer, "HTTP/1.1 204 No Content\r\n\r\n")
						}
					}()
					return client, nil
				}}
				p.registerProbe(member)
				member.entry.SetRelease(p.makeReleaseFunc(member))
				member.entry.SetBlacklistFn(func(d time.Duration) { blacklistSharedMember(member.tag, d) })
				if operation == "release" {
					member.shared.recordProbeFailure(errors.New("previous failure"), time.Hour, time.Minute)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := mgr.Probe(ctx, member.tag); done <- err }()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("probe did not start")
				}
				var err error
				if operation == "ban" {
					err = mgr.ManualBlacklist(member.tag, time.Hour)
				} else {
					err = mgr.Release(member.tag)
				}
				if err != nil {
					t.Fatal(err)
				}
				before := mgr.Snapshot()[0]
				once.Do(func() { close(finish) })
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), "replaced") {
						t.Errorf("old probe was not rejected: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("probe did not finish")
				}
				after := mgr.Snapshot()[0]
				if after.Blacklisted != before.Blacklisted || !after.BlacklistedUntil.Equal(before.BlacklistedUntil) || after.LastError != before.LastError || !after.LastProbeAt.Equal(before.LastProbeAt) || after.Available != before.Available {
					t.Fatal("old probe overwrote an administrator's newer state")
				}
				if member.shared.isBlocked(time.Now()) != (operation == "ban") {
					t.Fatal("old probe changed routing after administrative action")
				}
			})
		}
	}
}

func TestValidatedProbeBeforeAdministrativeChangeIsRejected(t *testing.T) {
	for _, operation := range []string{"ban", "release"} {
		t.Run(operation, func(t *testing.T) {
			p, member, mgr := newTrafficHealthFixture(t)
			p.monitor = mgr
			if err := mgr.SetProbeTarget("http://example.test/check", false); err != nil {
				t.Fatal(err)
			}
			member.outbound = &probeTransportOutbound{}
			p.registerProbe(member)
			target, _ := mgr.DestinationForProbe()
			result := ValidatedProbe{Tag: member.tag, Outbound: member.outbound, Target: target, StartedAt: time.Now(), Err: errors.New("old failure")}
			if operation == "ban" {
				blacklistSharedMember(member.tag, time.Hour)
			} else {
				member.shared.forceRelease()
			}
			result.CompletedAt = time.Now()
			if ApplyValidatedProbes(p, []ValidatedProbe{result}) != 0 {
				t.Fatal("old candidate probe overrode an administrator's newer state")
			}
			// The barrier rejects only old observations, not future checks.
			result.StartedAt, result.Err = time.Now(), nil
			result.CompletedAt = time.Now()
			if ApplyValidatedProbes(p, []ValidatedProbe{result}) != 1 {
				t.Fatal("administrative change permanently disabled fresh probes")
			}
		})
	}
}
