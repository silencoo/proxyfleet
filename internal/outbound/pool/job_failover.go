package pool

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

func (p *poolOutbound) jobSession(ctx context.Context, j config.JobConfig) (string, bool) {
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		return "", false
	}
	if j.SharedIngress() {
		return metadata.User, config.ValidProxySessionID(metadata.User)
	}
	return config.ProxySessionID(p.options.JobEndpoints[j.Name].Username, metadata.User)
}

// Target HTTP failures/expired benchmark samples are not transport failures.
// Unknown health during restart must not churn otherwise valid session identities.
func (p *poolOutbound) jobFailoverReason(j config.JobConfig, lease jobs.Lease) string {
	member := p.memberByTag[lease.Node]
	if member == nil {
		return "node_missing"
	}
	if p.memberEligibleForProfile(member, "tcp", p.profiles[j.Profile]) {
		return ""
	}
	if member.shared != nil && member.shared.blacklistedFast.Load() {
		return "node_unavailable"
	}
	if member.entry != nil {
		health := member.entry.ExportHealthState()
		if !health.InitialCheckDone {
			return ""
		}
		if health.Available && profileAllowsMember(p.profiles[j.Profile], member) {
			return ""
		}
	}
	return "node_unavailable"
}

func (p *poolOutbound) failoverJob(j config.JobConfig, lease jobs.Lease, reason string) (jobs.Lease, error) {
	p.healthMu.RLock()
	defer p.healthMu.RUnlock()
	if p.closed.Load() || p.jobStore == nil {
		return jobs.Lease{}, jobs.ErrUnavailable
	}
	selected := []jobs.SelectedNode{}
	unknown := []jobs.SelectedNode{}
	for _, n := range p.jobStatus(j).Selected {
		member := p.memberByTag[n.Node]
		if n.Node == lease.Node || !p.memberEligibleForProfile(member, "tcp", p.profiles[j.Profile]) {
			continue
		}
		// The eligibility index also admits retries after blacklist expiry.
		// That is not evidence of recovery: do not move a durable binding to a
		// node whose most recent transport check still reports failure.
		if member.entry != nil {
			health := member.entry.ExportHealthState()
			if health.InitialCheckDone {
				if health.Available {
					selected = append(selected, n)
				}
				continue
			}
		}
		unknown = append(unknown, n)
	}
	// During cold start, unknown nodes are a last resort, not preferable to
	// confirmed healthy nodes merely because their account load is lower.
	if len(selected) == 0 {
		selected = unknown
	}
	return p.jobStore.Failover(j, p.options.JobPolicies[j.Name], lease, selected, reason, p.options.Metadata[lease.Node].Name)
}

// Capture the revision before dialing: a late failed dial must never overwrite
// a concurrent manual migration, release/reacquire, or another recovery.
func (p *poolOutbound) failoverLeaseForDial(ctx context.Context, member *memberState) (config.JobConfig, jobs.Lease, bool) {
	j, ok := p.jobFromContext(ctx)
	if !ok || !j.AutomaticFailover() || p.jobStore == nil {
		return j, jobs.Lease{}, false
	}
	session, valid := p.jobSession(ctx, j)
	if !valid {
		return j, jobs.Lease{}, false
	}
	lease, exists := p.jobStore.Lease(j.Name, session)
	return j, lease, exists && lease.Node == member.tag
}
