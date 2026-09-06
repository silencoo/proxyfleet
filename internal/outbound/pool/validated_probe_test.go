package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestValidatedCandidateHealthUpdatesRoutingWithoutTrafficCounters(t *testing.T) {
	for _, scenario := range []string{"healthy", "auto-ban", "manual-ban", "timeout", "remote-timeout", "protocol", "wrong-target", "wrong-outbound"} {
		t.Run(scenario, func(t *testing.T) {
			ResetSharedStateStore()
			t.Cleanup(ResetSharedStateStore)
			mgr, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.test/check"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(mgr.Stop)
			state := acquireSharedState("candidate")
			handle := mgr.Register(monitor.NodeInfo{Tag: state.tag})
			state.attachEntry(handle)
			outbound := &probeTransportOutbound{}
			member := &memberState{tag: state.tag, shared: state, entry: handle, outbound: outbound}
			p := newIndexedTestPool(member, Options{BlacklistDuration: time.Hour, TransientCooldown: time.Minute})
			p.monitor = mgr
			t.Cleanup(func() { _ = p.Close() })
			p.registerProbe(member)
			target, _ := mgr.DestinationForProbe()
			if scenario == "auto-ban" {
				state.recordProbeFailure(errors.New("bad protocol"), time.Hour, time.Minute)
			}
			if scenario == "manual-ban" {
				blacklistSharedMember(state.tag, time.Hour)
			}
			result := ValidatedProbe{Tag: state.tag, Outbound: outbound, Target: target, StartedAt: time.Now(), Latency: 2 * time.Millisecond}
			result.CompletedAt = time.Now()
			switch scenario {
			case "timeout":
				result.Err = context.DeadlineExceeded
			case "remote-timeout":
				result.Err = errors.New("read HTTP response: remote error: dial tcp 192.0.2.1:80: i/o timeout")
			case "protocol":
				result.Err = errors.New("bad protocol")
			case "wrong-target":
				result.Target.RequestURI = "/another"
			case "wrong-outbound":
				result.Outbound = &probeTransportOutbound{}
			}
			applied := ApplyValidatedProbes(p, []ValidatedProbe{result})
			snapshot := mgr.Snapshot()[0]
			if scenario == "wrong-target" || scenario == "wrong-outbound" {
				if applied != 0 || snapshot.InitialCheckDone {
					t.Fatal("unrelated candidate modified health")
				}
				return
			}
			if applied != 1 || !snapshot.InitialCheckDone {
				t.Fatalf("candidate evidence not imported: %+v", snapshot)
			}
			wantAvailable := scenario == "healthy" || scenario == "auto-ban"
			if snapshot.Available != wantAvailable || (p.selectEligibleMember("tcp") != nil) != wantAvailable {
				t.Fatalf("routing/health mismatch: %+v", snapshot)
			}
			if (scenario == "timeout" || scenario == "remote-timeout") && (!snapshot.CoolingDown || snapshot.Blacklisted) {
				t.Fatal("timeout did not use cooldown")
			}
			if snapshot.SuccessCount != 0 || snapshot.FailureCount != 0 || !snapshot.LastPassiveSuccess.IsZero() || !snapshot.LastPassiveFailure.IsZero() {
				t.Fatal("candidate probe counted as traffic")
			}
			if len(snapshot.Timeline) != 1 || snapshot.Timeline[0].Source != "probe" || !snapshot.LastProbeAt.Equal(result.CompletedAt) {
				t.Fatal("candidate observation lost timestamp/source")
			}
			if ApplyValidatedProbes(p, []ValidatedProbe{result}) != 0 {
				t.Fatal("candidate applied twice")
			}
		})
	}
}
