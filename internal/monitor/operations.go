package monitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func roleRank(role Role) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	default:
		return 0
	}
}

func roleAllows(actual, required Role) bool {
	return roleRank(actual) >= roleRank(required)
}

// MetricPoint is one bounded aggregate sample. It intentionally contains no
// node URI, credentials, destinations, or request data.
type MetricPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	TotalNodes          int       `json:"total_nodes"`
	HealthyNodes        int       `json:"healthy_nodes"`
	UnavailableNodes    int       `json:"unavailable_nodes"`
	BlacklistedNodes    int       `json:"blacklisted_nodes"`
	ActiveConnections   int64     `json:"active_connections"`
	AverageLatencyMs    float64   `json:"average_latency_ms"`
	P95LatencyMs        int64     `json:"p95_latency_ms"`
	AverageQualityScore float64   `json:"average_quality_score"`
	ProbeBudgetUsed     int       `json:"probe_budget_used"`
	ProbeBudgetLimit    int       `json:"probe_budget_limit"`
}

type Alert struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Severity   string     `json:"severity"`
	Message    string     `json:"message"`
	Active     bool       `json:"active"`
	StartedAt  time.Time  `json:"started_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Role      Role      `json:"role"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	RemoteIP  string    `json:"remote_ip,omitempty"`
}

type operationsState struct {
	Version int           `json:"version"`
	History []MetricPoint `json:"history"`
	Alerts  []Alert       `json:"alerts"`
}

const (
	operationsStateVersion = 1
	maximumHistoryPoints   = 10000
	maximumAlerts          = 200
)

// OperationsStore owns bounded history, local alerts and the management audit
// trail. History persistence is best effort and never blocks proxy routing.
type OperationsStore struct {
	mu sync.RWMutex

	historyEnabled bool
	historyPath    string
	retention      time.Duration
	interval       time.Duration
	alertMinimum   int
	alertRatio     float64
	alertCooldown  time.Duration
	history        []MetricPoint
	alerts         []Alert

	audit *AuditLog
}

func NewOperationsStore(cfg Config) *OperationsStore {
	retention := cfg.HistoryRetention
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	interval := cfg.HistoryInterval
	if interval < 10*time.Second {
		interval = time.Minute
	}
	cooldown := cfg.AlertCooldown
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	store := &OperationsStore{
		historyEnabled: cfg.HistoryEnabled,
		historyPath:    strings.TrimSpace(cfg.HistoryFile),
		retention:      retention,
		interval:       interval,
		alertMinimum:   cfg.AlertMinAvailable,
		alertRatio:     cfg.AlertMinAvailableRatio,
		alertCooldown:  cooldown,
		history:        make([]MetricPoint, 0, 256),
		alerts:         make([]Alert, 0, 16),
		audit:          NewAuditLog(cfg.AuditFile, cfg.AuditMaxEntries),
	}
	store.load()
	return store
}

func (s *OperationsStore) Start(manager *Manager) {
	if s == nil || manager == nil || !s.historyEnabled {
		return
	}
	go func() {
		s.record(manager, time.Now())
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-manager.ctx.Done():
				return
			case now := <-ticker.C:
				s.record(manager, now)
			}
		}
	}()
}

func (s *OperationsStore) record(manager *Manager, now time.Time) {
	snapshots := manager.Snapshot()
	point := metricPointFromSnapshots(snapshots, manager.ProbeBudgetStatus(), now)
	cutoff := now.Add(-s.retention)

	s.mu.Lock()
	s.history = append(s.history, point)
	first := 0
	for first < len(s.history) && s.history[first].Timestamp.Before(cutoff) {
		first++
	}
	if first > 0 {
		s.history = append([]MetricPoint(nil), s.history[first:]...)
	}
	if len(s.history) > maximumHistoryPoints {
		s.history = append([]MetricPoint(nil), s.history[len(s.history)-maximumHistoryPoints:]...)
	}
	s.evaluateAlertsLocked(point, now)
	snapshot := operationsState{Version: operationsStateVersion, History: append([]MetricPoint(nil), s.history...), Alerts: append([]Alert(nil), s.alerts...)}
	s.mu.Unlock()

	s.persist(snapshot)
}

