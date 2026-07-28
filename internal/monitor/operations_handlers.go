package monitor

import (
	"net/http"
	"strconv"
	"strings"
)

func parseBoundedLimit(r *http.Request, fallback, maximum int) (int, bool) {
	if r == nil {
		return fallback, true
	}
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximum {
		return 0, false
	}
	return limit, true
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, map[string]any{"role": requestRole(r)})
}

func (s *Server) handleProbeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONMethodNotAllowed(w, http.MethodGet)
		return
	}
	active, done, total, available, failed := s.mgr.ProbeSweepProgress()
	writeJSON(w, map[string]any{
		"budget": s.mgr.ProbeBudgetStatus(),
		"sweep": map[string]any{
			"active": active, "done": done, "total": total,
			"available": available, "failed": failed,
		},
	})
}

func (s *Server) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONMethodNotAllowed(w, http.MethodGet)
		return
	}
	limit, ok := parseBoundedLimit(r, 240, maximumHistoryPoints)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "limit 必须在 1 到 10000 之间")
		return
	}
	writeJSON(w, map[string]any{"history": s.mgr.MetricsHistory(limit)})
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONMethodNotAllowed(w, http.MethodGet)
		return
	}
	activeOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("active")), "true")
	writeJSON(w, map[string]any{"alerts": s.mgr.Alerts(activeOnly)})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONMethodNotAllowed(w, http.MethodGet)
		return
	}
	limit, ok := parseBoundedLimit(r, 200, 1000)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "limit 必须在 1 到 1000 之间")
		return
	}
	if s.mgr.AuditLog() == nil {
		writeJSON(w, map[string]any{"events": []AuditEvent{}})
		return
	}
	writeJSON(w, map[string]any{"events": s.mgr.AuditLog().Events(limit)})
}
