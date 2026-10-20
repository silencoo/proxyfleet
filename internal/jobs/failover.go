package jobs

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

// Bound per-session churn even when many concurrent browser connections fail.
// Persisting the timestamp also preserves this protection across restarts.
const FailoverCooldown = 30 * time.Second

var ErrFailoverCooldown = fmt.Errorf("%w: automatic failover cooldown; retry in 30 seconds", ErrUnavailable)

// Only the most recent transition is retained, keeping the durable store bounded.
// Events are immutable once published; readers must not modify them.
type FailoverEvent struct {
	At       time.Time `json:"at"`
	FromNode string    `json:"from_node"`
	ToNode   string    `json:"to_node"`
	FromName string    `json:"from_name,omitempty"`
	ToName   string    `json:"to_name,omitempty"`
	Reason   string    `json:"reason"`
}

// Failover atomically rebinds an opt-in session, never renewing its lifetime or
// adopting a changed policy. Concurrent/manual winners are returned unchanged.
// The runtime supplies freshly eligible candidates; this store enforces capacity,
// revision, cooldown, persistence and runtime-handoff fences under a single lock.
func (s *Store) Failover(j config.JobConfig, policy string, expected Lease, eligible []SelectedNode, reason, fromName string) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !j.AutomaticFailover() || expected.Job != j.Name {
		return Lease{}, ErrConflict
	}
	key := leaseKey(j.Name, expected.Session)
	old, ok := s.leases[key]
	if !ok {
		return Lease{}, ErrNotFound
	}
	if old.Version() != expected.Version() {
		return old, nil
	}
	now := time.Now()
	if old.Policy != policy || old.Expired(now) {
		return Lease{}, ErrConflict
	}
	if s.assignmentPauses > 0 {
		return Lease{}, ErrUnavailable
	}
	if old.LastFailover != nil && now.Sub(old.LastFailover.At) < FailoverCooldown {
		return Lease{}, ErrFailoverCooldown
	}
	switch reason {
	case "node_missing", "node_unavailable", "dial_failed":
	default:
		return Lease{}, ErrConflict
	}
	loads := make(map[string]int)
	for _, lease := range s.leases {
		if lease.Job == j.Name {
			loads[lease.Node]++
		}
	}
	var target SelectedNode
	hasAlternative := false
	for _, node := range eligible {
		if node.Node == "" || node.Node == old.Node {
			continue
		}
		hasAlternative = true
		if j.MaxAccountsPerNode > 0 && loads[node.Node] >= j.MaxAccountsPerNode {
			continue
		}
		if target.Node == "" || loads[node.Node] < loads[target.Node] {
			target = node
		}
	}
	if target.Node == "" {
		if hasAlternative {
			return Lease{}, ErrCapacity
		}
		return Lease{}, ErrUnavailable
	}
	updated := old
	updated.Node, updated.Revision = target.Node, rand.Text()
	updated.FailoverCount++
	updated.LastFailover = &FailoverEvent{At: now, FromNode: old.Node, ToNode: target.Node, FromName: fromName, ToName: target.Name, Reason: reason}
	s.leases[key] = updated
	if err := s.saveLocked(); err != nil {
		s.leases[key] = old
		return Lease{}, err
	}
	return updated, nil
}