func metricPointFromSnapshots(snapshots []Snapshot, budget ProbeBudgetStatus, now time.Time) MetricPoint {
	point := MetricPoint{Timestamp: now, TotalNodes: len(snapshots), ProbeBudgetUsed: budget.Used, ProbeBudgetLimit: budget.Limit}
	latencies := make([]int64, 0, len(snapshots))
	qualityTotal := 0.0
	for _, snapshot := range snapshots {
		if snapshot.InitialCheckDone && snapshot.Available && !snapshot.Blacklisted && !snapshot.CoolingDown {
			point.HealthyNodes++
		} else if snapshot.InitialCheckDone {
			point.UnavailableNodes++
		}
		if snapshot.Blacklisted || snapshot.CoolingDown {
			point.BlacklistedNodes++
		}
		point.ActiveConnections += int64(snapshot.ActiveConnections)
		if snapshot.LastLatencyMs > 0 {
			latencies = append(latencies, snapshot.LastLatencyMs)
		}
		qualityTotal += snapshot.QualityScore
	}
	if len(snapshots) > 0 {
		point.AverageQualityScore = qualityTotal / float64(len(snapshots))
	}
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		var total int64
		for _, latency := range latencies {
			total += latency
		}
		point.AverageLatencyMs = float64(total) / float64(len(latencies))
		index := (95*len(latencies) + 99) / 100
		if index < 1 {
			index = 1
		}
		point.P95LatencyMs = latencies[index-1]
	}
	return point
}

func (s *OperationsStore) evaluateAlertsLocked(point MetricPoint, now time.Time) {
	if point.TotalNodes == 0 || (s.alertMinimum <= 0 && s.alertRatio <= 0) {
		s.resolveAlertLocked("available_nodes_low", now)
		return
	}
	ratio := float64(point.HealthyNodes) / float64(point.TotalNodes)
	triggered := (s.alertMinimum > 0 && point.HealthyNodes < s.alertMinimum) || (s.alertRatio > 0 && ratio < s.alertRatio)
	if !triggered {
		s.resolveAlertLocked("available_nodes_low", now)
		return
	}
	message := fmt.Sprintf("可用节点 %d/%d（%.1f%%）低于告警门槛", point.HealthyNodes, point.TotalNodes, ratio*100)
	for index := range s.alerts {
		if s.alerts[index].ID != "available_nodes_low" || !s.alerts[index].Active {
			continue
		}
		if now.Sub(s.alerts[index].UpdatedAt) >= s.alertCooldown {
			s.alerts[index].UpdatedAt = now
			s.alerts[index].Message = message
		}
		return
	}
	s.alerts = append(s.alerts, Alert{ID: "available_nodes_low", Type: "availability", Severity: "critical", Message: message, Active: true, StartedAt: now, UpdatedAt: now})
	if len(s.alerts) > maximumAlerts {
		s.alerts = append([]Alert(nil), s.alerts[len(s.alerts)-maximumAlerts:]...)
	}
}

func (s *OperationsStore) resolveAlertLocked(id string, now time.Time) {
	for index := range s.alerts {
		if s.alerts[index].ID == id && s.alerts[index].Active {
			s.alerts[index].Active = false
			s.alerts[index].UpdatedAt = now
			resolved := now
			s.alerts[index].ResolvedAt = &resolved
		}
	}
}

func (s *OperationsStore) History(limit int) []MetricPoint {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.history) {
		limit = len(s.history)
	}
	return append([]MetricPoint(nil), s.history[len(s.history)-limit:]...)
}

func (s *OperationsStore) Alerts(activeOnly bool) []Alert {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	alerts := make([]Alert, 0, len(s.alerts))
	for index := len(s.alerts) - 1; index >= 0; index-- {
		if activeOnly && !s.alerts[index].Active {
			continue
		}
		alerts = append(alerts, s.alerts[index])
	}
	return alerts
}

func (s *OperationsStore) Audit() *AuditLog {
	if s == nil {
		return nil
	}
	return s.audit
}

