package pool

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/silencoo/proxyfleet/internal/jobs"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func assignmentHealthPool(t *testing.T, selection string, shared bool) *poolOutbound {
	t.Helper()
	p, _ := failoverPool(t, shared)
	j := &p.options.Jobs[1]
	j.Selection, j.FailurePolicy, j.SessionTTL, j.Size = selection, "strict", "0", 3
	mgr, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	for _, member := range p.members {
		member.entry = mgr.Register(monitor.NodeInfo{Tag: member.tag})
	}
	return p
}

func setAssignmentHealth(p *poolOutbound, tag, state string) {
	member := p.memberByTag[tag]
	member.entry.RestoreHealthState(monitor.PersistedHealthState{
		InitialCheckDone: state != "unknown", Available: state == "healthy",
	})
	// An expired blacklist/cooldown permits a transport retry, even though
	// the most recent completed health check may still have failed.
	p.setMemberEligible(member, true)
}

func TestNewPinnedAssignmentsRejectKnownFailedRetryCandidates(t *testing.T) {
	for _, selection := range []string{"manual", "auto"} {
		for _, shared := range []bool{false, true} {
			ingress := map[bool]string{false: "endpoint", true: "shared"}[shared]
			t.Run(selection+"/"+ingress, func(t *testing.T) {
				p := assignmentHealthPool(t, selection, shared)
				setAssignmentHealth(p, "a", "failed")
				setAssignmentHealth(p, "b", "healthy")
				setAssignmentHealth(p, "c", "failed")
				if len(p.jobStatus(p.options.Jobs[1]).Selected) != 3 {
					t.Fatal("fixture must retain failed nodes in the generic retry candidates")
				}
				first, err := p.AcquireJob("accounts", "first")
				if err != nil || first.Node != "b" || first.State != "ready" {
					t.Fatalf("assigned a known-failed node: %+v, %v", first, err)
				}
				nodes, err := p.ListJobNodes("accounts")
				if err != nil || len(nodes) != 1 || nodes[0].Node != "b" || nodes[0].Accounts != 1 || nodes[0].Available {
					t.Fatalf("capacity list disagrees with assignment health: %+v, %v", nodes, err)
				}
				before, _ := p.jobStore.Lease("accounts", "first")
				if _, err := p.MigrateJob("accounts", jobs.Migration{Session: "first", Node: "a", ExpectedRevision: first.Revision}); !errors.Is(err, jobs.ErrUnavailable) {
					t.Fatalf("manual migration accepted a known-failed node: %v", err)
				}
				after, _ := p.jobStore.Lease("accounts", "first")
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected migration changed the existing binding")
				}
				setAssignmentHealth(p, "b", "failed")
				if _, err := p.AcquireJob("accounts", "unavailable"); !errors.Is(err, jobs.ErrUnavailable) {
					t.Fatalf("all-failed pool allocated a new binding: %v", err)
				}
				if _, exists := p.jobStore.Lease("accounts", "unavailable"); exists {
					t.Fatal("failed acquisition persisted a binding")
				}
				if nodes, err := p.ListJobNodes("accounts"); err != nil || len(nodes) != 0 {
					t.Fatalf("all-failed capacity list is not empty: %+v, %v", nodes, err)
				}
				setAssignmentHealth(p, "a", "healthy")
				moved, err := p.MigrateJob("accounts", jobs.Migration{Session: "first", Node: "a", ExpectedRevision: first.Revision})
				if err != nil || moved.Node != "a" || moved.Revision == first.Revision || moved.ProxyURL != first.ProxyURL {
					t.Fatalf("confirmed recovery did not permit explicit migration: %+v, %v", moved, err)
				}
			})
		}
	}
}

