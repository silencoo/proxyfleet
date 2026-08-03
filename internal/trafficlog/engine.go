package trafficlog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const (
	queueSize     = 4096
	maximumQuery  = 1000
	defaultQuery  = 200
	flushInterval = 250 * time.Millisecond
	maximumBatch  = 128
	schemaVersion = 1
)

var ErrDisabled = errors.New("traffic log is disabled")

type Config struct {
	Enabled           bool
	Path              string
	Retention         time.Duration
	MaxEntries        int
	RedactDestination bool
}

type Event struct {
	Timestamp     time.Time `json:"timestamp"`
	NodeID        string    `json:"node_id"`
	Profile       string    `json:"profile,omitempty"`
	Inbound       string    `json:"inbound,omitempty"`
	Destination   string    `json:"destination"`
	Network       string    `json:"network"`
	ConnectMS     int64     `json:"connect_ms"`
	TTFBMS        int64     `json:"ttfb_ms,omitempty"`
	DurationMS    int64     `json:"duration_ms"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
	Attempt       int       `json:"attempt"`
	Retried       bool      `json:"retried"`
	Success       bool      `json:"success"`
	ErrorCategory string    `json:"error_category,omitempty"`
}

type Query struct {
	Limit   int
	NodeID  string
	Profile string
	Success *bool
}

type Status struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
	Dropped uint64 `json:"dropped"`
}

type Engine struct {
	db      *sql.DB
	config  Config
	events  chan Event
	done    chan struct{}
	closed  atomic.Bool
	dropped atomic.Uint64
	close   sync.Once
	wg      sync.WaitGroup
}

var global struct {
	sync.RWMutex
	engine *Engine
}

func Configure(cfg Config) error {
	if !cfg.Enabled {
		return Close()
	}
	global.RLock()
	current := global.engine
	unchanged := current != nil && current.config == cfg && !current.closed.Load()
	global.RUnlock()
	if unchanged {
		return nil
	}
	next, err := Open(cfg)
	if err != nil {
		return err
	}
	global.Lock()
	previous := global.engine
	global.engine = next
	global.Unlock()
	if previous != nil {
		return previous.Close()
	}
	return nil
}

func Open(cfg Config) (*Engine, error) {
	cfg.Path = filepath.Clean(strings.TrimSpace(cfg.Path))
	if cfg.Path == "" || cfg.Path == "." {
		return nil, errors.New("traffic log path is empty")
	}
	if cfg.Retention <= 0 {
		cfg.Retention = 24 * time.Hour
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 100_000
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create traffic log directory: %w", err)
	}
	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open traffic log: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	engine := &Engine{db: db, config: cfg, events: make(chan Event, queueSize), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := engine.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(cfg.Path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure traffic log permissions: %w", err)
	}
	engine.wg.Add(1)
	go engine.run()
	return engine, nil
}

func (e *Engine) initialize(ctx context.Context) error {
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA busy_timeout=5000"} {
		if _, err := e.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure traffic log (%s): %w", statement, err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at_ns INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS traffic_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp_ns INTEGER NOT NULL,
			node_id TEXT NOT NULL,
			profile TEXT NOT NULL DEFAULT '',
			inbound TEXT NOT NULL DEFAULT '',
			destination TEXT NOT NULL DEFAULT '',
			network TEXT NOT NULL DEFAULT '',
			connect_ms INTEGER NOT NULL DEFAULT 0,
			ttfb_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			upload_bytes INTEGER NOT NULL DEFAULT 0,
			download_bytes INTEGER NOT NULL DEFAULT 0,
			attempt INTEGER NOT NULL DEFAULT 1,
			retried INTEGER NOT NULL DEFAULT 0,
			success INTEGER NOT NULL DEFAULT 0,
			error_category TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_events_time ON traffic_events(timestamp_ns DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_events_node_time ON traffic_events(node_id, timestamp_ns DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_events_profile_time ON traffic_events(profile, timestamp_ns DESC)`,
	} {
		if _, err := e.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate traffic log: %w", err)
		}
	}
	_, err := e.db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version, applied_at_ns) VALUES (?, ?)", schemaVersion, time.Now().UTC().UnixNano())
	return err
}

func Record(event Event) bool {
	global.RLock()
	engine := global.engine
	if engine == nil || engine.closed.Load() {
		global.RUnlock()
		return false
	}
	accepted := engine.record(event)
	global.RUnlock()
	return accepted
}

func Enabled() bool {
	global.RLock()
	enabled := global.engine != nil && !global.engine.closed.Load()
	global.RUnlock()
	return enabled
}

func (e *Engine) record(event Event) bool {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if e.config.RedactDestination {
		event.Destination = redactDestination(event.Destination)
	}
	event.NodeID = strings.TrimSpace(event.NodeID)
	event.Profile = strings.ToLower(strings.TrimSpace(event.Profile))
	event.Inbound = strings.TrimSpace(event.Inbound)
	event.Network = strings.ToLower(strings.TrimSpace(event.Network))
	select {
	case e.events <- event:
		return true
	default:
		e.dropped.Add(1)
		return false
	}
}

