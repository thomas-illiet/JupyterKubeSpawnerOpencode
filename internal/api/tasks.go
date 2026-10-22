package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// createTask validates, locks, persists, and snapshots a new Agent task.
func (s *Service) createTask(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspace, ok := s.ownedWorkspace(r.PathValue("workspace"), user)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	agentID := requestedAgentID(r)
	if !s.agentExists(r.Context(), workspace, agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	var request struct {
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.Description) == "" || len(request.Description) > 32*1024 {
		problem(w, http.StatusBadRequest, "invalid_request", "description is required and must be at most 32 KiB")
		return
	}
	id, err := randomID()
	if err != nil {
		problem(w, http.StatusInternalServerError, "internal_error", "Could not allocate task")
		return
	}
	locked, err := s.kube.AcquireWorkspace(r.Context(), workspace, id)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "lock_unavailable", "Could not lock workspace")
		return
	}
	if !locked {
		problem(w, http.StatusConflict, "workspace_busy", "Another task is already using this workspace")
		return
	}
	now := time.Now().UTC()
	task := model.Task{ID: id, WorkspaceID: workspace.ID, Owner: user, Description: request.Description, Phase: model.PhaseSnapshotting, SnapshotName: id, AgentID: agentID, AgentName: agentName(workspace.ID, agentID), KubeTaskName: "vscode-task-" + id, CreatedAt: now, UpdatedAt: now}
	if err := s.kube.CreateRecord(r.Context(), task); err != nil {
		_ = s.kube.ReleaseWorkspace(r.Context(), workspace, id)
		problem(w, http.StatusServiceUnavailable, "record_failed", "Could not persist task")
		return
	}
	if err := s.kube.CreateSnapshotJob(r.Context(), workspace, task, false); err != nil {
		s.fail(r.Context(), workspace, &task, "Could not create snapshot job")
		problem(w, http.StatusServiceUnavailable, "snapshot_failed", "Could not create snapshot job")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

// agentExists reports whether an Agent identifier exists in a workspace.
func (s *Service) agentExists(ctx context.Context, workspace config.Workspace, agentID string) bool {
	if agentID == "default" {
		return true
	}
	values, err := s.kube.ListAgentConfigs(ctx, workspace)
	if err != nil {
		return false
	}
	for _, value := range values {
		if value.ID == agentID {
			return true
		}
	}
	return false
}

// getTask returns an owned task.
func (s *Service) getTask(w http.ResponseWriter, r *http.Request) {
	_, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// taskOutput returns the plain-text output of an owned task.
func (s *Service) taskOutput(w http.ResponseWriter, r *http.Request) {
	workspace, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	output, err := s.kube.TaskOutput(r.Context(), workspace, task)
	if apierrors.IsNotFound(err) {
		output = ""
	} else if err != nil {
		problem(w, http.StatusServiceUnavailable, "output_unavailable", "Could not read OpenCode output")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(output))
}

// events streams task state changes as server-sent events.
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	workspace, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, http.StatusInternalServerError, "stream_unsupported", "Streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	last := time.Time{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if task.UpdatedAt.After(last) {
			data, _ := json.Marshal(task)
			_, _ = fmt.Fprintf(w, "event: task\ndata: %s\n\n", data)
			flusher.Flush()
			last = task.UpdatedAt
			if task.Terminal() {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			updated, err := s.kube.GetRecord(r.Context(), workspace, task.ID)
			if err != nil {
				return
			}
			task = updated
		}
	}
}

// cancelTask records a cancellation request for an active task.
func (s *Service) cancelTask(w http.ResponseWriter, r *http.Request) {
	workspace, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	if task.Terminal() {
		writeJSON(w, http.StatusOK, task)
		return
	}
	task.CancelRequest = true
	task.UpdatedAt = time.Now().UTC()
	if err := s.kube.UpdateRecord(r.Context(), workspace, task); err != nil {
		problem(w, http.StatusServiceUnavailable, "update_failed", "Could not cancel task")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

// restoreTask starts restoration of a completed task snapshot.
func (s *Service) restoreTask(w http.ResponseWriter, r *http.Request) {
	workspace, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	if !task.Terminal() {
		problem(w, http.StatusConflict, "task_active", "Cancel or wait for the task before restoring")
		return
	}
	locked, err := s.kube.AcquireWorkspace(r.Context(), workspace, task.ID)
	if err != nil || !locked {
		problem(w, http.StatusConflict, "workspace_busy", "Workspace is currently locked")
		return
	}
	if err := s.kube.CreateSnapshotJob(r.Context(), workspace, task, true); err != nil {
		_ = s.kube.ReleaseWorkspace(r.Context(), workspace, task.ID)
		problem(w, http.StatusServiceUnavailable, "restore_failed", "Could not start restore job")
		return
	}
	task.Phase, task.Message, task.UpdatedAt = model.PhaseRestoring, "Restoring protected snapshot", time.Now().UTC()
	if err := s.kube.UpdateRecord(r.Context(), workspace, task); err != nil {
		problem(w, http.StatusServiceUnavailable, "update_failed", "Restore started but state update failed")
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

// workspaceActivity cancels conflicting tasks after an interactive file change.
func (s *Service) workspaceActivity(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspace, ok := s.ownedWorkspace(r.PathValue("workspace"), user)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &request); err != nil || request.Path == "" || path.IsAbs(request.Path) || strings.Contains(request.Path, "..") {
		problem(w, http.StatusBadRequest, "invalid_path", "A relative workspace path is required")
		return
	}
	tasks, err := s.kube.ListActiveRecords(r.Context())
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "lookup_failed", "Could not inspect active tasks")
		return
	}
	for _, task := range tasks {
		if task.WorkspaceID == workspace.ID && task.Owner == user && (task.Phase == model.PhaseSnapshotting || task.Phase == model.PhaseRunning || task.Phase == model.PhaseQueued) {
			task.CancelRequest = true
			task.Message = "Cancelled because VS Code saved " + request.Path + " while the agent was active"
			task.UpdatedAt = time.Now().UTC()
			_ = s.kube.UpdateRecord(r.Context(), workspace, task)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
