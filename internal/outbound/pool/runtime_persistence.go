package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silencoo/proxyfleet/internal/runtimestate"

	"gopkg.in/yaml.v3"
)

const maxDomainLatencyEntriesPerNode = 16

type domainLatencyValue struct {
	EWMAMs     float64
	UpdatedAt  time.Time
	LastAccess time.Time
}

// ConfigureRuntimeState selects the SQLite runtime engine and imports the
// legacy YAML health sidecar once when the database is still empty.
func ConfigureRuntimeState(databasePath, legacyHealthPath string) error {
	databasePath = filepath.Clean(strings.TrimSpace(databasePath))
	if databasePath == "" || databasePath == "." {
		return errors.New("runtime state database path is empty")
	}
	healthPersistence.mu.Lock()
	if healthPersistence.runtimePath == databasePath && healthPersistence.engine != nil {
		healthPersistence.mu.Unlock()
		return nil
	}
	healthPersistence.mu.Unlock()

	if err := FlushHealthState(); err != nil {
		return fmt.Errorf("flush previous runtime state: %w", err)
	}
	engine, err := runtimestate.Open(databasePath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthRows, err := engine.LoadHealth(ctx)
	if err != nil {
		_ = engine.Close()
		return err
	}
	domainRows, err := engine.LoadDomainLatencies(ctx)
	if err != nil {
		_ = engine.Close()
		return err
	}

	records := make(map[string]persistedMemberHealth, len(healthRows))
	for tag, row := range healthRows {
		record, err := decodeRuntimeHealth(row)
		if err != nil {
			_ = engine.Close()
			return fmt.Errorf("decode runtime health %q: %w", tag, err)
		}
		records[tag] = sanitizeRestoredHealth(record, time.Now())
	}
	imported := false
	if len(records) == 0 {
		legacy, loadErr := loadLegacyHealthRecords(legacyHealthPath)
		if loadErr != nil {
			_ = engine.Close()
			return loadErr
		}
		if len(legacy) > 0 {
			records = legacy
			if err := saveRuntimeSnapshot(ctx, engine, records, nil); err != nil {
				_ = engine.Close()
				return fmt.Errorf("import legacy health state: %w", err)
			}
			imported = true
		}
	}
	domains := make(map[string]map[string]domainLatencyValue)
	for _, row := range domainRows {
		if row.NodeID == "" || row.Domain == "" || row.EWMAMs <= 0 {
			continue
		}
		if domains[row.NodeID] == nil {
			domains[row.NodeID] = make(map[string]domainLatencyValue)
		}
		domains[row.NodeID][row.Domain] = domainLatencyValue{EWMAMs: row.EWMAMs, UpdatedAt: row.UpdatedAt, LastAccess: row.LastAccess}
	}
	pruneDomainLatencyState(domains)

	healthPersistence.writeMu.Lock()
	healthPersistence.mu.Lock()
	oldEngine := healthPersistence.engine
	oldRuntimePath := healthPersistence.runtimePath
	pathChanged := oldRuntimePath != databasePath
	// Database loading happens outside the state mutex. Overlay any live state
	// recorded during that window so a configuration reload cannot lose traffic
	// health or target-latency samples. The newest timestamp wins.
	for tag, current := range healthPersistence.records {
		loaded, exists := records[tag]
		if !exists || current.UpdatedAt.After(loaded.UpdatedAt) {
			records[tag] = current
		}
	}
	for tag, currentDomains := range healthPersistence.domains {
		if domains[tag] == nil {
			domains[tag] = make(map[string]domainLatencyValue)
		}
		for domain, current := range currentDomains {
			loaded, exists := domains[tag][domain]
			if !exists || current.UpdatedAt.After(loaded.UpdatedAt) {
				domains[tag][domain] = current
			}
		}
	}
	pruneDomainLatencyState(domains)
	if healthPersistence.timer != nil {
		healthPersistence.timer.Stop()
		healthPersistence.timer = nil
	}
	healthPersistence.engine = engine
	healthPersistence.runtimePath = databasePath
	healthPersistence.path = filepath.Clean(strings.TrimSpace(legacyHealthPath))
	healthPersistence.records = records
	healthPersistence.domains = domains
	healthPersistence.dirty = healthPersistence.dirty || (pathChanged && len(healthPersistence.records) > 0)
	healthPersistence.domainDirty = healthPersistence.domainDirty || (pathChanged && len(healthPersistence.domains) > 0)
	needsFlush := healthPersistence.dirty || healthPersistence.domainDirty
	healthPersistence.mu.Unlock()
	healthPersistence.writeMu.Unlock()
	if oldEngine != nil {
		_ = oldEngine.Close()
	}
	if needsFlush {
		if err := FlushHealthState(); err != nil {
			return fmt.Errorf("persist runtime state after path change: %w", err)
		}
	}
	if imported {
		log.Printf("[pool] imported legacy health state into %s", databasePath)
	}
	return nil
}

func loadLegacyHealthRecords(path string) (map[string]persistedMemberHealth, error) {
	records := make(map[string]persistedMemberHealth)
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return records, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy health state: %w", err)
	}
	var state persistedHealthFile
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode legacy health state: %w", err)
	}
	if state.Version != 0 && state.Version != 1 && state.Version != 2 && state.Version != healthStateVersion {
		return nil, errors.New("unsupported health state version")
	}
	now := time.Now()
	for tag, record := range state.Nodes {
		if strings.TrimSpace(tag) != "" {
			records[tag] = sanitizeRestoredHealth(record, now)
		}
	}
	return records, nil
}