func TestNewAssignmentFilterPreservesExistingStrictIdentity(t *testing.T) {
	for _, ttl := range []string{"0", "48h"} {
		for _, state := range []string{"failed", "unknown", "paused"} {
			t.Run(ttl+"/"+state, func(t *testing.T) {
				p := assignmentHealthPool(t, "manual", true)
				p.options.Jobs[1].SessionTTL = ttl
				path := filepath.Join(t.TempDir(), "bindings.json")
				store, err := jobs.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				p.jobStore = store
				setAssignmentHealth(p, "a", "healthy")
				first, err := p.AcquireJob("accounts", "existing")
				if err != nil || first.Node != "a" {
					t.Fatalf("initial assignment: %+v, %v", first, err)
				}
				// Reopen the persisted store, as on restart, before changing health.
				p.jobStore, err = jobs.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				before, _ := p.jobStore.Lease("accounts", "existing")
				setAssignmentHealth(p, "a", state)
				if state == "paused" {
					p.setMemberEligible(p.memberByTag["a"], false)
				}
				setAssignmentHealth(p, "b", "healthy")
				setAssignmentHealth(p, "c", "failed")
				if _, err := p.ListJobNodes("accounts"); err != nil {
					t.Fatal(err)
				}
				again, err := p.AcquireJob("accounts", "existing")
				if err != nil || again.Node != first.Node || again.Revision != first.Revision || !again.ExpiresAt.Equal(first.ExpiresAt) || again.Permanent != first.Permanent || again.FailoverCount != 0 {
					t.Fatalf("strict binding was rebound or renewed: %+v, %v", again, err)
				}
				if state == "paused" && (again.State != "paused" || again.ProxyURL != "") {
					t.Fatalf("paused strict binding became usable: %+v", again)
				}
				after, _ := p.jobStore.Lease("accounts", "existing")
				if !reflect.DeepEqual(before, after) {
					t.Fatal("listing/acquiring changed the persisted identity")
				}
			})
		}
	}
}

func TestNewPinnedAssignmentHealthAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, a, b, c, want string
	}{
		{"prefer healthy over unknown", "unknown", "healthy", "failed", "b"},
		{"cold start can use unknown", "failed", "unknown", "failed", "b"},
		{"missing monitor is unknown", "failed", "unmonitored", "failed", "b"},
		{"known failures are never fallback", "failed", "failed", "failed", ""},
		{"unknown cannot bypass block", "failed", "blocked", "failed", ""},
		{"healthy cannot escape profile", "unknown", "outside-profile", "failed", "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := assignmentHealthPool(t, "manual", true)
			for tag, state := range map[string]string{"a": tc.a, "b": tc.b, "c": tc.c} {
				switch state {
				case "unmonitored":
					p.memberByTag[tag].entry = nil
				case "blocked":
					setAssignmentHealth(p, tag, "unknown")
					p.setMemberEligible(p.memberByTag[tag], false)
				case "outside-profile":
					setAssignmentHealth(p, tag, "healthy")
					delete(p.profiles["fast"].allowed, tag)
				default:
					setAssignmentHealth(p, tag, state)
				}
			}
			first, err := p.AcquireJob("accounts", "first")
			if tc.want == "" {
				if !errors.Is(err, jobs.ErrUnavailable) || len(p.jobStore.Leases()) != 0 {
					t.Fatalf("unsafe fallback created a binding: %+v, %v", first, err)
				}
				return
			}
			if err != nil || first.Node != tc.want {
				t.Fatalf("assignment %+v, %v; want %s", first, err, tc.want)
			}
			if _, err := p.AcquireJob("accounts", "overflow"); !errors.Is(err, jobs.ErrCapacity) {
				t.Fatalf("capacity was bypassed with a failed/unknown/reused node: %v", err)
			}
			if _, exists := p.jobStore.Lease("accounts", "overflow"); exists {
				t.Fatal("capacity rejection persisted a binding")
			}
			nodes, err := p.ListJobNodes("accounts")
			if err != nil || len(nodes) != 1 || nodes[0].Node != tc.want || nodes[0].Accounts != 1 || nodes[0].Available {
				t.Fatalf("capacity list mismatch: %+v, %v", nodes, err)
			}
			// Capacity belongs to each application, not to the global node.
			other := p.options.Jobs[1]
			other.Name = "other-app"
			p.options.Jobs = append(p.options.Jobs, other)
			p.options.JobPolicies[other.Name] = "other-policy"
			p.options.JobEndpoints[other.Name] = p.options.JobEndpoints["accounts"]
			second, err := p.AcquireJob(other.Name, "first")
			if err != nil || second.Node != tc.want {
				t.Fatalf("capacity leaked across applications: %+v, %v", second, err)
			}
		})
	}
}

func TestNewAssignmentFilterDoesNotChangePooledRetries(t *testing.T) {
	p := assignmentHealthPool(t, "manual", false)
	p.options.Jobs[0].Selection = "manual"
	for _, tag := range []string{"a", "b", "c"} {
		setAssignmentHealth(p, tag, "failed")
	}
	if access, err := p.AcquireJob("catalog", ""); err != nil || access.ProxyURL == "" {
		t.Fatalf("durable-assignment filtering disabled ordinary pooled retries: %+v, %v", access, err)
	}
	if len(p.jobStore.Leases()) != 0 {
		t.Fatal("pooled access persisted a durable binding")
	}
}
