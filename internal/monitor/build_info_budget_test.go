package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDailyProbeBudgetCapsAcrossHourlyReset(t *testing.T) {
	manager, err := NewManager(Config{ProbeMode: "adaptive", ProbeMaxPerHour: 10, ProbeMaxPerDay: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location())
	if got := manager.reserveProbeBudget(2, dayStart); got != 2 {
		t.Fatalf("first reservation = %d, want 2", got)
	}
	if got := manager.reserveProbeBudget(2, dayStart.Add(time.Hour)); got != 0 {
		t.Fatalf("hourly reset bypassed daily limit: got %d", got)
	}
	if got := manager.reserveProbeBudget(2, dayStart.AddDate(0, 0, 1)); got != 2 {
		t.Fatalf("daily reset reservation = %d, want 2", got)
	}
}

func TestBuildInfoHandlerReportsCapabilitiesAndMethodGuard(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/build-info", nil)
	recorder := httptest.NewRecorder()
	server.handleBuildInfo(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", recorder.Code)
	}
	var response struct {
		Product      string          `json:"product"`
		Capabilities map[string]bool `json:"capabilities"`
		Protocols    []string        `json:"protocols"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Product != "ProxyFleet" || response.Capabilities == nil || len(response.Protocols) == 0 {
		t.Fatalf("incomplete build info response: %+v", response)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/build-info", nil)
	recorder = httptest.NewRecorder()
	server.handleBuildInfo(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", recorder.Code)
	}
}
