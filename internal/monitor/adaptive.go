package monitor

import (
	"sort"
	"time"
)

// ProbeBudgetStatus is exposed to the API so operators can see the actual
// adaptive-probe cost instead of guessing from interval and batch size.
type ProbeBudgetStatus struct {
	Mode           string    `json:"mode"`
	Limit          int       `json:"limit"`
	Used           int       `json:"used"`
	Remaining      int       `json:"remaining"`
	WindowStarted  time.Time `json:"window_started"`
	NextReset      time.Time `json:"next_reset"`
	Eligible       int       `json:"eligible"`
	Due            int       `json:"due"`
	PassiveSkipped int       `json:"passive_skipped"`
}

type adaptiveProbeCandidate struct {
	entry    *entry
	priority int
	last     time.Time
}

func (m *Manager) ProbeBudgetStatus() ProbeBudgetStatus {
	if m == nil {
		return ProbeBudgetStatus{}
	}
	m.mu.RLock()
	mode := m.cfg.ProbeMode
	limit := m.cfg.ProbeMaxPerHour
	m.mu.RUnlock()
	now := time.Now()
	m.probeBudgetMu.Lock()
	m.resetProbeBudgetLocked(now)
	window := m.probeBudgetWindow
	used := m.probeBudgetUsed
	m.probeBudgetMu.Unlock()
	remaining := 0
	if limit > 0 {
		remaining = limit - used
		if remaining < 0 {
			remaining = 0
		}
	}
	return ProbeBudgetStatus{
		Mode:           mode,
		Limit:          limit,
		Used:           used,
		Remaining:      remaining,
		WindowStarted:  window,
		NextReset:      window.Add(time.Hour),
		Eligible:       int(m.adaptiveEligible.Load()),
		Due:            int(m.adaptiveDue.Load()),
		PassiveSkipped: int(m.adaptivePassiveSkip.Load()),
	}
}

func (m *Manager) resetProbeBudgetLocked(now time.Time) {
	window := now.Truncate(time.Hour)
	if m.probeBudgetWindow.IsZero() || !m.probeBudgetWindow.Equal(window) {
		m.probeBudgetWindow = window
		m.probeBudgetUsed = 0
	}
}

func (m *Manager) reserveProbeBudget(requested int, now time.Time) int {
	if requested <= 0 {
		return 0
	}
	m.mu.RLock()
	limit := m.cfg.ProbeMaxPerHour
	m.mu.RUnlock()
	m.probeBudgetMu.Lock()
	defer m.probeBudgetMu.Unlock()
	m.resetProbeBudgetLocked(now)
	if limit <= 0 {
		m.probeBudgetUsed += requested
		return requested
	}
	remaining := limit - m.probeBudgetUsed
	if remaining <= 0 {
		return 0
	}
	if requested > remaining {
		requested = remaining
	}
	m.probeBudgetUsed += requested
	return requested
}

