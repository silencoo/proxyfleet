// Package jobs owns bounded target measurements and durable session assignments.
// It deliberately has no dependency on the proxy runtime or management server.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

var (
	ErrNotFound    = errors.New("job or session not found")
	ErrUnavailable = errors.New("no eligible nodes available for job")
	ErrConflict    = errors.New("job operation conflicts with current state")
)

type Measurement struct {
	Node        string    `json:"node"`
	Success     bool      `json:"success"`
	DurationMS  float64   `json:"duration_ms"`
	SuccessRate float64   `json:"success_rate"`
	CheckedAt   time.Time `json:"checked_at"`
	StatusCode  int       `json:"status_code"`
	Error       string    `json:"error,omitempty"`
}

type Lease struct {
	Job       string    `json:"job"`
	Session   string    `json:"session"`
	Node      string    `json:"node"`
	Policy    string    `json:"policy"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Manual selection has node identities but no target measurements. Embedding
// keeps the existing flat JSON format for automatically measured selections.
type SelectedNode struct {
	Node string `json:"node"`
	Name string `json:"name,omitempty"`
	*Measurement
}

type Status struct {
	Name       string         `json:"name"`
	Mode       string         `json:"mode"`
	Selection  string         `json:"selection"`
	TargetURL  string         `json:"target_url"`
	Size       int            `json:"size"`
	Candidates int            `json:"candidates"`
	Selected   []SelectedNode `json:"selected"`
	Measured   int            `json:"measured"`
	Refreshing bool           `json:"refreshing"`
}

type Access struct {
	Job            string    `json:"job"`
	Mode           string    `json:"mode"`
	Selection      string    `json:"selection"`
	State          string    `json:"state"` // ready, paused, expired, or policy_changed
	Session        string    `json:"session,omitempty"`
	Node           string    `json:"node,omitempty"`
	ProxyURL       string    `json:"proxy_url,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	Concurrency    int       `json:"concurrency"`
	Timeout        string    `json:"timeout"`
	TimeoutMS      int64     `json:"timeout_ms"`
	ExpectedStatus int       `json:"expected_status"`
	BodyContains   string    `json:"body_contains,omitempty"`
}

