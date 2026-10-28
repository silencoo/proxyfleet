package pool

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func failoverPool(t *testing.T, shared bool) (*poolOutbound, context.Context) {
	p := jobPoolFixture(t)
	j := &p.options.Jobs[1]
	j.Selection = "manual"
	j.FailurePolicy = "failover"
	j.Endpoint = "accounts"
	j.SessionTTL = "48h"
	j.MaxAccountsPerNode = 1
	p.options.JobEndpoints[j.Name] = config.EndpointConfig{Address: "127.0.0.1", Port: 2325, Username: "worker", Password: "secret"}
	md := &adapter.InboundContext{Inbound: config.EndpointInboundTag(j.Name), User: "worker-session-browser"}
	if shared {
		j.Ingress = "shared"
		j.Endpoint = ""
		j.Password = "application-secret"
		md.Inbound = config.SharedJobInboundTag(j.Name)
		md.User = "browser"
	}
	for _, m := range p.members {
		m.outbound = &scriptedOutbound{}
	}
	return p, adapter.WithContext(context.Background(), md)
}

func TestApplicationFailoverRecoversSameSessionForBothIngresses(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "endpoint", true: "shared"}[shared], func(t *testing.T) {
			p, ctx := failoverPool(t, shared)
			first, err := p.AcquireJob("accounts", "browser")
			if err != nil {
				t.Fatal(err)
			}
			p.setMemberEligible(p.memberByTag[first.Node], false)
			before, _ := p.jobStore.Lease("accounts", "browser")
			listed := p.ListJobSessions()
			after, _ := p.jobStore.Lease("accounts", "browser")
			if len(listed) != 1 || listed[0].State != "paused" || !reflect.DeepEqual(before, after) {
				t.Fatal("listing migrated binding")
			}
			var wg sync.WaitGroup
			for range 24 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					m, e := dialTestJob(t, p, ctx)
					if e != nil && !errors.Is(e, jobs.ErrConflict) || e == nil && m != "b" {
						t.Errorf("concurrent recovery: %v %v", m, e)
					}
				}()
			}
			wg.Wait()
			a, err := p.AcquireJob("accounts", "browser")
			if err != nil || a.Node != "b" || a.ProxyURL != first.ProxyURL || a.FailoverCount != 1 || a.LastFailover.Reason != "node_unavailable" || !a.ExpiresAt.Equal(first.ExpiresAt) {
				t.Fatal(a, err)
			}
			p.setMemberEligible(p.memberByTag[first.Node], true)
			again, _ := p.AcquireJob("accounts", "browser")
			if again.Node != "b" {
				t.Fatal("reverted to old node")
			}
			if p.ListJobSessions()[0].LastFailover == nil {
				t.Fatal("audit missing from list")
			}
		})
	}
}

func TestApplicationFailoverMissingNodeAndScope(t *testing.T) {
	p, ctx := failoverPool(t, false)
	_, _ = p.AcquireJob("accounts", "browser")
	delete(p.memberByTag, "a")
	p.profiles["fast"].allowed = map[string]struct{}{"b": {}}
	m, err := dialTestJob(t, p, ctx)
	if err != nil || m != "b" {
		t.Fatal(m, err)
	}
	l, _ := p.jobStore.Lease("accounts", "browser")
	if l.LastFailover.Reason != "node_missing" {
		t.Fatal(l)
	}
	p.setMemberEligible(p.memberByTag["b"], false)
	if _, err := dialTestJob(t, p, ctx); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("escaped cooldown/profile", err)
	}
}