func (m *Manager) selectAdaptiveEntries(entries []*entry, limit int, now time.Time) []*entry {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if limit <= 0 {
		limit = len(entries)
	}
	if cfg.ProbeHealthyInterval <= 0 {
		cfg.ProbeHealthyInterval = 30 * time.Minute
	}
	if cfg.ProbeFailureRetryInterval <= 0 {
		cfg.ProbeFailureRetryInterval = time.Minute
	}
	if cfg.ProbeFailureMaxInterval < cfg.ProbeFailureRetryInterval {
		cfg.ProbeFailureMaxInterval = time.Hour
	}
	if cfg.ProbePassiveGrace <= 0 {
		cfg.ProbePassiveGrace = 10 * time.Minute
	}

	candidates := make([]adaptiveProbeCandidate, 0, len(entries))
	passiveSkipped := 0
	for _, candidate := range entries {
		candidate.mu.RLock()
		probeAvailable := candidate.probe != nil
		initialCheckDone := candidate.initialCheckDone
		available := candidate.available
		lastProbeAt := candidate.lastProbeAt
		lastPassiveSuccess := candidate.lastPassiveSuccess
		failures := candidate.consecutiveProbeFails
		candidate.mu.RUnlock()
		if !probeAvailable {
			continue
		}
		if !lastPassiveSuccess.IsZero() && now.Sub(lastPassiveSuccess) < cfg.ProbePassiveGrace {
			passiveSkipped++
			continue
		}

		priority := 3
		due := false
		last := lastProbeAt
		switch {
		case !initialCheckDone || lastProbeAt.IsZero():
			priority = 0
			due = true
		case failures > 0 || !available:
			priority = 1
			interval := adaptiveFailureInterval(cfg.ProbeFailureRetryInterval, cfg.ProbeFailureMaxInterval, failures)
			due = now.Sub(lastProbeAt) >= interval
		default:
			due = now.Sub(lastProbeAt) >= cfg.ProbeHealthyInterval
		}
		if due {
			candidates = append(candidates, adaptiveProbeCandidate{entry: candidate, priority: priority, last: last})
		}
	}
	m.adaptiveEligible.Store(int32(len(entries)))
	m.adaptiveDue.Store(int32(len(candidates)))
	m.adaptivePassiveSkip.Store(int32(passiveSkipped))

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		if candidates[i].last.Equal(candidates[j].last) {
			candidates[i].entry.mu.RLock()
			left := candidates[i].entry.info.Tag
			candidates[i].entry.mu.RUnlock()
			candidates[j].entry.mu.RLock()
			right := candidates[j].entry.info.Tag
			candidates[j].entry.mu.RUnlock()
			return left < right
		}
		return candidates[i].last.Before(candidates[j].last)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	allowed := m.reserveProbeBudget(len(candidates), now)
	if allowed < len(candidates) {
		candidates = candidates[:allowed]
	}
	selected := make([]*entry, len(candidates))
	for index := range candidates {
		selected[index] = candidates[index].entry
	}
	return selected
}

func adaptiveFailureInterval(base, maximum time.Duration, failures int) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	if maximum < base {
		maximum = time.Hour
	}
	if failures < 1 {
		failures = 1
	}
	interval := base
	for step := 1; step < failures && interval < maximum; step++ {
		if interval > maximum/2 {
			return maximum
		}
		interval *= 2
	}
	if interval > maximum {
		return maximum
	}
	return interval
}

func updateEWMALatencyLocked(e *entry, latency time.Duration) {
	if e == nil || latency <= 0 {
		return
	}
	value := float64(latency) / float64(time.Millisecond)
	if e.ewmaLatencyMs <= 0 {
		e.ewmaLatencyMs = value
		return
	}
	const alpha = 0.25
	e.ewmaLatencyMs = alpha*value + (1-alpha)*e.ewmaLatencyMs
}

func qualityScoreLocked(e *entry, now time.Time) float64 {
	if e == nil || e.blacklist || e.coolingDown || (e.initialCheckDone && !e.available) {
		return 0
	}
	score := 15.0
	if e.available {
		score = 40
	}
	total := float64(e.success + int64(e.failure))
	if total > 0 {
		score += 30 * float64(e.success) / total
	} else {
		score += 15
	}
	latency := e.ewmaLatencyMs
	if latency <= 0 && e.lastProbe > 0 {
		latency = float64(e.lastProbe) / float64(time.Millisecond)
	}
	if latency > 0 {
		latencyScore := 25 * (1 - latency/2000)
		if latencyScore < 0 {
			latencyScore = 0
		}
		score += latencyScore
	} else {
		score += 10
	}
	if !e.lastOK.IsZero() {
		age := now.Sub(e.lastOK)
		switch {
		case age <= 5*time.Minute:
			score += 5
		case age <= time.Hour:
			score += 2
		}
	}
	score -= float64(e.consecutiveProbeFails * 8)
	if !e.lastPassiveFailure.IsZero() && now.Sub(e.lastPassiveFailure) < 5*time.Minute {
		score -= 10
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// QualityScore returns a bounded 0-100 score for quality-aware scheduling.
func (h *EntryHandle) QualityScore() float64 {
	if h == nil || h.ref == nil {
		return 0
	}
	h.ref.mu.RLock()
	defer h.ref.mu.RUnlock()
	return qualityScoreLocked(h.ref, time.Now())
}
