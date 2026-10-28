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

// Filter new durable assignments, not the identity of an existing binding.
// Generic retry eligibility alone is not evidence of transport recovery.
func (p *poolOutbound) jobAssignmentCandidates(j config.JobConfig, candidates []jobs.SelectedNode, exclude string) []jobs.SelectedNode {
	selected := []jobs.SelectedNode{}
	unknown := []jobs.SelectedNode{}
	for _, n := range candidates {
		member := p.memberByTag[n.Node]
		if n.Node == exclude || !p.memberEligibleForProfile(member, "tcp", p.profiles[j.Profile]) {
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
	return selected
}

func (p *poolOutbound) prepareJobFailover(use *jobConnectionUse, j config.JobConfig, lease jobs.Lease, reason string) error {
	p.healthMu.RLock()
	defer p.healthMu.RUnlock()
	if p.closed.Load() || p.jobStore == nil {
		return jobs.ErrUnavailable
	}
	selected := p.jobAssignmentCandidates(j, p.jobStatus(j).Selected, lease.Node)
	attempt, err := p.jobStore.BeginFailover(j, p.options.JobPolicies[j.Name], lease, selected, reason, p.options.Metadata[lease.Node].Name)
	if err == nil {
		use.failover, use.job = attempt, j
	}
	return err
}

func (p *poolOutbound) commitJobFailover(ctx context.Context, use *jobConnectionUse) error {
	p.healthMu.RLock()
	defer p.healthMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.closed.Load() {
		return jobs.ErrUnavailable
	}
	// A manual ban or runtime transition while the alternate dial was pending
	// must not publish a route that is no longer authorized.
	if len(p.jobAssignmentCandidates(use.job, []jobs.SelectedNode{{Node: use.failover.Node()}}, "")) == 0 {
		return jobs.ErrUnavailable
	}
	_, done, err := use.failover.Commit()
	if err != nil {
		return err
	}
	if use.done != nil {
		use.done()
	}
	use.done = done
	return nil
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
