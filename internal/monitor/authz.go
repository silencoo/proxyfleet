package monitor

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type managementAuthConfig struct {
	AdminPassword    string
	OperatorPassword string
	ViewerPassword   string
	Generation       uint64
}

func (c managementAuthConfig) required() bool {
	return c.AdminPassword != "" || c.OperatorPassword != "" || c.ViewerPassword != ""
}

func (c managementAuthConfig) roleForPassword(password string) (Role, bool) {
	candidates := []struct {
		role     Role
		password string
	}{
		{RoleAdmin, c.AdminPassword},
		{RoleOperator, c.OperatorPassword},
		{RoleViewer, c.ViewerPassword},
	}
	for _, candidate := range candidates {
		if candidate.password != "" && secureCompareStrings(password, candidate.password) {
			return candidate.role, true
		}
	}
	return "", false
}

type requestRoleContextKey struct{}

func requestRole(r *http.Request) Role {
	if r == nil {
		return ""
	}
	role, ok := r.Context().Value(requestRoleContextKey{}).(Role)
	if !ok || role == "" {
		return RoleAdmin
	}
	return role
}

func (s *Server) withRole(required Role, next http.HandlerFunc) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		if !roleAllows(requestRole(r), required) {
			writeJSONError(w, http.StatusForbidden, "当前角色无权执行此操作")
			return
		}
		next(w, r)
	})
}

func requireRole(w http.ResponseWriter, r *http.Request, required Role) bool {
	if roleAllows(requestRole(r), required) {
		return true
	}
	writeJSONError(w, http.StatusForbidden, "当前角色无权执行此操作")
	return false
}

type responseStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *responseStatusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type flushingStatusRecorder struct {
	*responseStatusRecorder
	flusher http.Flusher
}

func (w *flushingStatusRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}

func (s *Server) serveAuthorized(w http.ResponseWriter, r *http.Request, role Role, next http.HandlerFunc) {
	r = r.WithContext(context.WithValue(r.Context(), requestRoleContextKey{}, role))
	if !isUnsafeHTTPMethod(r.Method) {
		next(w, r)
		return
	}
	recorder := &responseStatusRecorder{ResponseWriter: w}
	var writer http.ResponseWriter = recorder
	if flusher, ok := w.(http.Flusher); ok {
		writer = &flushingStatusRecorder{responseStatusRecorder: recorder, flusher: flusher}
	}
	next(writer, r)
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	if s.mgr == nil || s.mgr.AuditLog() == nil {
		return
	}
	s.mgr.AuditLog().Record(AuditEvent{
		Timestamp: time.Now(),
		Role:      role,
		Method:    r.Method,
		Path:      r.URL.Path,
		Status:    status,
		RemoteIP:  requestHostname(r.RemoteAddr),
	})
}

func (s *Server) validateSessionRole(token string) (Role, bool) {
	s.sessionMu.RLock()
	session, exists := s.sessions[token]
	s.sessionMu.RUnlock()
	if !exists {
		return "", false
	}
	if time.Now().After(session.ExpiresAt) {
		s.sessionMu.Lock()
		delete(s.sessions, token)
		s.sessionMu.Unlock()
		return "", false
	}
	role := session.Role
	if role == "" {
		role = RoleAdmin
	}
	if !roleAllows(role, RoleViewer) {
		return "", false
	}
	return role, true
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}
