package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"easy_proxies/internal/config"
)

type settingsTransactionNodeManager struct {
	mu               sync.Mutex
	cfg              *config.Config
	revision         uint64
	conflictOnce     bool
	concurrentUpdate func(*config.Config)
	endpointStatuses []EndpointRuntimeStatus
}

func (m *settingsTransactionNodeManager) ListConfigNodes(context.Context) ([]config.NodeConfig, error) {
	return nil, nil
}
func (m *settingsTransactionNodeManager) CreateNode(context.Context, config.NodeConfig) (config.NodeConfig, error) {
	return config.NodeConfig{}, errors.New("not implemented")
}
func (m *settingsTransactionNodeManager) UpdateNode(context.Context, string, config.NodeConfig) (config.NodeConfig, error) {
	return config.NodeConfig{}, errors.New("not implemented")
}
func (m *settingsTransactionNodeManager) DeleteNode(context.Context, string) error {
	return errors.New("not implemented")
}
func (m *settingsTransactionNodeManager) TriggerReload(context.Context) error { return nil }
func (m *settingsTransactionNodeManager) EndpointStatuses() []EndpointRuntimeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]EndpointRuntimeStatus(nil), m.endpointStatuses...)
}
func (m *settingsTransactionNodeManager) ConfigSnapshot() (*config.Config, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Clone(), m.revision
}
func (m *settingsTransactionNodeManager) CommitConfig(
	_ context.Context,
	expectedRevision uint64,
	candidate *config.Config,
	persist func(*config.Config) (func() error, error),
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conflictOnce {
		m.conflictOnce = false
		if m.concurrentUpdate != nil {
			m.concurrentUpdate(m.cfg)
		}
		m.revision++
		return errors.New("revision changed")
	}
	if expectedRevision != m.revision {
		return errors.New("revision changed")
	}
	if persist != nil {
		if _, err := persist(candidate.Clone()); err != nil {
			return err
		}
	}
	m.cfg = candidate.Clone()
	m.revision++
	return nil
}

func newSettingsTransactionConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := loadSubscriptionSettingsConfig(t, 4)
	cfg.ExternalIP = "198.51.100.10"
	cfg.Management.Password = "old-password"
	cfg.Management.ProbeTarget = "example.com:80"
	cfg.GeoIP.Enabled = true
	cfg.GeoIP.DatabasePath = filepath.Join(t.TempDir(), "geo.mmdb")
	cfg.GeoIP.ExitIPConcurrency = 17
	cfg.GeoIP.AutoUpdateEnabled = true
	if err := cfg.SaveSettings(); err != nil {
		t.Fatalf("save fixture settings: %v", err)
	}
	return cfg
}

func newSettingsTransactionServer(cfg *config.Config, manager NodeManager) *Server {
	server := &Server{nodeMgr: manager}
	server.SetConfig(cfg)
	return server
}

func settingsPutRequest(body *bytes.Buffer, revision uint64) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	request.Header.Set("If-Match", settingsETag(revision))
	return request
}

func TestParsePositiveSettingsDuration(t *testing.T) {
	got, err := parsePositiveSettingsDuration(" 50ms ")
	if err != nil || got != 50*time.Millisecond {
		t.Fatalf("valid duration rejected: duration=%v err=%v", got, err)
	}
	for _, value := range []string{"", "0s", "-1m", "tomorrow"} {
		if _, err := parsePositiveSettingsDuration(value); err == nil {
			t.Fatalf("invalid duration %q accepted", value)
		}
	}
}

