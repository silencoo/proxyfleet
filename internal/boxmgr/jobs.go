package boxmgr

import (
	"context"
	"time"

	"github.com/silencoo/proxyfleet/internal/jobs"
	"github.com/silencoo/proxyfleet/internal/outbound/pool"
)

func (m *Manager) pauseNewJobAssignments() func() {
	if m.jobStore == nil {
		return func() {}
	}
	return m.jobStore.PauseNewAssignments()
}

// API operations are serialized with runtime replacement, so assignments
// cannot come from an uncommitted or retired pool.
func (m *Manager) jobController() jobs.Controller {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.currentBox == nil {
		return nil
	}
	outbound, ok := m.currentBox.Outbound().Outbound(pool.Tag)
	if !ok {
		return nil
	}
	controller, _ := outbound.(jobs.Controller)
	return controller
}
func (m *Manager) ListJobs() []jobs.Status {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c := m.jobController(); c != nil {
		return c.ListJobs()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []jobs.Status{}
	if m.cfg != nil {
		for _, job := range m.cfg.Jobs {
			status := jobs.Status{Name: job.Name, Mode: job.Mode, Selection: job.EffectiveSelection(), Selected: []jobs.SelectedNode{}}
			if job.AutomaticSelection() {
				status.Size, status.TargetURL = job.Size, job.TargetURL
			}
			result = append(result, status)
		}
	}
	return result
}

func (m *Manager) ListJobSessions() []jobs.SessionInfo {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c := m.jobController(); c != nil {
		return c.ListJobSessions()
	}
	result := []jobs.SessionInfo{}
	if m.jobStore != nil {
		for _, lease := range m.jobStore.Leases() {
			info := jobs.SessionInfo{Job: lease.Job, Session: lease.Session, Node: lease.Node, State: "unavailable", ExpiresAt: lease.ExpiresAt, Permanent: lease.Permanent, Revision: lease.Version()}
			m.mu.RLock()
			if m.cfg != nil {
				for _, j := range m.cfg.Jobs {
					if j.Name == lease.Job {
						m.jobStore.DescribeActivity(&info, j)
						break
					}
				}
			}
			m.mu.RUnlock()
			result = append(result, info)
		}
	}
	return result
}
func (m *Manager) RefreshJob(ctx context.Context, name string) (jobs.Status, error) {
	m.reloadMu.Lock()
	c := m.jobController()
	m.reloadMu.Unlock()
	// Network measurements must not block reload or shutdown. Closed pools
	// discard late results; measurements are scoped by the immutable policy.
	if c != nil {
		return c.RefreshJob(ctx, name)
	}
	return jobs.Status{}, jobs.ErrUnavailable
}
func (m *Manager) AcquireJob(name, session string) (jobs.Access, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c := m.jobController(); c != nil {
		return c.AcquireJob(name, session)
	}
	return jobs.Access{}, jobs.ErrUnavailable
}
func (m *Manager) ReleaseJob(name, session string) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if m.jobStore != nil {
		return m.jobStore.Release(name, session)
	}
	return jobs.ErrUnavailable
}
func (m *Manager) ReportJob(name string, f jobs.Feedback) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c := m.jobController(); c != nil {
		return c.ReportJob(name, f)
	}
	return jobs.ErrUnavailable
}

func (m *Manager) ListJobNodes(name string) ([]jobs.NodeCapacity, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c, ok := m.jobController().(jobs.BindingController); ok {
		return c.ListJobNodes(name)
	}
	return nil, jobs.ErrUnavailable
}

func (m *Manager) MigrateJob(name string, request jobs.Migration) (jobs.Access, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if c, ok := m.jobController().(jobs.BindingController); ok {
		return c.MigrateJob(name, request)
	}
	return jobs.Access{}, jobs.ErrUnavailable
}

// The manager owns one maintenance loop across all pool/runtime replacements.
func (m *Manager) startJobMaintenance(ctx context.Context) {
	m.jobMaintenanceStop = make(chan struct{})
	m.jobMaintenanceDone = make(chan struct{})
	go func() {
		defer close(m.jobMaintenanceDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.jobMaintenanceStop:
				return
			case <-ticker.C:
				m.reloadMu.Lock()
				m.mu.RLock()
				closed, cfg := m.closed, m.cfg
				m.mu.RUnlock()
				if !closed && cfg != nil {
					n, err := m.jobStore.SweepIdle(cfg.Jobs, time.Now())
					if err != nil {
						m.logger.Warnf("persist/reclaim idle application bindings: %v", err)
					}
					if n > 0 {
						m.logger.Infof("reclaimed %d idle application bindings", n)
					}
				}
				m.reloadMu.Unlock()
			}
		}
	}()
}