// SessionInfo is a read-only view of an existing binding, without credentials.
type SessionInfo struct {
	Job       string    `json:"job"`
	Session   string    `json:"session"`
	Node      string    `json:"node"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Feedback struct {
	Session    string  `json:"session"`
	Node       string  `json:"node"`
	Success    bool    `json:"success"`
	DurationMS float64 `json:"duration_ms"`
	StatusCode int     `json:"status_code"`
}

type Controller interface {
	ListJobs() []Status
	ListJobSessions() []SessionInfo
	RefreshJob(context.Context, string) (Status, error)
	AcquireJob(string, string) (Access, error)
	ReleaseJob(string, string) error
	ReportJob(string, Feedback) error
}

type measurements struct {
	version     uint64
	values      map[string]Measurement
	refreshing  bool
	lastAttempt time.Time
}

type Store struct {
	probeSlots       chan struct{}
	mu               sync.Mutex
	path             string
	leases           map[string]Lease
	scores           map[string]*measurements
	scoreVersion     uint64
	assignmentPauses int
}

type contextKey struct{}

func ContextWith(ctx context.Context, store *Store) context.Context {
	return context.WithValue(ctx, contextKey{}, store)
}
func FromContext(ctx context.Context) *Store { s, _ := ctx.Value(contextKey{}).(*Store); return s }

func Open(path string) (*Store, error) {
	s := &Store{path: path, leases: make(map[string]Lease), scores: make(map[string]*measurements), probeSlots: make(chan struct{}, 16)}
	if path == "" {
		return s, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 4<<20 {
		return nil, errors.New("job session state is too large")
	}
	decoder := json.NewDecoder(io.LimitReader(f, (4<<20)+1))
	if err := decoder.Decode(&s.leases); err != nil {
		return nil, fmt.Errorf("invalid job session state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected trailing job session data")
	}
	if s.leases == nil || len(s.leases) > 4096 {
		return nil, errors.New("invalid job session count")
	}
	for k, l := range s.leases {
		if k != leaseKey(l.Job, l.Session) || l.Node == "" || l.Policy == "" || l.ExpiresAt.IsZero() {
			return nil, errors.New("invalid job session record")
		}
	}
	return s, nil
}

// Policy also includes the filter: editing a job cannot silently move an old session.
func Policy(j config.JobConfig, profile config.ProfileConfig, skipCertVerify bool) string {
	return PolicyWithCertVerifyMode(j, profile, skipCertVerify, config.CertVerifyDefault)
}

func PolicyWithCertVerifyMode(j config.JobConfig, profile config.ProfileConfig, skipCertVerify bool, mode string) string {
	j.Selection = j.EffectiveSelection()
	if !j.AutomaticSelection() {
		// Saved benchmark options may be retained while disabled. Editing those
		// inactive options must not invalidate a manually selected session.
		j.TargetURL, j.RefreshInterval = "", ""
		j.Size, j.ProbeBatchSize, j.ProbeConcurrency, j.MaxResponseBytes = 0, 0, 0, 0
	}
	data, _ := json.Marshal(struct {
		Job            config.JobConfig
		Profile        config.ProfileConfig
		SkipCertVerify bool
	}{j, profile, skipCertVerify})
	// Keep existing default-policy hashes stable. Override changes upstream
	// verification and must not reuse old measurements or silently rebind pins.
	if mode == config.CertVerifyOverride {
		data = append(data, []byte("\ncert-verify:override")...)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
func leaseKey(job, session string) string { return job + "\x00" + session }

// Only writers allocate score entries. A running refresh owns its entry until
// EndRefresh, including its single-flight flag and the measurements it publishes.
// If all entries are busy, new work waits for a later refresh attempt.
func (s *Store) scoresLocked(key string) *measurements {
	v := s.scores[key]
	if v == nil {
		if len(s.scores) >= 128 {
			var oldest string
			var when time.Time
			found := false
			for k, m := range s.scores {
				if m.refreshing {
					continue
				}
				if !found || m.lastAttempt.Before(when) {
					oldest, when = k, m.lastAttempt
					found = true
				}
			}
			if !found {
				return nil
			}
			delete(s.scores, oldest)
		}
		v = &measurements{values: make(map[string]Measurement)}
		s.scores[key] = v
	}
	return v
}

func (s *Store) BeginRefresh(key string, interval time.Duration, force bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.scoresLocked(key)
	if v == nil {
		return false
	}
	if force {
		interval = 5 * time.Second
	}
	if v.refreshing || time.Since(v.lastAttempt) < interval {
		return false
	}
	v.refreshing, v.lastAttempt = true, time.Now()
	return true
}
func (s *Store) EndRefresh(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.scores[key]; v != nil {
		v.refreshing = false
	}
}
func (s *Store) IsRefreshing(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.scores[key]
	return v != nil && v.refreshing
}
func (s *Store) Measurements(key string) (map[string]Measurement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.scores[key]
	if v == nil {
		return nil, false
	}
	copy := make(map[string]Measurement, len(v.values))
	for k, m := range v.values {
		copy[k] = m
	}
	return copy, v.refreshing
}
func (s *Store) Record(key string, m Measurement) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.scoresLocked(key)
	if v == nil {
		return
	}
	// Versions must not repeat when a policy is evicted and recreated: pool
	// ranking caches may still hold the earlier entry's version and results.
	s.scoreVersion++
	v.version = s.scoreVersion
	m.CheckedAt = time.Now()
	rate := 0.0
	if m.Success {
		rate = 1
	}
	if previous, ok := v.values[m.Node]; ok {
		m.SuccessRate = .25*rate + .75*previous.SuccessRate
		if m.Success && previous.DurationMS > 0 {
			m.DurationMS = .25*m.DurationMS + .75*previous.DurationMS
		}
	} else {
		m.SuccessRate = rate
	}
	if _, exists := v.values[m.Node]; !exists && len(v.values) >= 4096 {
		var oldest string
		var when time.Time
		for k, value := range v.values {
			if oldest == "" || value.CheckedAt.Before(when) {
				oldest, when = k, value.CheckedAt
			}
		}
		delete(v.values, oldest)
	}
	v.values[m.Node] = m
}

func (s *Store) Version(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.scores[key]; v != nil {
		return v.version
	}
	return 0
}
func (s *Store) AdmitProbe(ctx context.Context) (func(), error) {
	select {
	case s.probeSlots <- struct{}{}:
		return func() { <-s.probeSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func Rank(values map[string]Measurement, allowed map[string]bool, maxAge time.Duration, now time.Time) []Measurement {
	ranked := make([]Measurement, 0)
	for tag, m := range values {
		if allowed[tag] && m.Success && m.SuccessRate > 0 && now.Sub(m.CheckedAt) <= maxAge {
			ranked = append(ranked, m)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i].DurationMS/ranked[i].SuccessRate, ranked[j].DurationMS/ranked[j].SuccessRate
		if a == b {
			return ranked[i].Node < ranked[j].Node
		}
		return a < b
	})
	return ranked
}

// PauseNewAssignments fences uncommitted runtime handoffs without blocking
// measurement updates or changing existing leases. Its release is idempotent.
func (s *Store) PauseNewAssignments() func() {
	s.mu.Lock()
	s.assignmentPauses++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.assignmentPauses--
			s.mu.Unlock()
		})
	}
}

// Acquire is idempotent even after expiry, policy changes, and process restarts.
// Expired records are retained until explicit release; they never silently rebind.
func (s *Store) Acquire(j config.JobConfig, policy, session string, ranked []SelectedNode) (Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := leaseKey(j.Name, session)
	if l, ok := s.leases[k]; ok {
		return l, nil
	}
	if s.assignmentPauses > 0 {
		return Lease{}, ErrUnavailable
	}
	if len(ranked) == 0 {
		return Lease{}, ErrUnavailable
	}
	count, loads := 0, make(map[string]int)
	for _, l := range s.leases {
		if l.Job == j.Name {
			count++
			if time.Now().Before(l.ExpiresAt) {
				loads[l.Node]++
			}
		}
	}
	if count >= j.MaxSessions || len(s.leases) >= 4096 {
		return Lease{}, fmt.Errorf("%w: release finished sessions before allocating more", ErrConflict)
	}
	node := ranked[0].Node
	for _, candidate := range ranked {
		if loads[candidate.Node] < loads[node] {
			node = candidate.Node
		}
	}
	l := Lease{Job: j.Name, Session: session, Node: node, Policy: policy, ExpiresAt: time.Now().Add(j.LeaseTTL())}
	s.leases[k] = l
	if err := s.saveLocked(); err != nil {
		delete(s.leases, k)
		return Lease{}, err
	}
	return l, nil
}
func (s *Store) Lease(job, session string) (Lease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseKey(job, session)]
	return l, ok
}

func (s *Store) Leases() []Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Lease, 0, len(s.leases))
	for _, lease := range s.leases {
		result = append(result, lease)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Job == result[j].Job {
			return result[i].Session < result[j].Session
		}
		return result[i].Job < result[j].Job
	})
	return result
}
func (s *Store) Release(job, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := leaseKey(job, session)
	l, ok := s.leases[k]
	if !ok {
		return ErrNotFound
	}
	delete(s.leases, k)
	if err := s.saveLocked(); err != nil {
		s.leases[k] = l
		return err
	}
	return nil
}
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.leases)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(s.path, data, 0600)
}