func TestValidateProbeTarget(t *testing.T) {
	for _, value := range []string{"example.com:80", "[::1]:443", "http://example.com/path", "https://example.com:8443/health"} {
		if err := validateProbeTarget(value); err != nil {
			t.Errorf("valid target %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "example.com", "ftp://example.com/file", "http://user@example.com/", "http://:80/", "example.com:0", "example.com:65536", "http://example.com:99999/", "example.com:80\r\nHost:evil"} {
		if err := validateProbeTarget(value); err == nil {
			t.Errorf("invalid target %q accepted", value)
		}
	}
}

func TestSettingsPartialUpdatePreservesUnrelatedConfiguration(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 7}
	server := newSettingsTransactionServer(cfg, manager)
	body := bytes.NewBufferString(`{"log":{"output":"stdout","max_size":20,"max_backups":4,"max_age":8,"compress":true}}`)
	recorder := httptest.NewRecorder()

	server.handleSettings(recorder, settingsPutRequest(body, 7))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if response["need_restart"] != true {
		t.Fatalf("log configuration change need_restart=%v, want true", response["need_restart"])
	}
	committed, _ := manager.ConfigSnapshot()
	if committed.ExternalIP != cfg.ExternalIP || committed.Management.Password != cfg.Management.Password {
		t.Fatalf("unrelated core settings changed: %#v", committed)
	}
	if committed.GeoIP.DatabasePath != cfg.GeoIP.DatabasePath || committed.GeoIP.ExitIPConcurrency != 17 || !committed.GeoIP.Enabled {
		t.Fatalf("GeoIP settings changed: %#v", committed.GeoIP)
	}
	if committed.Log.Output != "stdout" || committed.Log.MaxSize != 20 || committed.Log.MaxBackups != 4 || committed.Log.MaxAge != 8 || !committed.Log.Compress {
		t.Fatalf("log settings not committed: %#v", committed.Log)
	}
	disk, err := config.Load(cfg.FilePath())
	if err != nil {
		t.Fatalf("reload persisted config: %v", err)
	}
	if disk.GeoIP.DatabasePath != cfg.GeoIP.DatabasePath || disk.Management.Password != cfg.Management.Password {
		t.Fatalf("disk update lost unrelated settings: %#v", disk)
	}
}

func TestSettingsTrafficLogUpdatePersistsTransactionally(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 12}
	server := newSettingsTransactionServer(cfg, manager)
	body := bytes.NewBufferString(`{"traffic_log":{"enabled":true,"file":"history/traffic.db","retention":"2h","max_entries":4321,"redact_destination":false}}`)
	recorder := httptest.NewRecorder()

	server.handleSettings(recorder, settingsPutRequest(body, 12))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	committed, _ := manager.ConfigSnapshot()
	if !committed.TrafficLog.Enabled || committed.TrafficLog.Retention != 2*time.Hour || committed.TrafficLog.MaxEntries != 4321 || committed.TrafficLog.RedactDestinationValue() {
		t.Fatalf("traffic log settings not committed: %#v", committed.TrafficLog)
	}
	disk, err := config.Load(cfg.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if disk.TrafficLog.File != "history/traffic.db" || disk.TrafficLog.MaxEntries != 4321 {
		t.Fatalf("traffic log settings not persisted: %#v", disk.TrafficLog)
	}
}

func TestSettingsPersistenceFailureDoesNotChangeLiveConfiguration(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	missing := cfg.Clone()
	missing.SetFilePath(filepath.Join(t.TempDir(), "missing", "config.yaml"))
	manager := &settingsTransactionNodeManager{cfg: missing, revision: 1}
	server := newSettingsTransactionServer(missing, manager)
	recorder := httptest.NewRecorder()

	server.handleSettings(recorder, settingsPutRequest(bytes.NewBufferString(`{"external_ip":"203.0.113.20"}`), 1))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	committed, revision := manager.ConfigSnapshot()
	if revision != 1 || committed.ExternalIP != cfg.ExternalIP {
		t.Fatalf("failed persistence changed live config: revision=%d external=%q", revision, committed.ExternalIP)
	}
	if server.cfgSrc.ExternalIP != cfg.ExternalIP {
		t.Fatalf("failed persistence changed server config: %q", server.cfgSrc.ExternalIP)
	}
}

func TestSettingsRollbackDoesNotOverwriteNewerExternalWrite(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	candidate := cfg.Clone()
	candidate.ExternalIP = "203.0.113.77"
	rollback, err := persistSettingsCandidate(candidate)
	if err != nil {
		t.Fatalf("persist candidate: %v", err)
	}

	newer := []byte("newer external configuration\n")
	if err := config.WriteFileAtomic(cfg.FilePath(), newer, 0o600); err != nil {
		t.Fatalf("write newer config: %v", err)
	}
	if err := rollback(); !errors.Is(err, config.ErrRollbackConflict) {
		t.Fatalf("rollback error=%v, want ErrRollbackConflict", err)
	}
	data, err := os.ReadFile(cfg.FilePath())
	if err != nil {
		t.Fatalf("read config after rollback conflict: %v", err)
	}
	if !bytes.Equal(data, newer) {
		t.Fatalf("rollback overwrote newer config: %q", data)
	}
}

func TestSettingsConflictDoesNotReplayStaleFormOnLatestRevision(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{
		cfg:          cfg.Clone(),
		revision:     3,
		conflictOnce: true,
		concurrentUpdate: func(latest *config.Config) {
			latest.GeoIP.ExitIPConcurrency = 29
		},
	}
	server := newSettingsTransactionServer(cfg, manager)
	recorder := httptest.NewRecorder()

	server.handleSettings(recorder, settingsPutRequest(bytes.NewBufferString(`{"external_ip":"203.0.113.30"}`), 3))

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	committed, _ := manager.ConfigSnapshot()
	if committed.ExternalIP != cfg.ExternalIP || committed.GeoIP.ExitIPConcurrency != 29 {
		t.Fatalf("stale form was replayed: external=%q geo_concurrency=%d", committed.ExternalIP, committed.GeoIP.ExitIPConcurrency)
	}
}

func TestSettingsRequiresAndReturnsRevisionETag(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 11}
	server := newSettingsTransactionServer(cfg, manager)

	getRecorder := httptest.NewRecorder()
	server.handleSettings(getRecorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if getRecorder.Code != http.StatusOK || getRecorder.Header().Get("ETag") != settingsETag(11) {
		t.Fatalf("GET status=%d ETag=%q", getRecorder.Code, getRecorder.Header().Get("ETag"))
	}

	missingRecorder := httptest.NewRecorder()
	server.handleSettings(missingRecorder, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{}`)))
	if missingRecorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}

	staleRecorder := httptest.NewRecorder()
	server.handleSettings(staleRecorder, settingsPutRequest(bytes.NewBufferString(`{}`), 10))
	if staleRecorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
}

func TestSettingsNamedProfilesValidateBeforeCommit(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	cfg.Mode = "pool"
	cfg.Listener.Username = "fleet"
	cfg.Listener.Password = "secret"
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 21}
	server := newSettingsTransactionServer(cfg, manager)
	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"profiles":[{"name":" HK-Fast ","regions":["HK","hk"],"protocols":["VLESS"],"min_quality":80}]}`)
	server.handleSettings(recorder, settingsPutRequest(body, 21))
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid profile status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	committed, revision := manager.ConfigSnapshot()
	if revision != 22 || len(committed.Profiles) != 1 || committed.Profiles[0].Name != "hk-fast" || len(committed.Profiles[0].Regions) != 1 {
		t.Fatalf("profile not normalized and committed: revision=%d profiles=%#v", revision, committed.Profiles)
	}

	invalidManager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 8}
	invalidServer := newSettingsTransactionServer(cfg, invalidManager)
	invalidRecorder := httptest.NewRecorder()
	invalidBody := bytes.NewBufferString(`{"profiles":[{"name":"broken","name_regex":"["}]}`)
	invalidServer.handleSettings(invalidRecorder, settingsPutRequest(invalidBody, 8))
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid profile status=%d body=%s", invalidRecorder.Code, invalidRecorder.Body.String())
	}
	_, invalidRevision := invalidManager.ConfigSnapshot()
	if invalidRevision != 8 {
		t.Fatalf("invalid profile advanced revision to %d", invalidRevision)
	}
}

