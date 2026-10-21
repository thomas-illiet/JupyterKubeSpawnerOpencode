package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
)

// authorizedTask loads a task only when it belongs to the authenticated user.
func (s *Service) authorizedTask(w http.ResponseWriter, r *http.Request) (config.Workspace, model.Task, bool) {
	user := currentUser(r.Context())
	for _, workspace := range s.cfg.Workspaces {
		if workspace.Owner != user {
			continue
		}
		task, err := s.kube.GetRecord(r.Context(), workspace, r.PathValue("task"))
		if err == nil && task.Owner == user && task.WorkspaceID == workspace.ID {
			return workspace, task, true
		}
	}
	problem(w, http.StatusNotFound, "task_not_found", "Task not found")
	return config.Workspace{}, model.Task{}, false
}

// ownedWorkspace returns a workspace only when the user owns it.
func (s *Service) ownedWorkspace(id, user string) (config.Workspace, bool) {
	workspace, ok := s.cfg.Workspaces[id]
	return workspace, ok && workspace.Owner == user
}

// currentUser returns the authenticated username stored in a request context.
func currentUser(ctx context.Context) string {
	value, _ := ctx.Value(userContextKey{}).(string)
	return value
}

type userContextKey struct{}

// randomID returns a cryptographically random task identifier.
func randomID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

// agentName builds the Kubernetes resource name for an Agent.
func agentName(workspaceID, agentID string) string {
	if firstAgentID(agentID) != "default" {
		return "vscode-" + safeAPIName(workspaceID) + "-" + safeAPIName(agentID)
	}
	return "vscode-" + safeAPIName(workspaceID)
}

// safeAPIName normalizes an identifier for Kubernetes resource names.
func safeAPIName(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// decodeJSON decodes one bounded JSON request while rejecting unknown fields.
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nilResponseWriter{}, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

type nilResponseWriter struct{}

// Header implements http.ResponseWriter for bounded request decoding.
func (nilResponseWriter) Header() http.Header { return make(http.Header) }

// Write implements http.ResponseWriter while discarding output.
func (nilResponseWriter) Write([]byte) (int, error) { return 0, nil }

// WriteHeader implements http.ResponseWriter while discarding status codes.
func (nilResponseWriter) WriteHeader(int) {}

// writeJSON writes a JSON response with the requested status.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// problem writes an RFC 9457-style API error response.
func problem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": code, "status": status, "detail": detail})
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
