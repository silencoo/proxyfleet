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

// A reservation consumes one account slot, not one slot per concurrent dial.
// It is deliberately memory-only: a crash must leave the original durable pin.
type failoverReservation struct {
	expected                            Lease
	target                              SelectedNode
	policy, reason, fromName, committed string
	users                               int
}

// FailoverAttempt holds capacity until its dial has finished. Call Abort on every
// exit, including success. Commit is allowed only after a successful TCP dial.
type FailoverAttempt struct {
	store       *Store
	reservation *failoverReservation
	closed      bool // protected by store.mu
}

func (a *FailoverAttempt) Node() string { return a.reservation.target.Node }

func (s *Store) accountLoadsLocked(job string) map[string]int {
	loads := make(map[string]int)
	for _, l := range s.leases {
		if l.Job == job {
			loads[l.Node]++
		}
	}
	for key, r := range s.failovers {
		if r.expected.Job != job {
			continue
		}
		// A committed (or manually adopted) reservation is already counted.
		if l, ok := s.leases[key]; !ok || l.Node != r.target.Node {
			loads[r.target.Node]++
		}
	}
	return loads
}

func (s *Store) BeginFailover(j config.JobConfig, policy string, expected Lease, eligible []SelectedNode, reason, fromName string) (*FailoverAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !j.AutomaticFailover() || expected.Job != j.Name {
		return nil, ErrConflict
	}
	key := leaseKey(j.Name, expected.Session)
	old, ok := s.leases[key]
	if !ok {
		return nil, ErrNotFound
	}
	if old.Policy != policy || old.Expired(time.Now()) {
		return nil, ErrConflict
	}
	if s.assignmentPauses > 0 {
		return nil, ErrUnavailable
	}
	switch reason {
	case "node_missing", "node_unavailable", "dial_failed":
	default:
		return nil, ErrConflict
	}
	if r := s.failovers[key]; r != nil {
		if r.expected.Version() != expected.Version() || r.policy != policy ||
			(old.Version() != expected.Version() && old.Version() != r.committed) {
			return nil, ErrConflict
		}
		allowed := false
		for _, n := range eligible {
			if n.Node == r.target.Node {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, ErrUnavailable
		}
		// Stores survive runtime reloads. A new runtime must not inherit a
		// reservation admitted under a larger account limit as a committed pin.
		if old.Version() == expected.Version() && j.MaxAccountsPerNode > 0 && s.accountLoadsLocked(j.Name)[r.target.Node] > j.MaxAccountsPerNode {
			return nil, ErrCapacity
		}
		r.users++
		return &FailoverAttempt{store: s, reservation: r}, nil
	}
	if old.Version() != expected.Version() {
		return nil, ErrConflict
	}
	if old.LastFailover != nil && time.Since(old.LastFailover.At) < FailoverCooldown {
		return nil, ErrFailoverCooldown
	}
	loads := s.accountLoadsLocked(j.Name)
	var target SelectedNode
	hasAlternative := false
	for _, n := range eligible {
		if n.Node == "" || n.Node == old.Node {
			continue
		}
		hasAlternative = true
		if j.MaxAccountsPerNode > 0 && loads[n.Node] >= j.MaxAccountsPerNode {
			continue
		}
		if target.Node == "" || loads[n.Node] < loads[target.Node] {
			target = n
		}
	}
	if target.Node == "" {
		if hasAlternative {
			return nil, ErrCapacity
		}
		return nil, ErrUnavailable
	}
	r := &failoverReservation{expected: old, target: target, policy: policy, reason: reason, fromName: fromName, users: 1}
	if s.failovers == nil {
		s.failovers = make(map[string]*failoverReservation)
	}
	s.failovers[key] = r
	return &FailoverAttempt{store: s, reservation: r}, nil
}

// Commit atomically persists the successful route and holds its live tunnel.
// Manual migration, release/reacquire and runtime cutover always win over a late
// dial. A failed write leaves identity, history and cooldown unchanged.
func (a *FailoverAttempt) Commit() (Lease, func(), error) {
	s := a.store
	s.mu.Lock()
	defer s.mu.Unlock()
	r := a.reservation
	key := leaseKey(r.expected.Job, r.expected.Session)
	if a.closed || s.failovers[key] != r {
		return Lease{}, nil, ErrConflict
	}
	old, ok := s.leases[key]
	if !ok {
		return Lease{}, nil, ErrNotFound
	}
	if s.assignmentPauses > 0 {
		return Lease{}, nil, ErrUnavailable
	}
	if old.Policy != r.policy || old.Expired(time.Now()) {
		return Lease{}, nil, ErrConflict
	}
	if r.committed != "" && old.Version() == r.committed {
		return old, s.holdLocked(key, old), nil
	}
	if old.Version() != r.expected.Version() {
		return Lease{}, nil, ErrConflict
	}
	updated := old
	updated.Node, updated.Revision = r.target.Node, rand.Text()
	updated.FailoverCount++
	updated.LastFailover = &FailoverEvent{At: time.Now(), FromNode: old.Node, ToNode: updated.Node, FromName: r.fromName, ToName: r.target.Name, Reason: r.reason}
	s.leases[key] = updated
	if err := s.saveLocked(); err != nil {
		s.leases[key] = old
		return Lease{}, nil, err
	}
	r.committed = updated.Version()
	return updated, s.holdLocked(key, updated), nil
}

func (a *FailoverAttempt) Abort() {
	s := a.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	r := a.reservation
	r.users--
	if r.users == 0 {
		delete(s.failovers, leaseKey(r.expected.Job, r.expected.Session))
	}
}