func TestApplicationFailoverRechecksTransportHealthAfterBlacklistExpiry(t *testing.T) {
	for _, tc := range []struct {
		name, b, c, want string
		full             bool
	}{
		{"expired blacklist is not recovery", "failed", "healthy", "c", false},
		{"prefer confirmed over unknown", "unknown", "healthy", "c", false},
		{"no confirmed failures as fallback", "failed", "failed", "", false},
		{"cold start can try unknown", "unknown", "failed", "b", false},
		{"never bypass confirmed capacity with unknown", "unknown", "healthy", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ctx := failoverPool(t, false)
			if _, err := p.AcquireJob("accounts", "browser"); err != nil {
				t.Fatal(err)
			}
			before, _ := p.jobStore.Lease("accounts", "browser")
			mgr, err := monitor.NewManager(monitor.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mgr.Stop)
			for tag, health := range map[string]string{"b": tc.b, "c": tc.c} {
				member := p.memberByTag[tag]
				member.entry = mgr.Register(monitor.NodeInfo{Tag: tag})
				member.entry.RestoreHealthState(monitor.PersistedHealthState{
					InitialCheckDone: health != "unknown", Available: health == "healthy",
				})
				// Reproduce an expired blacklist returning a still-failed node
				// to the generic retry index.
				p.setMemberEligible(member, true)
			}
			if tc.full {
				j := p.options.Jobs[1]
				if _, err := p.jobStore.Acquire(j, p.options.JobPolicies[j.Name], "occupied", []jobs.SelectedNode{{Node: "c"}}); err != nil {
					t.Fatal(err)
				}
			}
			p.setMemberEligible(p.memberByTag["a"], false)
			member, err := dialTestJob(t, p, ctx)
			if tc.want == "" {
				if err == nil {
					t.Fatal("unsafe failover was allowed")
				}
				after, _ := p.jobStore.Lease("accounts", "browser")
				before.LastUsedAt, after.LastUsedAt = time.Time{}, time.Time{}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("failed recovery changed the binding")
				}
			} else if err != nil || member != tc.want {
				t.Fatalf("recovered to %v, error %v; want %s", member, err, tc.want)
			}
		})
	}
}

func TestApplicationFailoverDoesNotHandleTargetChallengesExpiryOrPolicyChanges(t *testing.T) {
	for _, tc := range []string{"target", "expired", "policy", "cold-start", "full", "empty", "strict"} {
		t.Run(tc, func(t *testing.T) {
			p, ctx := failoverPool(t, false)
			j := &p.options.Jobs[1]
			if tc == "expired" {
				j.SessionTTL = "-1s"
			}
			_, _ = p.AcquireJob("accounts", "browser")
			before, _ := p.jobStore.Lease("accounts", "browser")
			switch tc {
			case "target":
				j.Selection = "auto"
				p.jobStore.Record("accounts-policy", jobs.Measurement{Node: "a", Success: false, StatusCode: 403})
			case "policy":
				p.options.JobPolicies[j.Name] = "changed"
				p.setMemberEligible(p.memberByTag["a"], false)
			case "cold-start":
				mgr, err := monitor.NewManager(monitor.Config{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(mgr.Stop)
				p.memberByTag["a"].entry = mgr.Register(monitor.NodeInfo{Tag: "a"})
				p.setMemberEligible(p.memberByTag["a"], false)
			case "full":
				_, _ = p.AcquireJob("accounts", "other-b")
				_, _ = p.AcquireJob("accounts", "other-c")
				p.setMemberEligible(p.memberByTag["a"], false)
			case "empty":
				for _, m := range p.members {
					p.setMemberEligible(m, false)
				}
			case "strict":
				j.FailurePolicy = "strict"
				p.setMemberEligible(p.memberByTag["a"], false)
			}
			if _, err := dialTestJob(t, p, ctx); err == nil {
				t.Fatal("unexpected usable binding")
			}
			after, _ := p.jobStore.Lease("accounts", "browser")
			before.LastUsedAt, after.LastUsedAt = time.Time{}, time.Time{}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unsafe automatic rebind", after)
			}
		})
	}
}

func TestApplicationFailoverRetriesOnlyConnectionEstablishment(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "failover", true: "strict"}[strict], func(t *testing.T) {
			p, ctx := failoverPool(t, false)
			if strict {
				p.options.Jobs[1].FailurePolicy = "strict"
			}
			first, second, third := &scriptedOutbound{fail: true}, &scriptedOutbound{}, &scriptedOutbound{}
			p.memberByTag["a"].outbound = first
			p.memberByTag["b"].outbound = second
			p.memberByTag["c"].outbound = third
			conn, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443"))
			if strict {
				if err == nil || conn != nil || second.calls.Load() != 0 {
					t.Fatal("strict retried")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows := p.ListJobSessions()
			if len(rows) != 1 || rows[0].ActiveConnections != 1 {
				t.Fatal("retry did not protect new binding", rows)
			}
			_ = conn.Close()
			if rows = p.ListJobSessions(); rows[0].ActiveConnections != 0 {
				t.Fatal("retry leaked protection", rows)
			}
			if first.calls.Load() != 1 || second.calls.Load() != 1 || third.calls.Load() != 0 {
				t.Fatal("incorrect retry bound")
			}
			lease, _ := p.jobStore.Lease("accounts", "browser")
			if lease.Node != "b" || lease.LastFailover.Reason != "dial_failed" || lease.FailoverCount != 1 {
				t.Fatal(lease)
			}
		})
	}
}

