package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

type jobAPIFixture struct {
	settingsTransactionNodeManager
	session string
}

func (*jobAPIFixture) ListJobs() []jobs.Status {
	return []jobs.Status{{Name: "accounts", Mode: "pinned"}}
}
func (*jobAPIFixture) ListJobSessions() []jobs.SessionInfo {
	return []jobs.SessionInfo{{Job: "accounts", Session: "account-a", Node: "node-a", State: "ready"}, {Job: "deleted", Session: "account-b", Node: "node-b", State: "removed"}}
}
func (*jobAPIFixture) RefreshJob(context.Context, string) (jobs.Status, error) {
	return jobs.Status{Name: "accounts"}, nil
}
func (f *jobAPIFixture) AcquireJob(name, session string) (jobs.Access, error) {
	f.session = session
	return jobs.Access{Job: name, State: "paused", Node: "same-node"}, nil
}
func (*jobAPIFixture) ReleaseJob(string, string) error       { return nil }
func (*jobAPIFixture) ReportJob(string, jobs.Feedback) error { return nil }
func TestJobsAPIRequiresAdminAndValidatesBodies(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop()
	s := NewServer(Config{Enabled: true, Listen: "127.0.0.1:9091", Password: "admin-test"}, m, nil)
	defer s.Shutdown(context.Background())
	f := &jobAPIFixture{}
	s.SetNodeManager(f)
	for _, role := range []Role{RoleViewer, RoleOperator, RoleAdmin} {
		s.sessions[string(role)] = &Session{Role: role, ExpiresAt: time.Now().Add(time.Hour)}
	}
	for _, test := range []struct {
		role string
		body string
		want int
	}{{"", `{}`, 401}, {string(RoleViewer), `{}`, 403}, {string(RoleOperator), `{}`, 403}, {string(RoleAdmin), `{"session":"account-a","typo":true}`, 400}, {string(RoleAdmin), `{"session":"account-a"}`, 200}} {
		r := httptest.NewRequest(http.MethodPost, "/api/jobs/accounts/acquire", strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		if test.role != "" {
			r.Header.Set("Authorization", "Bearer "+test.role)
		}
		w := httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("role=%q body=%s status=%d response=%s", test.role, test.body, w.Code, w.Body.String())
		}
		if w.Code == 200 && (f.session != "account-a" || !strings.Contains(w.Body.String(), `"state":"paused"`) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store")) {
			t.Fatal("session or paused state lost")
		}
	}
	for _, test := range []struct {
		role, query string
		code        int
		contains    string
	}{
		{string(RoleViewer), "", 403, ""}, {string(RoleAdmin), "?page_size=1&page=2", 200, `"job":"deleted"`},
		{string(RoleAdmin), "?job=accounts&q=ACCOUNT-A&state=ready", 200, `"total_items":1`},
		{string(RoleAdmin), "?page_size=101", 400, ""}, {string(RoleAdmin), "?page=-1", 400, ""},
		{string(RoleAdmin), "?job=missing&page=20", 200, `"page":1`},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/jobs/sessions"+test.query, nil)
		r.Header.Set("Authorization", "Bearer "+test.role)
		w := httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(w, r)
		if w.Code != test.code || !strings.Contains(w.Body.String(), test.contains) {
			t.Fatalf("session list: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "proxy_url") || strings.Contains(w.Body.String(), "password") {
			t.Fatal("session list disclosed credentials")
		}
	}
}

func TestPinnedAccessAssistantRequiresSessionInsteadOfExportingBaseCredentials(t *testing.T) {
	cfg := newSettingsTransactionConfig(t)
	cfg.Mode = "pool"
	cfg.Endpoints = []config.EndpointConfig{{Name: "accounts", Address: "127.0.0.1", Port: 23231, Username: "scraper", Password: "secret", Profile: "fast"}}
	cfg.Profiles = []config.ProfileConfig{{Name: "fast"}}
	cfg.Jobs = []config.JobConfig{{Name: "accounts", Mode: "pinned", Profile: "fast", Endpoint: "accounts"}}
	server := newSettingsTransactionServer(cfg, nil)
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()
	server.mgr = manager
	w := httptest.NewRecorder()
	server.handleAccessAssistant(w, httptest.NewRequest(http.MethodGet, "/api/access", nil))
	var response struct {
		HTTPURI   string                   `json:"http_uri"`
		SocksURI  string                   `json:"socks5_uri"`
		Endpoints []endpointAccessResponse `json:"endpoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(response.Endpoints) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	endpoint := response.Endpoints[0]
	if endpoint.Job != "accounts" || endpoint.JobMode != "pinned" || endpoint.HTTPURI != "" || endpoint.Socks5URI != "" || response.HTTPURI != "" || response.SocksURI != "" {
		t.Fatalf("pinned endpoint advertised an unusable URL: %+v", response)
	}

	w = httptest.NewRecorder()
	server.handleExport(w, httptest.NewRequest(http.MethodGet, "/api/export?scheme=all", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "scraper:secret") || !strings.Contains(w.Body.String(), "session ID is required") {
		t.Fatalf("pinned export advertised base credentials: %d %s", w.Code, w.Body.String())
	}
}
