package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

func TestConfiguredSampleProbeRotatesBoundedBatches(t *testing.T) {
	manager, err := NewManager(Config{ProbeTarget: "example.com:80", ProbeMode: "sample", ProbeBatchSize: 2, ProbeConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	var calls [5]atomic.Int32
	for index := range calls {
		index := index
		handle := manager.Register(NodeInfo{Tag: fmt.Sprintf("node-%d", index)})
		handle.SetProbe(func(context.Context) (time.Duration, error) {
			calls[index].Add(1)
			return time.Millisecond, nil
		})
	}

	for pass := 0; pass < 2; pass++ {
		ran, err := manager.ProbeConfiguredNowContext(context.Background(), time.Second, 0)
		if err != nil || !ran {
			t.Fatalf("pass %d: ran=%v err=%v", pass, ran, err)
		}
	}
	for index, want := range []int32{1, 1, 1, 1, 0} {
		if got := calls[index].Load(); got != want {
			t.Fatalf("node-%d calls=%d, want %d", index, got, want)
		}
	}
}

func TestConfiguredManualProbeSkipsAutomaticPass(t *testing.T) {
	manager, err := NewManager(Config{ProbeMode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	var calls atomic.Int32
	handle := manager.Register(NodeInfo{Tag: "manual"})
	handle.SetProbe(func(context.Context) (time.Duration, error) {
		calls.Add(1)
		return time.Millisecond, nil
	})

	ran, err := manager.ProbeConfiguredNowContext(context.Background(), time.Second, 0)
	if err != nil || ran || calls.Load() != 0 {
		t.Fatalf("manual policy ran=%v calls=%d err=%v", ran, calls.Load(), err)
	}
}

func TestClearDiagnosticsPreservesRoutingHealth(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	handle := manager.Register(NodeInfo{Tag: "keep-health"})
	handle.MarkInitialCheckDone(true)
	handle.MarkAvailable(true)
	handle.RecordSuccessWithLatency(42 * time.Millisecond)
	handle.Blacklist(time.Now().Add(time.Hour))

	cleared, err := manager.ClearDiagnostics("keep-health")
	if err != nil || !cleared {
		t.Fatalf("cleared=%v err=%v", cleared, err)
	}
	snapshot := manager.Snapshot()[0]
	if snapshot.HasDiagnostics || snapshot.SuccessCount != 0 || snapshot.FailureCount != 0 || len(snapshot.Timeline) != 0 {
		t.Fatalf("diagnostics not cleared: %+v", snapshot)
	}
	if snapshot.LastProbeLatency != 42*time.Millisecond || !snapshot.Available || !snapshot.InitialCheckDone || !snapshot.Blacklisted {
		t.Fatalf("routing health changed while clearing diagnostics: %+v", snapshot)
	}
}

func TestDebugAndLogDeleteEndpoints(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	handle := manager.Register(NodeInfo{Tag: "debug-node"})
	handle.RecordFailure(fmt.Errorf("probe failed"))
	server := &Server{mgr: manager}

	deleteRecord := httptest.NewRecorder()
	server.handleDebugItem(deleteRecord, httptest.NewRequest(http.MethodDelete, "/api/debug/debug-node", nil))
	if deleteRecord.Code != http.StatusOK {
		t.Fatalf("delete diagnostic status=%d body=%s", deleteRecord.Code, deleteRecord.Body.String())
	}
	list := httptest.NewRecorder()
	server.handleDebug(list, httptest.NewRequest(http.MethodGet, "/api/debug", nil))
	var debugPayload struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &debugPayload); err != nil || len(debugPayload.Nodes) != 0 {
		t.Fatalf("diagnostic list after delete=%s err=%v", list.Body.String(), err)
	}

	previous := SharedLogBuffer
	SharedLogBuffer = NewLogBuffer(128)
	defer func() { SharedLogBuffer = previous }()
	_, _ = SharedLogBuffer.Write([]byte("temporary console output"))
	clearLogs := httptest.NewRecorder()
	server.handleLogs(clearLogs, httptest.NewRequest(http.MethodDelete, "/api/logs", nil))
	if clearLogs.Code != http.StatusOK || SharedLogBuffer.Content() != "" {
		t.Fatalf("clear logs status=%d content=%q body=%s", clearLogs.Code, SharedLogBuffer.Content(), clearLogs.Body.String())
	}
}

func TestConfigNodeBulkRevealRequiresExplicitQuery(t *testing.T) {
	node := config.NodeConfig{Name: "private", URI: "vless://uuid:password@example.com:443?token=secret"}
	server := &Server{nodeMgr: &nodeConfigManagerStub{nodes: []config.NodeConfig{node}}}

	hidden := httptest.NewRecorder()
	server.handleConfigNodes(hidden, httptest.NewRequest(http.MethodGet, "/api/nodes/config", nil))
	if strings.Contains(hidden.Body.String(), "uuid") || strings.Contains(hidden.Body.String(), "secret") {
		t.Fatalf("default list exposed URI: %s", hidden.Body.String())
	}

	revealed := httptest.NewRecorder()
	server.handleConfigNodes(revealed, httptest.NewRequest(http.MethodGet, "/api/nodes/config?reveal=true", nil))
	if !strings.Contains(revealed.Body.String(), node.URI) {
		t.Fatalf("explicit bulk reveal omitted URI: %s", revealed.Body.String())
	}
	if strings.Contains(revealed.Body.String(), `"username"`) || strings.Contains(revealed.Body.String(), `"password"`) {
		t.Fatalf("bulk URI reveal exposed separate node credentials: %s", revealed.Body.String())
	}
	if got := revealed.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
}

func TestSettingsCommitProbePolicyAndScheduledLogArchive(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	manager := &settingsTransactionNodeManager{cfg: cfg.Clone(), revision: 11}
	server := newSettingsTransactionServer(cfg, manager)
	body := bytes.NewBufferString(`{
		"probe_mode":"sample",
		"probe_interval":"30m",
		"probe_timeout":"3s",
		"probe_batch_size":25,
		"log":{"output":"file","max_size":25,"max_backups":6,"max_age":14,"compress":true,"rotate_interval":"24h"}
	}`)
	recorder := httptest.NewRecorder()
	server.handleSettings(recorder, settingsPutRequest(body, 11))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	committed, _ := manager.ConfigSnapshot()
	if committed.Management.ProbeMode != "sample" || committed.Management.ProbeInterval != 30*time.Minute || committed.Management.ProbeTimeout != 3*time.Second || committed.Management.ProbeBatchSize != 25 {
		t.Fatalf("probe policy not committed: %+v", committed.Management)
	}
	if committed.Log.Output != "file" || committed.Log.RotateInterval != 24*time.Hour || !committed.Log.Compress {
		t.Fatalf("scheduled log archive not committed: %+v", committed.Log)
	}
}
func TestProbePolicyDurationValidation(t *testing.T) {
	mode := "sample"
	interval := "10m0s"
	timeout := "500ms"
	batch := 25
	candidate := config.ManagementConfig{}
	if err := applyProbePolicyUpdate(&candidate, &mode, &interval, &timeout, &batch); err != nil {
		t.Fatalf("valid duration rejected: %v", err)
	}
	if candidate.ProbeInterval != 10*time.Minute || candidate.ProbeTimeout != 500*time.Millisecond {
		t.Fatalf("unexpected parsed policy: %+v", candidate)
	}

	for _, test := range []struct {
		name     string
		interval string
		timeout  string
	}{
		{name: "invalid interval", interval: "later", timeout: "10s"},
		{name: "interval below minimum", interval: "9s", timeout: "10s"},
		{name: "invalid timeout", interval: "5m", timeout: "soon"},
		{name: "timeout below minimum", interval: "5m", timeout: "99ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := config.ManagementConfig{}
			if err := applyProbePolicyUpdate(&policy, &mode, &test.interval, &test.timeout, &batch); err == nil {
				t.Fatalf("invalid policy accepted: interval=%q timeout=%q", test.interval, test.timeout)
			}
		})
	}
}

func TestEmbeddedWebUIExposesRequestedManagementControls(t *testing.T) {
	html := readWebUIBundle(t)
	for _, required := range []string{
		`id="configNodesRevealAll"`,
		`function toggleConfigNodeURI(id)`,
		`function copyConfigNodeURI(id, button)`,
		`navigator.clipboard.writeText(value)`,
		`${iconMarkup('copy')}`,
		`failSorted.map(getChartNodeDisplayName)`,
		`\p{Extended_Pictographic}`,
		`formatDurationForInput(d.probe_interval`,
		`function validateProbeDurationSettings()`,
		`id="settingProbeIntervalError"`,
		`function clearDiagnostic(tag, name)`,
		`function clearAllDiagnostics()`,
		`function clearConsoleLogs()`,
		`id="confirmOverlay"`,
		`role="alertdialog"`,
		`id="settingProbeMode"`,
		`id="settingProbeBatchSize"`,
		`id="settingLogRotateInterval"`,
		`center: ['50%', '42%']`,
	} {
		if !strings.Contains(html, required) {
			t.Errorf("embedded WebUI is missing %q", required)
		}
	}
}