func TestSettingsEndpointManagerCommitsAndReturnsRuntimeStatus(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	cfg.Mode = "pool"
	manager := &settingsTransactionNodeManager{
		cfg: cfg.Clone(), revision: 31,
		endpointStatuses: []EndpointRuntimeStatus{
			{Name: "public", Status: "running"},
			{Name: "standby", Status: "disabled", Message: "已由配置停用"},
		},
	}
	server := newSettingsTransactionServer(cfg, manager)
	body := bytes.NewBufferString(`{"endpoints":[{"name":" Public ","enabled":true,"address":"127.0.0.1","port":2323,"username":"fleet","password":"secret"},{"name":"standby","enabled":false,"address":"127.0.0.1","port":2324}]}`)
	recorder := httptest.NewRecorder()

	server.handleSettings(recorder, settingsPutRequest(body, 31))

	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	committed, revision := manager.ConfigSnapshot()
	if revision != 32 || len(committed.Endpoints) != 2 || committed.Endpoints[0].Name != "public" {
		t.Fatalf("endpoints not normalized and committed: revision=%d endpoints=%#v", revision, committed.Endpoints)
	}
	if committed.Listener.Address != "127.0.0.1" || committed.Listener.Port != 2323 || committed.Listener.Username != "fleet" {
		t.Fatalf("legacy listener mirror was not updated: %#v", committed.Listener)
	}

	getRecorder := httptest.NewRecorder()
	server.handleSettings(getRecorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if getRecorder.Code != http.StatusOK || getRecorder.Header().Get("ETag") != settingsETag(32) {
		t.Fatalf("GET status=%d ETag=%q body=%s", getRecorder.Code, getRecorder.Header().Get("ETag"), getRecorder.Body.String())
	}
	var response struct {
		Endpoints []endpointSettingsResponse `json:"endpoints"`
	}
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if len(response.Endpoints) != 2 || response.Endpoints[0].Status != "running" || response.Endpoints[1].Status != "disabled" {
		t.Fatalf("unexpected endpoint settings payload: %#v", response.Endpoints)
	}
}

func TestAccessAssistantReturnsCopyableProfileEndpointData(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	cfg.Mode = "pool"
	cfg.Listener.Address = "127.0.0.1"
	cfg.Listener.Port = 23230
	cfg.Listener.Username = "fleet"
	cfg.Listener.Password = "p@ss word"
	cfg.Profiles = []config.ProfileConfig{{Name: "hk-fast", Regions: []string{"hk"}}}
	server := newSettingsTransactionServer(cfg, nil)
	recorder := httptest.NewRecorder()
	server.handleAccessAssistant(recorder, httptest.NewRequest(http.MethodGet, "/api/access", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		HTTPURI  string                 `json:"http_uri"`
		SOCKSURI string                 `json:"socks5_uri"`
		Profiles []config.ProfileConfig `json:"profiles"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.HTTPURI != "http://fleet:p%40ss%20word@127.0.0.1:23230" || response.SOCKSURI != "socks5://fleet:p%40ss%20word@127.0.0.1:23230" {
		t.Fatalf("unexpected access URIs: http=%q socks=%q", response.HTTPURI, response.SOCKSURI)
	}
	if len(response.Profiles) != 1 || response.Profiles[0].Name != "hk-fast" {
		t.Fatalf("profiles=%#v", response.Profiles)
	}
}