func (s *OperationsStore) load() {
	if s == nil || !s.historyEnabled || s.historyPath == "" {
		return
	}
	data, err := os.ReadFile(s.historyPath)
	if err != nil {
		return
	}
	var state operationsState
	if json.Unmarshal(data, &state) != nil || (state.Version != 0 && state.Version != operationsStateVersion) {
		return
	}
	cutoff := time.Now().Add(-s.retention)
	for _, point := range state.History {
		if !point.Timestamp.Before(cutoff) {
			s.history = append(s.history, point)
		}
	}
	if len(s.history) > maximumHistoryPoints {
		s.history = append([]MetricPoint(nil), s.history[len(s.history)-maximumHistoryPoints:]...)
	}
	if len(state.Alerts) > maximumAlerts {
		state.Alerts = state.Alerts[len(state.Alerts)-maximumAlerts:]
	}
	s.alerts = append(s.alerts, state.Alerts...)
}

func (s *OperationsStore) persist(state operationsState) {
	if s == nil || !s.historyEnabled || s.historyPath == "" {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(s.historyPath), 0o755) != nil {
		return
	}
	temporary := s.historyPath + ".tmp"
	if os.WriteFile(temporary, data, 0o600) != nil {
		return
	}
	_ = os.Chmod(temporary, 0o600)
	if os.Rename(temporary, s.historyPath) != nil {
		_ = os.Remove(s.historyPath)
		_ = os.Rename(temporary, s.historyPath)
	}
}

type AuditLog struct {
	mu         sync.RWMutex
	ioMu       sync.Mutex
	path       string
	maxEntries int
	events     []AuditEvent
}

func NewAuditLog(path string, maxEntries int) *AuditLog {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	log := &AuditLog{path: strings.TrimSpace(path), maxEntries: maxEntries, events: make([]AuditEvent, 0, maxEntries)}
	log.load()
	return log
}

func (l *AuditLog) Record(event AuditEvent) {
	if l == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	event.Path = strings.TrimSpace(strings.SplitN(event.Path, "?", 2)[0])
	l.ioMu.Lock()
	defer l.ioMu.Unlock()
	l.mu.Lock()
	rewrite := len(l.events) >= l.maxEntries
	l.events = append(l.events, event)
	if len(l.events) > l.maxEntries {
		l.events = append([]AuditEvent(nil), l.events[len(l.events)-l.maxEntries:]...)
	}
	path := l.path
	snapshot := append([]AuditEvent(nil), l.events...)
	l.mu.Unlock()
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	if rewrite {
		writeAuditSnapshot(path, snapshot)
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
	_ = file.Close()
}

func writeAuditSnapshot(path string, events []AuditEvent) {
	var builder strings.Builder
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	temporary := path + ".tmp"
	if os.WriteFile(temporary, []byte(builder.String()), 0o600) != nil {
		return
	}
	_ = os.Chmod(temporary, 0o600)
	if os.Rename(temporary, path) != nil {
		_ = os.Remove(path)
		_ = os.Rename(temporary, path)
	}
}

func (l *AuditLog) Events(limit int) []AuditEvent {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if limit <= 0 || limit > len(l.events) {
		limit = len(l.events)
	}
	result := make([]AuditEvent, 0, limit)
	for index := len(l.events) - 1; index >= len(l.events)-limit; index-- {
		result = append(result, l.events[index])
	}
	return result
}

func (l *AuditLog) load() {
	if l == nil || l.path == "" {
		return
	}
	file, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var event AuditEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			l.events = append(l.events, event)
			if len(l.events) > l.maxEntries {
				l.events = l.events[1:]
			}
		}
	}
}
func (m *Manager) MetricsHistory(limit int) []MetricPoint {
	if m == nil || m.operations == nil {
		return nil
	}
	return m.operations.History(limit)
}

func (m *Manager) Alerts(activeOnly bool) []Alert {
	if m == nil || m.operations == nil {
		return nil
	}
	return m.operations.Alerts(activeOnly)
}

func (m *Manager) AuditLog() *AuditLog {
	if m == nil || m.operations == nil {
		return nil
	}
	return m.operations.Audit()
}
