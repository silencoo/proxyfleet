package jobs

import (
	"sync"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func activityKey(l Lease) string { return leaseKey(l.Job, l.Session) + "\x00" + l.Version() }

// Hold starts before dialing and ends only when the resulting tunnel closes.
// Revision fencing prevents a reclaimed or manually migrated lease being used.
func (s *Store) Hold(job, session, revision string) (func(), error) {
	s.mu.Lock()
	k := leaseKey(job, session)
	l, ok := s.leases[k]
	if !ok || l.Version() != revision {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	done := s.holdLocked(k, l)
	s.mu.Unlock()
	return done, nil
}

func (s *Store) holdLocked(k string, l Lease) func() {
	revision := l.Version()
	a := activityKey(l)
	s.active[a]++
	l.LastUsedAt = time.Now().UTC()
	s.leases[k] = l
	s.activityDirty = true
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.active[a]--
			if s.active[a] == 0 {
				delete(s.active, a)
			}
			if current, ok := s.leases[k]; ok && current.Version() == revision {
				current.LastUsedAt = time.Now().UTC()
				s.leases[k] = current
				s.activityDirty = true
			}
		})
	}
}

// Restart grants a full idle interval: an unclean shutdown may have lost recent
// activity or interrupted a live tunnel. Never infer abandonment from that gap.
func (s *Store) idleSince(l Lease) time.Time {
	if l.LastUsedAt.Before(s.openedAt) {
		return s.openedAt
	}
	return l.LastUsedAt
}

func (s *Store) DescribeActivity(info *SessionInfo, j config.JobConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseKey(info.Job, info.Session)]
	if !ok || l.Version() != info.Revision {
		return
	}
	info.LastUsedAt = l.LastUsedAt
	info.ActiveConnections = s.active[activityKey(l)]
	if j.Mode == "pinned" && !j.DirectIngress() && j.IdleTimeout() > 0 && info.ActiveConnections == 0 {
		info.ReclaimAt = s.idleSince(l).Add(j.IdleTimeout())
	}
}

// SweepIdle is called with committed configuration only. Deletions and activity
// persistence share one atomic write; a failed write restores all removed pins.
func (s *Store) SweepIdle(configured []config.JobConfig, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignmentPauses > 0 {
		return 0, nil
	}
	timeouts := make(map[string]time.Duration)
	for _, j := range configured {
		if j.Mode == "pinned" && !j.DirectIngress() && j.IdleTimeout() > 0 {
			timeouts[j.Name] = j.IdleTimeout()
		}
	}
	removed := make(map[string]Lease)
	for k, l := range s.leases {
		if d := timeouts[l.Job]; d > 0 && s.failovers[k] == nil && s.active[activityKey(l)] == 0 && !now.Before(s.idleSince(l).Add(d)) {
			removed[k] = l
			delete(s.leases, k)
		}
	}
	if len(removed) == 0 && !s.activityDirty {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		for k, l := range removed {
			s.leases[k] = l
		}
		return 0, err
	}
	s.activityDirty = false
	return len(removed), nil
}

func (s *Store) FlushActivity() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.activityDirty {
		return nil
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.activityDirty = false
	return nil
}
