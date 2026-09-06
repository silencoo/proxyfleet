package pool

import (
	"fmt"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

// ValidatedProbe is transaction-local evidence, never persisted or published
// until its candidate runtime is committed. Outbound retains the exact live
// transport identity; a reused subscription tag alone is not sufficient.
type ValidatedProbe struct {
	Tag         string
	Outbound    adapter.Outbound
	Target      monitor.ProbeTarget
	StartedAt   time.Time
	CompletedAt time.Time
	Latency     time.Duration
	Err         error
}

func probeRuntimeIdentity(outbound adapter.Outbound) string {
	return fmt.Sprintf("%T:%p", outbound, outbound)
}

// ApplyValidatedProbes transfers candidate results to this exact committed
// pool. Results from another runtime/target or superseded by traffic are ignored.
func ApplyValidatedProbes(outbound adapter.Outbound, results []ValidatedProbe) int {
	p, ok := outbound.(*poolOutbound)
	if !ok || p.monitor == nil {
		return 0
	}
	p.healthMu.RLock()
	defer p.healthMu.RUnlock()
	if p.closed.Load() {
		return 0
	}
	target, ready := p.monitor.DestinationForProbe()
	if !ready {
		return 0
	}
	applied := 0
	for _, result := range results {
		member := p.memberByTag[result.Tag]
		if member == nil || member.shared == nil || member.entry == nil || result.Outbound == nil ||
			probeRuntimeIdentity(member.outbound) != probeRuntimeIdentity(result.Outbound) || result.Target != target {
			continue
		}
		s := member.shared
		s.transitionMu.Lock()
		if !s.closed.Load() && member.entry.ApplyValidatedProbe(probeRuntimeIdentity(member.outbound), result.StartedAt, result.CompletedAt, result.Latency, result.Err, func() {
			if result.Err == nil {
				s.releaseAfterProbeLocked()
			} else {
				s.recordFailureWithSourceLocked(result.Err, 1, p.options.BlacklistDuration, p.options.TransientCooldown, false)
			}
		}) {
			s.persistTransitionLocked()
			applied++
		}
		s.transitionMu.Unlock()
	}
	return applied
}
