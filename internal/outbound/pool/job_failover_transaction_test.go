package pool

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

type transactionalOutbound struct {
	scriptedOutbound
	hook   func()
	closed bool
}
type closeObservedConn struct {
	net.Conn
	owner *transactionalOutbound
}

func (c *closeObservedConn) Close() error { c.owner.closed = true; return c.Conn.Close() }
func (o *transactionalOutbound) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	if o.hook != nil {
		o.hook()
	}
	c, e := o.scriptedOutbound.DialContext(ctx, n, d)
	if e != nil {
		return nil, e
	}
	return &closeObservedConn{c, o}, nil
}

func TestAlternateConnectionCommitsOnlyAfterSuccess(t *testing.T) {
	for _, mode := range []string{"success", "failed", "canceled", "manual", "release", "reacquire", "closed", "paused-runtime", "banned"} {
		t.Run(mode, func(t *testing.T) {
			p, ctx := failoverPool(t, true)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			_, _ = p.AcquireJob("accounts", "browser")
			before, _ := p.jobStore.Lease("accounts", "browser")
			p.memberByTag["a"].outbound = &scriptedOutbound{fail: true}
			alt := &transactionalOutbound{}
			p.memberByTag["b"].outbound = alt
			var winner = before
			alt.hook = func() {
				current, _ := p.jobStore.Lease("accounts", "browser")
				if current.Node != "a" || current.FailoverCount != 0 || current.LastFailover != nil {
					t.Fatal("committed before dial")
				}
				j := p.options.Jobs[1]
				if _, err := p.jobStore.Acquire(j, p.options.JobPolicies[j.Name], "competitor", []jobs.SelectedNode{{Node: "b"}}); !errors.Is(err, jobs.ErrCapacity) {
					t.Fatal("capacity not reserved", err)
				}
				switch mode {
				case "failed":
					alt.fail = true
				case "canceled":
					cancel()
				case "manual":
					var err error
					winner, err = p.jobStore.Migrate(j, before.Policy, jobs.Migration{Session: "browser", Node: "c", ExpectedRevision: before.Version()}, []jobs.SelectedNode{{Node: "c"}})
					if err != nil {
						t.Fatal(err)
					}
				case "release", "reacquire":
					if err := p.jobStore.Release(j.Name, "browser"); err != nil {
						t.Fatal(err)
					}
					if mode == "reacquire" {
						winner, _ = p.jobStore.Acquire(j, before.Policy, "browser", []jobs.SelectedNode{{Node: "c"}})
					}
				case "closed":
					p.closed.Store(true)
					t.Cleanup(func() { p.closed.Store(false) })
				case "paused-runtime":
					resume := p.jobStore.PauseNewAssignments()
					t.Cleanup(resume)
				case "banned":
					p.setMemberEligible(p.memberByTag["b"], false)
				}
			}
			conn, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443"))
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				l, _ := p.jobStore.Lease("accounts", "browser")
				if l.Node != "b" || l.FailoverCount != 1 || l.ExpiresAt != before.ExpiresAt {
					t.Fatal(l)
				}
			} else {
				if err == nil || conn != nil {
					t.Fatal("failed/unauthorized dial escaped")
				}
				if mode != "failed" && !alt.closed {
					t.Fatal("successful but rejected socket leaked")
				}
				after, ok := p.jobStore.Lease("accounts", "browser")
				if mode == "release" {
					if ok {
						t.Fatal("released pin recreated")
					}
				} else {
					after.LastUsedAt, winner.LastUsedAt = before.LastUsedAt, before.LastUsedAt
					if !reflect.DeepEqual(after, winner) {
						t.Fatal("failed alternate changed identity", after)
					}
				}
			}
		})
	}
}

func TestAcquireOffersRecoveryWithoutMigrating(t *testing.T) {
	p, _ := failoverPool(t, true)
	first, _ := p.AcquireJob("accounts", "browser")
	p.setMemberEligible(p.memberByTag[first.Node], false)
	for range 3 {
		a, err := p.AcquireJob("accounts", "browser")
		if err != nil || a.State != "paused" || !a.FailoverAvailable || a.ProxyURL != first.ProxyURL || a.Revision != first.Revision || a.FailoverCount != 0 {
			t.Fatal(a, err)
		}
	}
	p.options.Jobs[1].FailurePolicy = "strict"
	a, _ := p.AcquireJob("accounts", "browser")
	if a.FailoverAvailable || a.ProxyURL != "" {
		t.Fatal("strict offers recovery")
	}
}

func TestUnavailablePinFailedAlternateKeepsOriginal(t *testing.T) {
	for _, shared := range []bool{false, true} {
		p, ctx := failoverPool(t, shared)
		first, _ := p.AcquireJob("accounts", "browser")
		p.setMemberEligible(p.memberByTag[first.Node], false)
		b := &scriptedOutbound{fail: true}
		c := &scriptedOutbound{}
		p.memberByTag["b"].outbound = b
		p.memberByTag["c"].outbound = c
		if conn, err := p.DialContext(ctx, "tcp", M.ParseSocksaddr("example.test:443")); err == nil {
			conn.Close()
			t.Fatal("expected failure")
		}
		after, _ := p.jobStore.Lease("accounts", "browser")
		if after.Node != first.Node || after.Version() != first.Revision || after.LastFailover != nil || after.FailoverCount != 0 || b.calls.Load() != 1 || c.calls.Load() != 0 {
			t.Fatal("premature migration or retry", after)
		}
	}
}
