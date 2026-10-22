package api

import (
	"bufio"
	"context"
	"crypto/subtle"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Handler returns the authenticated HTTP handler for the service.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		observed := &observedResponseWriter{ResponseWriter: w, status: http.StatusOK}
		w = observed
		requestID, err := randomID()
		if err != nil {
			requestID = "unavailable"
		}
		w.Header().Set("X-Request-ID", requestID)
		s.requestCount.Add(1)
		s.requestInFlight.Add(1)
		defer func() {
			s.requestInFlight.Add(-1)
			s.requestNanos.Add(uint64(time.Since(started)))
			switch observed.status / 100 {
			case 2:
				s.response2xx.Add(1)
			case 3:
				s.response3xx.Add(1)
			case 4:
				s.response4xx.Add(1)
			case 5:
				s.response5xx.Add(1)
			}
			slog.Debug("HTTP request completed", "request_id", requestID, "method", r.Method, "route", r.Pattern, "status", observed.status, "duration", time.Since(started))
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" || r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			s.mux.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/users/") {
			if !s.cleanupAuthorized(r.Header.Get("Authorization")) {
				problem(w, http.StatusUnauthorized, "cleanup_authentication_required", "A valid cleanup token is required")
				return
			}
			s.mux.ServeHTTP(w, r)
			return
		}
		user, err := s.auth.Authenticate(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			problem(w, http.StatusUnauthorized, "authentication_required", "A valid JupyterHub user token is required")
			return
		}
		s.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	})
}

// observedResponseWriter records status codes while preserving streaming,
// WebSocket, HTTP/2 push, and optimized copy capabilities of the wrapped writer.
type observedResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records and forwards the first response status.
func (w *observedResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status, w.wroteHeader = status, true
	w.ResponseWriter.WriteHeader(status)
}

// Write records an implicit success status and forwards response bytes.
func (w *observedResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// Flush forwards streaming flushes when supported by the wrapped writer.
func (w *observedResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack forwards WebSocket connection upgrades when supported.
func (w *observedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Push forwards HTTP/2 server push when supported.
func (w *observedResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

// ReadFrom preserves optimized response copying when supported.
func (w *observedResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, reader)
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *observedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// cleanupAuthorized compares an administrative bearer token in constant time.
func (s *Service) cleanupAuthorized(authorization string) bool {
	if s.cfg.CleanupToken == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.CleanupToken)) == 1
}

// deleteUserResources removes mediator-owned resources for every workspace owned by a user.
func (s *Service) deleteUserResources(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("user")
	workspace, exists := s.cfg.WorkspacesByOwner[username]
	if !exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if err := s.kube.DeleteWorkspaceResources(r.Context(), workspace); err != nil {
		slog.Error("could not delete workspace resources", "workspace_id", workspace.ID, "error", err)
		problem(w, http.StatusServiceUnavailable, "cleanup_failed", "Could not delete user resources")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