func sanitizeRestoredHealth(record persistedMemberHealth, now time.Time) persistedMemberHealth {
	if !record.BlacklistedUntil.After(now) {
		record.BlacklistedUntil = time.Time{}
		record.Monitor.BlacklistedUntil = time.Time{}
	}
	if !record.CooldownUntil.After(now) {
		record.CooldownUntil = time.Time{}
		record.Monitor.CooldownUntil = time.Time{}
	}
	return record
}

func decodeRuntimeHealth(row runtimestate.HealthRecord) (persistedMemberHealth, error) {
	record := persistedMemberHealth{
		Failures: row.Failures, BlacklistedUntil: row.BlacklistedUntil,
		ManualBlacklist: row.ManualBlacklist, CooldownUntil: row.CooldownUntil,
		UpdatedAt: row.UpdatedAt,
	}
	if len(row.MonitorJSON) > 0 {
		if err := json.Unmarshal(row.MonitorJSON, &record.Monitor); err != nil {
			return persistedMemberHealth{}, err
		}
	}
	return record, nil
}

func saveRuntimeSnapshot(ctx context.Context, engine *runtimestate.Engine, records map[string]persistedMemberHealth, domains map[string]map[string]domainLatencyValue) error {
	if engine == nil {
		return nil
	}
	healthRows := make([]runtimestate.HealthRecord, 0, len(records))
	for tag, record := range records {
		monitorJSON, err := json.Marshal(record.Monitor)
		if err != nil {
			return fmt.Errorf("encode monitor state %q: %w", tag, err)
		}
		healthRows = append(healthRows, runtimestate.HealthRecord{
			NodeID: tag, Failures: record.Failures, BlacklistedUntil: record.BlacklistedUntil,
			ManualBlacklist: record.ManualBlacklist, CooldownUntil: record.CooldownUntil,
			MonitorJSON: monitorJSON, UpdatedAt: record.UpdatedAt,
		})
	}
	domainRows := make([]runtimestate.DomainLatencyRecord, 0)
	for tag, nodeDomains := range domains {
		for domain, value := range nodeDomains {
			domainRows = append(domainRows, runtimestate.DomainLatencyRecord{
				NodeID: tag, Domain: domain, EWMAMs: value.EWMAMs,
				UpdatedAt: value.UpdatedAt, LastAccess: value.LastAccess,
			})
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return engine.SaveSnapshot(ctx, healthRows, domainRows)
}

func cloneDomainLatencyState(source map[string]map[string]domainLatencyValue) map[string]map[string]domainLatencyValue {
	result := make(map[string]map[string]domainLatencyValue, len(source))
	for tag, values := range source {
		clone := make(map[string]domainLatencyValue, len(values))
		for domain, value := range values {
			clone[domain] = value
		}
		result[tag] = clone
	}
	return result
}

func pruneDomainLatencyState(state map[string]map[string]domainLatencyValue) {
	for _, values := range state {
		for len(values) > maxDomainLatencyEntriesPerNode {
			oldestKey := ""
			var oldest time.Time
			for key, value := range values {
				if oldestKey == "" || value.LastAccess.Before(oldest) {
					oldestKey, oldest = key, value.LastAccess
				}
			}
			delete(values, oldestKey)
		}
	}
}

func recordDomainLatency(tag, domain string, latency time.Duration) {
	tag = strings.TrimSpace(tag)
	domain = strings.ToLower(strings.TrimSpace(domain))
	if tag == "" || domain == "" || latency <= 0 {
		return
	}
	now := time.Now().UTC()
	healthPersistence.mu.Lock()
	if healthPersistence.domains == nil {
		healthPersistence.domains = make(map[string]map[string]domainLatencyValue)
	}
	values := healthPersistence.domains[tag]
	if values == nil {
		values = make(map[string]domainLatencyValue)
		healthPersistence.domains[tag] = values
	}
	value := values[domain]
	measurement := float64(latency) / float64(time.Millisecond)
	if value.EWMAMs <= 0 {
		value.EWMAMs = measurement
	} else {
		const alpha = 0.25
		value.EWMAMs = alpha*measurement + (1-alpha)*value.EWMAMs
	}
	value.UpdatedAt = now
	value.LastAccess = now
	values[domain] = value
	pruneDomainLatencyState(map[string]map[string]domainLatencyValue{tag: values})
	healthPersistence.domainDirty = true
	if healthPersistence.engine != nil && healthPersistence.timer == nil {
		healthPersistence.timer = time.AfterFunc(healthWriteDelay, func() {
			if err := FlushHealthState(); err != nil {
				log.Printf("[pool] persist runtime state: %v", err)
			}
		})
	}
	healthPersistence.mu.Unlock()
}

func domainLatency(tag, domain string) time.Duration {
	if tag == "" || domain == "" {
		return 0
	}
	healthPersistence.mu.Lock()
	value := healthPersistence.domains[tag][strings.ToLower(domain)]
	healthPersistence.mu.Unlock()
	if value.EWMAMs <= 0 {
		return 0
	}
	return time.Duration(value.EWMAMs * float64(time.Millisecond))
}

// CloseRuntimeState flushes and closes SQLite during process shutdown.
func CloseRuntimeState() error {
	flushErr := PersistHealthStateNow()
	healthPersistence.writeMu.Lock()
	healthPersistence.mu.Lock()
	engine := healthPersistence.engine
	healthPersistence.engine = nil
	healthPersistence.runtimePath = ""
	healthPersistence.path = ""
	healthPersistence.mu.Unlock()
	healthPersistence.writeMu.Unlock()
	var closeErr error
	if engine != nil {
		closeErr = engine.Close()
	}
	return errors.Join(flushErr, closeErr)
}
