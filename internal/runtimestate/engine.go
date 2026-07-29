package runtimestate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// HealthRecord is the storage-neutral health snapshot owned by the pool.
type HealthRecord struct {
	NodeID           string
	Failures         int
	BlacklistedUntil time.Time
	ManualBlacklist  bool
	CooldownUntil    time.Time
	MonitorJSON      []byte
	UpdatedAt        time.Time
}

// DomainLatencyRecord stores target-aware EWMA connection setup latency.
type DomainLatencyRecord struct {
	NodeID     string
	Domain     string
	EWMAMs     float64
	UpdatedAt  time.Time
	LastAccess time.Time
}

// Engine owns the process-wide weak runtime-state database.
type Engine struct {
	db   *sql.DB
	path string
}

// Open creates or migrates a runtime-state database. One connection keeps
// connection-local pragmas deterministic while WAL still allows readers.
func Open(path string) (*Engine, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil, errors.New("runtime state path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create runtime state directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open runtime state: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	engine := &Engine{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := engine.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return engine, nil
}

func (e *Engine) initialize(ctx context.Context) error {
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := e.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure runtime state (%s): %w", statement, err)
		}
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at_ns INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_health (
			node_id TEXT PRIMARY KEY,
			failures INTEGER NOT NULL DEFAULT 0,
			blacklisted_until_ns INTEGER NOT NULL DEFAULT 0,
			manual_blacklist INTEGER NOT NULL DEFAULT 0,
			cooldown_until_ns INTEGER NOT NULL DEFAULT 0,
			monitor_json BLOB NOT NULL DEFAULT '{}',
			updated_at_ns INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_domain_latency (
			node_id TEXT NOT NULL,
			domain TEXT NOT NULL,
			ewma_ms REAL NOT NULL,
			updated_at_ns INTEGER NOT NULL,
			last_access_ns INTEGER NOT NULL,
			PRIMARY KEY (node_id, domain)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_node_domain_latency_access
		ON node_domain_latency(node_id, last_access_ns DESC)`,
		`CREATE TABLE IF NOT EXISTS runtime_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at_ns INTEGER NOT NULL
		)`,
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime state migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate runtime state: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_migrations(version, applied_at_ns) VALUES (?, ?)",
		schemaVersion, time.Now().UTC().UnixNano(),
	); err != nil {
		return fmt.Errorf("record runtime state migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime state migration: %w", err)
	}
	return nil
}

// LoadHealth returns all persisted node-health records.
func (e *Engine) LoadHealth(ctx context.Context) (map[string]HealthRecord, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT node_id, failures, blacklisted_until_ns,
		manual_blacklist, cooldown_until_ns, monitor_json, updated_at_ns FROM node_health`)
	if err != nil {
		return nil, fmt.Errorf("load node health: %w", err)
	}
	defer rows.Close()
	records := make(map[string]HealthRecord)
	for rows.Next() {
		var record HealthRecord
		var blacklistedNS, cooldownNS, updatedNS int64
		if err := rows.Scan(&record.NodeID, &record.Failures, &blacklistedNS, &record.ManualBlacklist, &cooldownNS, &record.MonitorJSON, &updatedNS); err != nil {
			return nil, fmt.Errorf("scan node health: %w", err)
		}
		record.BlacklistedUntil = timeFromUnixNano(blacklistedNS)
		record.CooldownUntil = timeFromUnixNano(cooldownNS)
		record.UpdatedAt = timeFromUnixNano(updatedNS)
		records[record.NodeID] = record
	}
	return records, rows.Err()
}

// LoadDomainLatencies returns all bounded target-aware latency records.
func (e *Engine) LoadDomainLatencies(ctx context.Context) ([]DomainLatencyRecord, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT node_id, domain, ewma_ms,
		updated_at_ns, last_access_ns FROM node_domain_latency`)
	if err != nil {
		return nil, fmt.Errorf("load domain latency: %w", err)
	}
	defer rows.Close()
	var records []DomainLatencyRecord
	for rows.Next() {
		var record DomainLatencyRecord
		var updatedNS, accessNS int64
		if err := rows.Scan(&record.NodeID, &record.Domain, &record.EWMAMs, &updatedNS, &accessNS); err != nil {
			return nil, fmt.Errorf("scan domain latency: %w", err)
		}
		record.UpdatedAt = timeFromUnixNano(updatedNS)
		record.LastAccess = timeFromUnixNano(accessNS)
		records = append(records, record)
	}
	return records, rows.Err()
}

// SaveSnapshot atomically upserts coalesced health and domain-latency changes.
func (e *Engine) SaveSnapshot(ctx context.Context, health []HealthRecord, domains []DomainLatencyRecord) error {
	if len(health) == 0 && len(domains) == 0 {
		return nil
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime state snapshot: %w", err)
	}
	defer tx.Rollback()
	for _, record := range health {
		if strings.TrimSpace(record.NodeID) == "" {
			continue
		}
		if len(record.MonitorJSON) == 0 {
			record.MonitorJSON = []byte("{}")
		}
		if record.UpdatedAt.IsZero() {
			record.UpdatedAt = time.Now().UTC()
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO node_health (
			node_id, failures, blacklisted_until_ns, manual_blacklist,
			cooldown_until_ns, monitor_json, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			failures=excluded.failures,
			blacklisted_until_ns=excluded.blacklisted_until_ns,
			manual_blacklist=excluded.manual_blacklist,
			cooldown_until_ns=excluded.cooldown_until_ns,
			monitor_json=excluded.monitor_json,
			updated_at_ns=excluded.updated_at_ns`,
			record.NodeID, record.Failures, unixNano(record.BlacklistedUntil), record.ManualBlacklist,
			unixNano(record.CooldownUntil), record.MonitorJSON, unixNano(record.UpdatedAt))
		if err != nil {
			return fmt.Errorf("save node health %q: %w", record.NodeID, err)
		}
	}
	// Domain latencies are a complete bounded snapshot. Replacing them in the
	// same transaction removes entries evicted by the in-memory per-node LRU.
	if _, err := tx.ExecContext(ctx, "DELETE FROM node_domain_latency"); err != nil {
		return fmt.Errorf("replace domain latency snapshot: %w", err)
	}
	for _, record := range domains {
		if strings.TrimSpace(record.NodeID) == "" || strings.TrimSpace(record.Domain) == "" || record.EWMAMs <= 0 {
			continue
		}
		if record.UpdatedAt.IsZero() {
			record.UpdatedAt = time.Now().UTC()
		}
		if record.LastAccess.IsZero() {
			record.LastAccess = record.UpdatedAt
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO node_domain_latency (
			node_id, domain, ewma_ms, updated_at_ns, last_access_ns
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_id, domain) DO UPDATE SET
			ewma_ms=excluded.ewma_ms,
			updated_at_ns=excluded.updated_at_ns,
			last_access_ns=excluded.last_access_ns`,
			record.NodeID, record.Domain, record.EWMAMs, unixNano(record.UpdatedAt), unixNano(record.LastAccess))
		if err != nil {
			return fmt.Errorf("save domain latency %q/%q: %w", record.NodeID, record.Domain, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit runtime state snapshot: %w", err)
	}
	return nil
}

func unixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func timeFromUnixNano(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

// Path returns the selected database path.
func (e *Engine) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

// Close releases the database connection after the final synchronous flush.
func (e *Engine) Close() error {
	if e == nil || e.db == nil {
		return nil
	}
	return e.db.Close()
}
