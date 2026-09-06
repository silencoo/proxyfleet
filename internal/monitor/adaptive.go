package monitor

import (
	"math"
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
	DailyLimit     int       `json:"daily_limit"`
	DailyUsed      int       `json:"daily_used"`
	DailyRemaining int       `json:"daily_remaining"`
	DayStarted     time.Time `json:"day_started"`
	DailyNextReset time.Time `json:"daily_next_reset"`
	Eligible       int       `json:"eligible"`
	Due            int       `json:"due"`
	PassiveSkipped int       `json:"passive_skipped"`
	AvailableNow   int       `json:"available_now"`
	PacingBurst    int       `json:"pacing_burst"`
}

type adaptiveProbeCandidate struct {
	entry    *entry
	priority int
	last     time.Time
	tag      string
}

func (m *Manager) ProbeBudgetStatus() ProbeBudgetStatus {
	if m == nil {
		return ProbeBudgetStatus{}
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	limit, dailyLimit := cfg.ProbeMaxPerHour, cfg.ProbeMaxPerDay
	now := time.Now()
	m.probeBudgetMu.Lock()
	m.resetProbeBudgetLocked(now)
	burst, rate := m.refillProbeBudgetLocked(cfg, now)
	available := m.probeAllowanceLocked(cfg, rate)
	window := m.probeBudgetWindow
	used := m.probeBudgetUsed
	day := m.probeBudgetDay
	dailyUsed := m.probeBudgetDailyUsed
	m.probeBudgetMu.Unlock()
	remaining := 0
	if limit > 0 {
		remaining = limit - used
		if remaining < 0 {
			remaining = 0
		}
	}
	dailyRemaining := 0
	if dailyLimit > 0 {
		dailyRemaining = dailyLimit - dailyUsed
		if dailyRemaining < 0 {
			dailyRemaining = 0
		}
	}
	return ProbeBudgetStatus{
		Mode:           cfg.ProbeMode,
		Limit:          limit,
		Used:           used,
		Remaining:      remaining,
		WindowStarted:  window,
		NextReset:      window.Add(time.Hour),
		DailyLimit:     dailyLimit,
		DailyUsed:      dailyUsed,
		DailyRemaining: dailyRemaining,
		DayStarted:     day,
		DailyNextReset: day.AddDate(0, 0, 1),
		Eligible:       int(m.adaptiveEligible.Load()),
		Due:            int(m.adaptiveDue.Load()),
		PassiveSkipped: int(m.adaptivePassiveSkip.Load()),
		AvailableNow:   min(available, burst),
		PacingBurst:    burst,
	}
}

func (m *Manager) resetProbeBudgetLocked(now time.Time) {
	window := now.Truncate(time.Hour)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if m.probeBudgetWindow.IsZero() || window.After(m.probeBudgetWindow) {
		m.probeBudgetWindow = window
		m.probeBudgetUsed = 0
	}
	if m.probeBudgetDay.IsZero() || day.After(m.probeBudgetDay) {
		m.probeBudgetDay = day
		m.probeBudgetDailyUsed = 0
	}
}

// A single startup/idle batch is available immediately. Thereafter refill at
// the stricter hourly/daily rate, without reissuing a burst at window resets.
// The fixed-window caps still apply independently of this pacing bucket.
func (m *Manager) refillProbeBudgetLocked(cfg Config, now time.Time) (burst int, rate float64) {
	burst = cfg.ProbeBatchSize
	if burst <= 0 {
		burst = defaultAutomaticProbeBatch
	}
	if cfg.ProbeMaxPerHour > 0 {
		burst = min(burst, cfg.ProbeMaxPerHour)
		rate = float64(cfg.ProbeMaxPerHour) / time.Hour.Seconds()
	}
	if cfg.ProbeMaxPerDay > 0 {
		burst = min(burst, cfg.ProbeMaxPerDay)
		dailyRate := float64(cfg.ProbeMaxPerDay) / (24 * time.Hour).Seconds()
		if rate == 0 || dailyRate < rate {
			rate = dailyRate
		}
	}
	if m.probeBudgetRefilled.IsZero() {
		m.probeBudgetTokens = float64(burst)
		m.probeBudgetRefilled = now
	} else if now.After(m.probeBudgetRefilled) {
		m.probeBudgetTokens += now.Sub(m.probeBudgetRefilled).Seconds() * rate
		m.probeBudgetRefilled = now
	}
	m.probeBudgetTokens = min(float64(burst), m.probeBudgetTokens)
	return burst, rate
}

func (m *Manager) probeAllowanceLocked(cfg Config, rate float64) int {
	available := int(^uint(0) >> 1)
	if rate > 0 {
		available = int(math.Floor(m.probeBudgetTokens + 1e-9))
	}
	if cfg.ProbeMaxPerHour > 0 {
		available = min(available, cfg.ProbeMaxPerHour-m.probeBudgetUsed)
	}
	if cfg.ProbeMaxPerDay > 0 {
		available = min(available, cfg.ProbeMaxPerDay-m.probeBudgetDailyUsed)
	}
	return max(available, 0)
}

func (m *Manager) reserveProbeBudget(requested int, now time.Time) int {
	if requested <= 0 {
		return 0
	}
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	m.probeBudgetMu.Lock()
	defer m.probeBudgetMu.Unlock()
	m.resetProbeBudgetLocked(now)
	_, rate := m.refillProbeBudgetLocked(cfg, now)
	allowed := min(requested, m.probeAllowanceLocked(cfg, rate))
	if allowed <= 0 {
		return 0
	}
	m.probeBudgetUsed += allowed
	m.probeBudgetDailyUsed += allowed
	if rate > 0 {
		m.probeBudgetTokens = max(0, m.probeBudgetTokens-float64(allowed))
	}
	return allowed
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
		lastFailure := candidate.lastFail
		failures := candidate.consecutiveProbeFails
		tag := candidate.info.Tag
		candidate.mu.RUnlock()
		if !probeAvailable {
			continue
		}
		if initialCheckDone && available && lastPassiveSuccess.After(lastFailure) && now.Sub(lastPassiveSuccess) >= 0 && now.Sub(lastPassiveSuccess) < cfg.ProbePassiveGrace {
			passiveSkipped++
			continue
		}

		priority := 2
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
			candidates = append(candidates, adaptiveProbeCandidate{entry: candidate, priority: priority, last: last, tag: tag})
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
			return candidates[i].tag < candidates[j].tag
		}
		return candidates[i].last.Before(candidates[j].last)
	})
	allowed := m.reserveProbeBudget(min(len(candidates), limit), now)
	var groups [3][]*entry
	for _, candidate := range candidates {
		groups[candidate.priority] = append(groups[candidate.priority], candidate.entry)
	}
	// New nodes get two shares, recovery and healthy rechecks one each. Empty
	// groups lend their shares to the others; retain the cursor across tiny
	// batches so a one-token budget cannot starve recovery or healthy nodes.
	shares := [...]int{0, 1, 0, 2}
	selected := make([]*entry, 0, allowed)
	for len(selected) < allowed {
		group := shares[(m.adaptivePickCursor.Add(1)-1)%uint64(len(shares))]
		if len(groups[group]) == 0 {
			continue
		}
		selected = append(selected, groups[group][0])
		groups[group] = groups[group][1:]
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
