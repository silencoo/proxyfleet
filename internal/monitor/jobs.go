package monitor

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/silencoo/proxyfleet/internal/jobs"
)

// Job access contains proxy credentials, so all operations require the same
// administrator authorization and CSRF policy as /api/access and settings.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, ok := s.nodeManager().(jobs.Controller)
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "job runtime unavailable")
		return
	}
	if r.URL.Path == "/api/jobs" {
		if r.Method != http.MethodGet {
			writeJSONMethodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, map[string]any{"jobs": c.ListJobs()})
		return
	}
	if r.URL.Path == "/api/jobs/sessions" {
		if r.Method != http.MethodGet {
			writeJSONMethodNotAllowed(w, http.MethodGet)
			return
		}
		query := r.URL.Query()
		page, size := 1, 20
		for key, dst := range map[string]*int{"page": &page, "page_size": &size} {
			if value := query.Get(key); value != "" {
				n, err := strconv.Atoi(value)
				if err != nil || n < 1 || n > 4096 || (key == "page_size" && n > 100) {
					writeJSONError(w, http.StatusBadRequest, "invalid session pagination")
					return
				}
				*dst = n
			}
		}
		filtered := []jobs.SessionInfo{}
		search := strings.ToLower(query.Get("q"))
		for _, session := range c.ListJobSessions() {
			if query.Get("job") != "" && session.Job != query.Get("job") {
				continue
			}
			if query.Get("state") != "" && session.State != query.Get("state") {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(session.Session+" "+session.Node+" "+session.Job), search) {
				continue
			}
			filtered = append(filtered, session)
		}
		total := len(filtered)
		pages := (total + size - 1) / size
		if pages == 0 {
			page = 1
		}
		if pages > 0 && page > pages {
			page = pages
		}
		start := min((page-1)*size, total)
		writeJSON(w, map[string]any{"sessions": filtered[start:min(start+size, total)], "pagination": map[string]int{"page": page, "page_size": size, "total_items": total, "total_pages": pages}})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "job route not found")
		return
	}
	name, action := parts[0], parts[1]
	if action == "refresh" {
		if r.Method != http.MethodPost {
			writeJSONMethodNotAllowed(w, http.MethodPost)
			return
		}
		status, err := c.RefreshJob(r.Context(), name)
		if err != nil {
			writeJobError(w, err)
			return
		}
		writeJSON(w, status)
		return
	}
	if action == "feedback" {
		if r.Method != http.MethodPost {
			writeJSONMethodNotAllowed(w, http.MethodPost)
			return
		}
		var feedback jobs.Feedback
		if err := decodeStrictJSON(w, r, 16<<10, &feedback); err != nil {
			writeStrictJSONError(w, err)
			return
		}
		if err := c.ReportJob(name, feedback); err != nil {
			writeJobError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if action != "acquire" && action != "release" {
		writeJSONError(w, http.StatusNotFound, "job route not found")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request struct {
		Session string `json:"session"`
	}
	if err := decodeStrictJSON(w, r, 16<<10, &request); err != nil {
		writeStrictJSONError(w, err)
		return
	}
	if action == "release" {
		if err := c.ReleaseJob(name, request.Session); err != nil {
			writeJobError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	access, err := c.AcquireJob(name, request.Session)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, access)
}
func writeJobError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, jobs.ErrUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, jobs.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "job operation failed"
	}
	writeJSONError(w, status, message)
}