func TestApplicationFailoverStopsAfterOneAlternate(t *testing.T) {
	p, ctx := failoverPool(t, false)
	for _, m := range p.members {
		m.outbound = &scriptedOutbound{fail: true}
	}
	if _, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443")); err == nil {
		t.Fatal("expected failure")
	}
	if p.memberByTag["c"].outbound.(*scriptedOutbound).calls.Load() != 0 {
		t.Fatal("unbounded retry")
	}
	l, _ := p.jobStore.Lease("accounts", "browser")
	if l.FailoverCount != 0 || l.Node != "a" || l.LastFailover != nil {
		t.Fatal(l)
	}
}

func TestApplicationFailoverIgnoresCancellationAndLocalResources(t *testing.T) {
	for _, tc := range []string{"canceled", "local-resource"} {
		t.Run(tc, func(t *testing.T) {
			p, ctx := failoverPool(t, false)
			_, _ = p.AcquireJob("accounts", "browser")
			before, _ := p.jobStore.Lease("accounts", "browser")
			p.resourceBackoff = new(resourceBackoff)
			second := &scriptedOutbound{}
			p.memberByTag["b"].outbound = second
			if tc == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				p.memberByTag["a"].outbound = &scriptedOutbound{fail: true}
			} else {
				p.memberByTag["a"].outbound = &resourceFailureOutbound{}
			}
			if _, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443")); err == nil {
				t.Fatal("expected failed dial")
			}
			after, _ := p.jobStore.Lease("accounts", "browser")
			before.LastUsedAt, after.LastUsedAt = time.Time{}, time.Time{}
			if !reflect.DeepEqual(before, after) || second.calls.Load() != 0 {
				t.Fatal("local failure migrated binding")
			}
		})
	}
}

func TestApplicationFailoverCannotEscapeClosedOrUncommittedRuntime(t *testing.T) {
	for _, tc := range []string{"closed", "uncommitted"} {
		t.Run(tc, func(t *testing.T) {
			p, ctx := failoverPool(t, false)
			_, _ = p.AcquireJob("accounts", "browser")
			before, _ := p.jobStore.Lease("accounts", "browser")
			p.setMemberEligible(p.memberByTag["a"], false)
			if tc == "closed" {
				p.closed.Store(true)
				defer p.closed.Store(false)
			} else {
				defer p.jobStore.PauseNewAssignments()()
			}
			if _, err := dialTestJob(t, p, ctx); err == nil {
				t.Fatal("recovery escaped runtime fence")
			}
			after, _ := p.jobStore.Lease("accounts", "browser")
			before.LastUsedAt, after.LastUsedAt = time.Time{}, time.Time{}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("runtime fence altered binding")
			}
		})
	}
}

func dialTestJob(t *testing.T, p *poolOutbound, ctx context.Context) (string, error) {
	t.Helper()
	conn, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	l, _ := p.jobStore.Lease("accounts", "browser")
	return l.Node, nil
}
