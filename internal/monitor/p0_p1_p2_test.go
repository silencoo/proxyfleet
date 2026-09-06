package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdaptiveSelectionSkipsRecentPassiveSuccessAndHonorsBudget(t *testing.T) {
	manager, err := NewManager(Config{
		ProbeMode: "adaptive", ProbeBatchSize: 10, ProbeMaxPerHour: 1,
		ProbeHealthyInterval: time.Minute, ProbeFailureRetryInterval: time.Second,
		ProbeFailureMaxInterval: time.Minute, ProbePassiveGrace: 10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	now := time.Now()
	passive := manager.Register(NodeInfo{Tag: "passive"})
	passive.SetProbe(func(context.Context) (time.Duration, error) { return time.Millisecond, nil })
	passive.ref.mu.Lock()
	passive.ref.lastPassiveSuccess = now.Add(-time.Minute)
	passive.ref.initialCheckDone = true
	passive.ref.available = true
	passive.ref.mu.Unlock()
	first := manager.Register(NodeInfo{Tag: "first"})
	first.SetProbe(func(context.Context) (time.Duration, error) { return time.Millisecond, nil })
	second := manager.Register(NodeInfo{Tag: "second"})
	second.SetProbe(func(context.Context) (time.Duration, error) { return time.Millisecond, nil })

	selected := manager.selectAdaptiveEntries([]*entry{passive.ref, first.ref, second.ref}, 10, now)
	if len(selected) != 1 {
		t.Fatalf("selected %d entries, want budget-limited 1", len(selected))
	}
	if selected[0].info.Tag == "passive" {
		t.Fatal("recent passive success was actively probed")
	}
	status := manager.ProbeBudgetStatus()
	if status.Used != 1 || status.Remaining != 0 || status.PassiveSkipped != 1 || status.Due != 2 {
		t.Fatalf("unexpected budget status: %+v", status)
	}
}

func TestAdaptiveFailureBackoffAndQualityScoreAreBounded(t *testing.T) {
	if got := adaptiveFailureInterval(time.Minute, 10*time.Minute, 5); got != 10*time.Minute {
		t.Fatalf("capped backoff = %v, want 10m", got)
	}
	entry := &entry{available: true, initialCheckDone: true, success: 10, failure: 1, ewmaLatencyMs: 50, lastOK: time.Now()}
	score := qualityScoreLocked(entry, time.Now())
	if score < 80 || score > 100 {
		t.Fatalf("healthy quality score = %.2f, want 80..100", score)
	}
	entry.blacklist = true
	if got := qualityScoreLocked(entry, time.Now()); got != 0 {
		t.Fatalf("blacklisted quality score = %.2f, want 0", got)
	}
}

func TestNodeQueryFiltersSortsAndPaginatesServerSide(t *testing.T) {
	nodes := []Snapshot{
		{NodeInfo: NodeInfo{Name: "Tokyo", Tag: "jp-1", Region: "jp"}, InitialCheckDone: true, Available: true, QualityScore: 92},
		{NodeInfo: NodeInfo{Name: "Osaka", Tag: "jp-2", Region: "jp"}, InitialCheckDone: true, Available: true, QualityScore: 75},
		{NodeInfo: NodeInfo{Name: "Hong Kong", Tag: "hk-1", Region: "hk"}, InitialCheckDone: true, Available: true, QualityScore: 99},
	}
	page, pagination := queryNodes(nodes, NodeQuery{Page: 1, PageSize: 1, Region: "jp", Status: "healthy", Sort: "score", Order: "desc"})
	if pagination.TotalItems != 2 || pagination.TotalPages != 2 || len(page) != 1 || page[0].Tag != "jp-1" {
		t.Fatalf("unexpected page=%+v pagination=%+v", page, pagination)
	}
}

func TestRoleMiddlewareEnforcesViewerOperatorAdminHierarchy(t *testing.T) {
	server := &Server{
		cfg: Config{Password: "admin", OperatorPassword: "operator", ViewerPassword: "viewer"},
		sessions: map[string]*Session{
			"viewer-token":   {Token: "viewer-token", Role: RoleViewer, ExpiresAt: time.Now().Add(time.Hour)},
			"operator-token": {Token: "operator-token", Role: RoleOperator, ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	handler := server.withRole(RoleOperator, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		token string
		want  int
	}{{"viewer-token", http.StatusForbidden}, {"operator-token", http.StatusNoContent}} {
		request := httptest.NewRequest(http.MethodPost, "/api/nodes/probe-all", nil)
		request.Header.Set("Authorization", "Bearer "+test.token)
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("token %s status=%d want=%d body=%s", test.token, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestMetricPointAggregatesDoNotContainNodeSecrets(t *testing.T) {
	point := metricPointFromSnapshots([]Snapshot{
		{NodeInfo: NodeInfo{URI: "vless://secret@example.test"}, InitialCheckDone: true, Available: true, LastLatencyMs: 10, QualityScore: 90},
		{InitialCheckDone: true, Available: false, LastLatencyMs: 50, QualityScore: 0},
	}, ProbeBudgetStatus{Used: 3, Limit: 10}, time.Now())
	if point.TotalNodes != 2 || point.HealthyNodes != 1 || point.UnavailableNodes != 1 || point.P95LatencyMs != 50 || point.ProbeBudgetUsed != 3 {
		t.Fatalf("unexpected metric point: %+v", point)
	}
}
func TestAuditLogBoundsPersistedEntriesAndStripsQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	log := NewAuditLog(path, 2)
	log.Record(AuditEvent{Method: http.MethodPost, Path: "/first?secret=one", Status: 200})
	log.Record(AuditEvent{Method: http.MethodPut, Path: "/second?token=two", Status: 204})
	log.Record(AuditEvent{Method: http.MethodDelete, Path: "/third?password=three", Status: 202})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("persisted audit lines=%d, want 2: %s", len(lines), data)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "token") || strings.Contains(string(data), "password") || !strings.Contains(string(data), "/third") {
		t.Fatalf("audit persistence did not sanitize/bound entries: %s", data)
	}
}