func redactDestination(destination string) string {
	destination = strings.TrimSpace(destination)
	host, port, err := net.SplitHostPort(destination)
	if err != nil {
		host = destination
		port = ""
	}
	digest := sha256.Sum256([]byte(strings.ToLower(strings.Trim(host, "[]"))))
	masked := "sha256:" + hex.EncodeToString(digest[:8])
	if port != "" {
		return net.JoinHostPort(masked, port)
	}
	return masked
}

func (e *Engine) run() {
	defer e.wg.Done()
	flushTicker := time.NewTicker(flushInterval)
	cleanupTicker := time.NewTicker(time.Hour)
	defer flushTicker.Stop()
	defer cleanupTicker.Stop()
	batch := make([]Event, 0, maximumBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.insertBatch(ctx, batch); err != nil {
			e.dropped.Add(uint64(len(batch)))
		}
		cancel()
		batch = batch[:0]
	}
	for {
		select {
		case event := <-e.events:
			batch = append(batch, event)
			if len(batch) >= maximumBatch {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-cleanupTicker.C:
			flush()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = e.cleanup(ctx)
			cancel()
		case <-e.done:
			for {
				select {
				case event := <-e.events:
					batch = append(batch, event)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (e *Engine) insertBatch(ctx context.Context, events []Event) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `INSERT INTO traffic_events (
		timestamp_ns, node_id, profile, inbound, destination, network, connect_ms,
		ttfb_ms, duration_ms, upload_bytes, download_bytes, attempt, retried, success, error_category
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, event := range events {
		if _, err := statement.ExecContext(ctx, event.Timestamp.UTC().UnixNano(), event.NodeID, event.Profile, event.Inbound,
			event.Destination, event.Network, event.ConnectMS, event.TTFBMS, event.DurationMS, event.UploadBytes,
			event.DownloadBytes, event.Attempt, event.Retried, event.Success, event.ErrorCategory); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (e *Engine) cleanup(ctx context.Context) error {
	cutoff := time.Now().Add(-e.config.Retention).UTC().UnixNano()
	if _, err := e.db.ExecContext(ctx, "DELETE FROM traffic_events WHERE timestamp_ns < ?", cutoff); err != nil {
		return err
	}
	_, err := e.db.ExecContext(ctx, `DELETE FROM traffic_events WHERE id NOT IN (
		SELECT id FROM traffic_events ORDER BY timestamp_ns DESC LIMIT ?
	)`, e.config.MaxEntries)
	return err
}

func QueryEvents(ctx context.Context, query Query) ([]Event, error) {
	global.RLock()
	defer global.RUnlock()
	if global.engine == nil {
		return nil, ErrDisabled
	}
	return global.engine.Query(ctx, query)
}

func (e *Engine) Query(ctx context.Context, query Query) ([]Event, error) {
	if query.Limit <= 0 {
		query.Limit = defaultQuery
	} else if query.Limit > maximumQuery {
		query.Limit = maximumQuery
	}
	clauses := []string{"1=1"}
	args := make([]any, 0, 4)
	if query.NodeID = strings.TrimSpace(query.NodeID); query.NodeID != "" {
		clauses = append(clauses, "node_id = ?")
		args = append(args, query.NodeID)
	}
	if query.Profile = strings.ToLower(strings.TrimSpace(query.Profile)); query.Profile != "" {
		clauses = append(clauses, "profile = ?")
		args = append(args, query.Profile)
	}
	if query.Success != nil {
		clauses = append(clauses, "success = ?")
		args = append(args, *query.Success)
	}
	args = append(args, query.Limit)
	rows, err := e.db.QueryContext(ctx, `SELECT timestamp_ns, node_id, profile, inbound, destination,
		network, connect_ms, ttfb_ms, duration_ms, upload_bytes, download_bytes, attempt,
		retried, success, error_category FROM traffic_events WHERE `+strings.Join(clauses, " AND ")+` ORDER BY timestamp_ns DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0, query.Limit)
	for rows.Next() {
		var event Event
		var timestamp int64
		if err := rows.Scan(&timestamp, &event.NodeID, &event.Profile, &event.Inbound, &event.Destination,
			&event.Network, &event.ConnectMS, &event.TTFBMS, &event.DurationMS, &event.UploadBytes,
			&event.DownloadBytes, &event.Attempt, &event.Retried, &event.Success, &event.ErrorCategory); err != nil {
			return nil, err
		}
		event.Timestamp = time.Unix(0, timestamp).UTC()
		events = append(events, event)
	}
	return events, rows.Err()
}

func Clear(ctx context.Context) error {
	global.RLock()
	defer global.RUnlock()
	if global.engine == nil {
		return ErrDisabled
	}
	_, err := global.engine.db.ExecContext(ctx, "DELETE FROM traffic_events")
	return err
}

func CurrentStatus() Status {
	global.RLock()
	defer global.RUnlock()
	if global.engine == nil {
		return Status{}
	}
	return Status{Enabled: true, Path: global.engine.config.Path, Dropped: global.engine.dropped.Load()}
}

func Close() error {
	global.Lock()
	engine := global.engine
	global.engine = nil
	global.Unlock()
	if engine == nil {
		return nil
	}
	return engine.Close()
}

func (e *Engine) Close() error {
	if e == nil {
		return nil
	}
	var err error
	e.close.Do(func() {
		e.closed.Store(true)
		close(e.done)
		e.wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = e.cleanup(ctx)
		cancel()
		err = e.db.Close()
	})
	return err
}
