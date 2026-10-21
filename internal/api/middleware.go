package api

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
)

// Handler returns the authenticated HTTP handler for the service.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/healthz" {
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
	workspaces := make([]config.Workspace, 0, 1)
	for _, workspace := range s.cfg.Workspaces {
		if workspace.Owner == username {
			workspaces = append(workspaces, workspace)
		}
	}
	if len(workspaces) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	for _, workspace := range workspaces {
		if err := s.kube.DeleteWorkspaceResources(r.Context(), workspace); err != nil {
			slog.Error("could not delete workspace resources", "workspace_id", workspace.ID, "error", err)
			problem(w, http.StatusServiceUnavailable, "cleanup_failed", "Could not delete user resources")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
