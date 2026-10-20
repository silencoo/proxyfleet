package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestDashboardStatusFiltersCombineWithRegionAndSearch(t *testing.T) {
	nodes := []Snapshot{
		{NodeInfo: NodeInfo{Tag: "healthy", Region: "us"}, InitialCheckDone: true, Available: true},
		{NodeInfo: NodeInfo{Tag: "blocked", Region: "us"}, Blacklisted: true},
		{NodeInfo: NodeInfo{Tag: "cooling", Region: "us"}, CoolingDown: true},
		{NodeInfo: NodeInfo{Tag: "failed", Region: "us"}, InitialCheckDone: true},
		{NodeInfo: NodeInfo{Tag: "unknown", Region: "us"}},
		{NodeInfo: NodeInfo{Tag: "foreign", Region: "jp"}, Blacklisted: true},
		{NodeInfo: NodeInfo{Tag: "resident", Name: "Residential node", Region: "other"}, Blacklisted: true},
	}
	for i := range nodes {
		if nodes[i].Name == "" {
			nodes[i].Name = nodes[i].Tag
		}
	}
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"region=us&status=all", []string{"blocked", "cooling", "failed", "healthy", "unknown"}},
		{"region=us&status=healthy", []string{"healthy"}},
		{"region=us&status=blacklisted", []string{"blocked"}},
		{"region=us&status=cooling", []string{"cooling"}},
		{"region=us&status=unavailable", []string{"blocked", "cooling", "failed"}},
		{"region=us&status=unknown", []string{"unknown"}},
		{"region=resident&status=blacklisted&search=Residential", []string{"resident"}},
		{"region=us&status=blacklisted&search=foreign", []string{}},
	} {
		t.Run(tt.query, func(t *testing.T) {
			query, paginated, err := parseNodeQuery(httptest.NewRequest("GET", "/api/nodes?sort=name&"+tt.query, nil))
			if err != nil || !paginated {
				t.Fatalf("parse: paginated=%v err=%v", paginated, err)
			}
			got, pagination := queryNodes(nodes, query)
			tags := make([]string, 0, len(got))
			for _, node := range got {
				tags = append(tags, node.Tag)
			}
			if !reflect.DeepEqual(tags, tt.want) || pagination.TotalItems != len(tt.want) {
				t.Fatalf("got %v (%+v), want %v", tags, pagination, tt.want)
			}
		})
	}
}

func TestParseNodeActivityFilter(t *testing.T) {
	for _, tt := range []struct {
		query     string
		activity  string
		paginated bool
		invalid   bool
	}{
		{"", "all", false, false},
		{"activity=", "all", true, false},
		{"activity=all", "all", true, false},
		{"activity=%20ACTIVE%20", "active", true, false},
		{"activity=idle", "idle", true, false},
		{"activity=healthy", "", true, true},
		{"activity=true", "", true, true},
	} {
		t.Run(tt.query, func(t *testing.T) {
			query, paginated, err := parseNodeQuery(httptest.NewRequest(http.MethodGet, "/api/nodes?"+tt.query, nil))
			if (err != nil) != tt.invalid || paginated != tt.paginated || !tt.invalid && query.Activity != tt.activity {
				t.Fatalf("query=%+v paginated=%v err=%v", query, paginated, err)
			}
		})
	}
}

func TestHandleNodesActivityIsIndependentOfHealthAndKeepsGlobalCounts(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	for _, info := range []struct {
		tag    string
		region string
		active int
	}{
		{"healthy", "us", 3}, {"blocked", "us", 2}, {"idle", "us", 0},
		{"unknown", "jp", 0}, {"resident", "resident", 1},
	} {
		handle := manager.Register(NodeInfo{Tag: info.tag, Name: info.tag, Region: info.region})
		if info.tag != "unknown" {
			handle.MarkInitialCheckDone(true)
		}
		if info.tag == "blocked" {
			handle.Blacklist(time.Now().Add(time.Hour))
		}
		for range info.active {
			handle.IncActive()
		}
	}
	server := &Server{mgr: manager}
	for _, tt := range []struct {
		query string
		want  []string
		total int
		page  int
	}{
		{"activity=active", []string{"blocked", "healthy", "resident"}, 3, 1},
		{"activity=idle", []string{"idle", "unknown"}, 2, 1},
		{"activity=active&status=healthy&region=us", []string{"healthy"}, 1, 1},
		{"activity=active&status=blacklisted", []string{"blocked"}, 1, 1},
		{"activity=idle&status=healthy", []string{"idle"}, 1, 1},
		{"activity=active&region=resident&search=res", []string{"resident"}, 1, 1},
		{"activity=active&search=unknown", []string{}, 0, 1},
		{"activity=active&search=unknown&page=99", []string{}, 0, 1},
		{"activity=active&page_size=1&page=99", []string{"resident"}, 3, 3},
	} {
		t.Run(tt.query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.handleNodes(recorder, httptest.NewRequest(http.MethodGet, "/api/nodes?sort=name&"+tt.query, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var payload struct {
				Nodes      []Snapshot     `json:"nodes"`
				Pagination NodePagination `json:"pagination"`
				Summary    NodeSummary    `json:"summary"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			tags := make([]string, 0, len(payload.Nodes))
			for _, node := range payload.Nodes {
				tags = append(tags, node.Tag)
			}
			if !reflect.DeepEqual(tags, tt.want) || payload.Pagination.TotalItems != tt.total || payload.Pagination.Page != tt.page {
				t.Fatalf("tags=%v pagination=%+v want=%+v", tags, payload.Pagination, tt)
			}
			if payload.Summary.TotalNodes != 5 || payload.Summary.ActiveNodes != 3 || payload.Summary.ActiveConnections != 6 {
				t.Fatalf("filtered or paginated global summary: %+v", payload.Summary)
			}
		})
	}
	invalid := httptest.NewRecorder()
	server.handleNodes(invalid, httptest.NewRequest(http.MethodGet, "/api/nodes?activity=invalid", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unsupported activity status=%d", invalid.Code)
	}
}

func TestNodeActivityTracksConnectionLifetime(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	handle := manager.Register(NodeInfo{Tag: "node"})
	for _, active := range []int{0, 2, 1, 0} {
		for handle.ref.active.Load() < int32(active) {
			handle.IncActive()
		}
		for handle.ref.active.Load() > int32(active) {
			handle.DecActive()
		}
		snapshots := manager.Snapshot()
		want := 0
		if active > 0 {
			want = 1
		}
		if got := summarizeNodes(snapshots).ActiveNodes; got != want {
			t.Fatalf("connections=%d active nodes=%d want=%d", active, got, want)
		}
		if nodeMatchesActivity(snapshots[0], "active") != (active > 0) || nodeMatchesActivity(snapshots[0], "idle") != (active == 0) {
			t.Fatalf("activity did not follow connection lifetime: %+v", snapshots[0])
		}
	}
}
