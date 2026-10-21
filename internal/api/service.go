package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/auth"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type Service struct {
	cfg         config.Config
	kube        kube.Client
	auth        auth.Authenticator
	mux         *http.ServeMux
	workMu      sync.Map
	lifecycleMu sync.RWMutex
}

func NewService(cfg config.Config, client kube.Client, authenticator auth.Authenticator) *Service {
	s := &Service{cfg: cfg, kube: client, auth: authenticator, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("DELETE /api/v1/admin/users/{user}/resources", s.deleteUserResources)
	s.mux.HandleFunc("POST /api/v1/workspaces/{workspace}/tasks", s.createTask)
	s.mux.HandleFunc("GET /api/v1/workspaces/{workspace}/agent", s.getAgentConfig)
	s.mux.HandleFunc("PUT /api/v1/workspaces/{workspace}/agent", s.updateAgentConfig)
	s.mux.HandleFunc("GET /api/v1/workspaces/{workspace}/agents", s.listAgentConfigs)
	s.mux.HandleFunc("POST /api/v1/workspaces/{workspace}/agents", s.createAgentConfig)
	s.mux.HandleFunc("GET /api/v1/workspaces/{workspace}/agents/{agent}", s.getAgentConfig)
	s.mux.HandleFunc("PUT /api/v1/workspaces/{workspace}/agents/{agent}", s.updateAgentConfig)
	s.mux.HandleFunc("DELETE /api/v1/workspaces/{workspace}/agents/{agent}", s.deleteAgentConfig)
	s.mux.HandleFunc("POST /api/v1/workspaces/{workspace}/agents/{agent}/tasks", s.createTask)
	s.mux.HandleFunc("POST /api/v1/workspaces/{workspace}/activity", s.workspaceActivity)
	s.mux.HandleFunc("GET /api/v1/me/agents", s.listAgentConfigs)
	s.mux.HandleFunc("POST /api/v1/me/agents", s.createAgentConfig)
	s.mux.HandleFunc("GET /api/v1/me/agents/{agent}", s.getAgentConfig)
	s.mux.HandleFunc("PUT /api/v1/me/agents/{agent}", s.updateAgentConfig)
	s.mux.HandleFunc("DELETE /api/v1/me/agents/{agent}", s.deleteAgentConfig)
	s.mux.HandleFunc("GET /api/v1/me/agents/{agent}/status", s.agentStatus)
	s.mux.HandleFunc("/api/v1/me/agents/{agent}/attach", s.attachAgent)
	s.mux.HandleFunc("/api/v1/me/agents/{agent}/attach/{rest...}", s.attachAgent)
	s.mux.HandleFunc("GET /api/v1/tasks/{task}", s.getTask)
	s.mux.HandleFunc("GET /api/v1/tasks/{task}/events", s.events)
	s.mux.HandleFunc("GET /api/v1/tasks/{task}/output", s.taskOutput)
	s.mux.HandleFunc("POST /api/v1/tasks/{task}/cancel", s.cancelTask)
	s.mux.HandleFunc("POST /api/v1/tasks/{task}/restore", s.restoreTask)
	return s
}

func (s *Service) requestWorkspace(r *http.Request) (config.Workspace, bool) {
	user := currentUser(r.Context())
	if id := r.PathValue("workspace"); id != "" {
		return s.ownedWorkspace(id, user)
	}
	for _, workspace := range s.cfg.Workspaces {
		if workspace.Owner == user {
			return workspace, true
		}
	}
	return config.Workspace{}, false
}

func (s *Service) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	agentID := requestedAgentID(r)
	if !validAgentID(agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	if agentID != "default" && !s.agentExists(r.Context(), workspace, agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	value, err := s.kube.GetAgentConfig(r.Context(), workspace, agentID)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "configuration_unavailable", "Could not read agent configuration")
		return
	}
	value = normalizedAgentConfig(workspace, agentID, value)
	writeJSON(w, http.StatusOK, value)
}

func (s *Service) listAgentConfigs(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	values, err := s.kube.ListAgentConfigs(r.Context(), workspace)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "configuration_unavailable", "Could not list agents")
		return
	}
	for i := range values {
		values[i] = normalizedAgentConfig(workspace, values[i].ID, values[i])
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	writeJSON(w, http.StatusOK, values)
}

func (s *Service) createAgentConfig(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	var request model.AgentConfig
	if err := decodeJSON(r, &request); err != nil || !validAgentID(request.ID) || request.ID == "default" {
		problem(w, http.StatusBadRequest, "invalid_request", "A unique lowercase Agent id is required")
		return
	}
	values, err := s.kube.ListAgentConfigs(r.Context(), workspace)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "lookup_failed", "Could not inspect agents")
		return
	}
	if len(values) >= 5 {
		problem(w, http.StatusConflict, "agent_limit", "This workspace already has the maximum of five Agents")
		return
	}
	for _, value := range values {
		if value.ID == request.ID {
			problem(w, http.StatusConflict, "agent_exists", "Agent already exists")
			return
		}
	}
	s.saveAgentConfig(w, r, workspace, request.ID, request, http.StatusCreated)
}

func (s *Service) updateAgentConfig(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	var request model.AgentConfig
	if err := decodeJSON(r, &request); err != nil {
		problem(w, http.StatusBadRequest, "invalid_request", "Invalid agent configuration")
		return
	}
	agentID := requestedAgentID(r)
	if !validAgentID(agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	if agentID != "default" && !s.agentExists(r.Context(), workspace, agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	s.saveAgentConfig(w, r, workspace, agentID, request, http.StatusOK)
}

func (s *Service) saveAgentConfig(w http.ResponseWriter, r *http.Request, workspace config.Workspace, agentID string, request model.AgentConfig, status int) {
	user := currentUser(r.Context())
	request.Profile = strings.TrimSpace(request.Profile)
	request.Instructions = strings.TrimSpace(request.Instructions)
	request.Model = strings.TrimSpace(request.Model)
	if request.Profile == "" || len(request.Profile) > 200 || len(request.Instructions) > 16*1024 || !stringIn(workspace.AllowedModels, request.Model) || !validSkillSources(request.SkillSources) || !validEnvironment(request.Environment) {
		problem(w, http.StatusBadRequest, "invalid_configuration", "Profile, instructions, selected model or environment is invalid")
		return
	}
	active, err := s.kube.ListActiveRecords(r.Context())
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "lookup_failed", "Could not inspect active tasks")
		return
	}
	for _, task := range active {
		if task.WorkspaceID == workspace.ID && task.Owner == user {
			problem(w, http.StatusConflict, "workspace_busy", "Wait for the active task before changing the agent")
			return
		}
	}
	request.AllowedModels = nil
	request.UpdatedAt = time.Now().UTC()
	request.ID, request.ResourceName = agentID, agentName(workspace.ID, agentID)
	if err := s.kube.SetAgentConfig(r.Context(), workspace, agentID, request); err != nil {
		slog.Error("could not persist agent configuration", "workspace_id", workspace.ID, "agent_id", agentID, "error", err)
		problem(w, http.StatusServiceUnavailable, "configuration_failed", "Could not apply agent configuration")
		return
	}
	bootstrap := model.Task{ID: "configuration-" + agentID, WorkspaceID: workspace.ID, Owner: user, AgentID: agentID, AgentName: request.ResourceName, Phase: model.PhaseQueued}
	if err := s.kube.EnsureAgent(r.Context(), workspace, bootstrap); err != nil {
		slog.Error("could not reconcile agent", "workspace_id", workspace.ID, "agent_id", agentID, "error", err)
		problem(w, http.StatusServiceUnavailable, "agent_failed", "Configuration was saved but the Agent could not be provisioned")
		return
	}
	writeJSON(w, status, normalizedAgentConfig(workspace, agentID, request))
}

var environmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var sensitiveEnvironmentName = regexp.MustCompile(`(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|API_KEY|PRIVATE_KEY|ACCESS_KEY)($|_)`)
var platformEnvironmentName = regexp.MustCompile(`^(DO_NOT_TRACK|OPENCODE_DISABLE_AUTOUPDATE|OPENCODE_DISABLE_MODELS_FETCH)$`)

func validEnvironment(values map[string]string) bool {
	if len(values) > 32 {
		return false
	}
	total := 0
	for name, value := range values {
		total += len(name) + len(value)
		if len(name) > 128 || len(value) > 4096 || total > 16*1024 || !environmentName.MatchString(name) || sensitiveEnvironmentName.MatchString(name) || platformEnvironmentName.MatchString(name) {
			return false
		}
	}
	return true
}

func validSkillSources(values []model.SkillSource) bool {
	if len(values) > 5 {
		return false
	}
	seen := map[string]bool{}
	for i := range values {
		value := &values[i]
		value.Name, value.Repository, value.Ref, value.Path = strings.TrimSpace(value.Name), strings.TrimSpace(value.Repository), strings.TrimSpace(value.Ref), strings.TrimSpace(value.Path)
		if !validAgentID(value.Name) || seen[value.Name] || len(value.Repository) > 2048 || len(value.Ref) > 255 || len(value.Path) > 512 || strings.Contains(value.Path, "..") {
			return false
		}
		seen[value.Name] = true
		parsed, err := url.Parse(value.Repository)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return false
		}
		if len(value.Names) > 50 {
			return false
		}
		for j := range value.Names {
			value.Names[j] = strings.TrimSpace(value.Names[j])
			if !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`).MatchString(value.Names[j]) || len(value.Names[j]) > 253 {
				return false
			}
		}
	}
	return true
}

func (s *Service) deleteAgentConfig(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r.Context())
	workspace, ok := s.requestWorkspace(r)
	agentID := requestedAgentID(r)
	if !ok || !validAgentID(agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	if agentID == "default" {
		problem(w, http.StatusConflict, "default_agent", "The default Agent cannot be deleted")
		return
	}
	active, err := s.kube.ListActiveRecords(r.Context())
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "lookup_failed", "Could not inspect active tasks")
		return
	}
	for _, task := range active {
		if task.WorkspaceID == workspace.ID && task.Owner == user && task.AgentID == agentID {
			problem(w, http.StatusConflict, "agent_busy", "Agent has an active task")
			return
		}
	}
	if err := s.kube.DeleteAgent(r.Context(), workspace, agentID); err != nil {
		problem(w, http.StatusServiceUnavailable, "delete_failed", "Could not delete Agent")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) agentStatus(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	agentID := requestedAgentID(r)
	if !ok || !validAgentID(agentID) || !s.agentExists(r.Context(), workspace, agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	ready, err := s.kube.AgentRolloutReady(r.Context(), workspace, agentID)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "agent_unavailable", "Could not inspect Agent readiness")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ready": ready})
}

func (s *Service) attachAgent(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	agentID := requestedAgentID(r)
	if !ok || !validAgentID(agentID) || !s.agentExists(r.Context(), workspace, agentID) {
		problem(w, http.StatusNotFound, "agent_not_found", "Agent not found")
		return
	}
	target, err := url.Parse(s.cfg.KubeOpenCodeURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		problem(w, http.StatusServiceUnavailable, "attach_unavailable", "Agent attachment is unavailable")
		return
	}
	prefix := fmt.Sprintf("/api/v1/namespaces/%s/agents/%s/proxy", url.PathEscape(workspace.Namespace), url.PathEscape(agentName(workspace.ID, agentID)))
	rest := r.PathValue("rest")
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = prefix + "/" + rest
		req.Host = target.Host
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("X-Forwarded-User")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(response http.ResponseWriter, _ *http.Request, _ error) {
		problem(response, http.StatusBadGateway, "attach_unavailable", "Agent attachment is unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func normalizedAgentConfig(workspace config.Workspace, agentID string, value model.AgentConfig) model.AgentConfig {
	value.ID = firstAgentID(agentID)
	value.ResourceName = agentName(workspace.ID, value.ID)
	if strings.TrimSpace(value.Profile) == "" {
		value.Profile = "Jupyter workspace agent for " + workspace.ID
	}
	if !stringIn(workspace.AllowedModels, value.Model) {
		value.Model = workspace.OpenCodeModel
	}
	value.AllowedModels = append([]string(nil), workspace.AllowedModels...)
	return value
}

func requestedAgentID(r *http.Request) string { return firstAgentID(r.PathValue("agent")) }
func firstAgentID(value string) string {
	if value == "" {
		return "default"
	}
	return value
}
func validAgentID(value string) bool {
	return len(value) <= 30 && regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(value)
}
func stringIn(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

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

func (s *Service) cleanupAuthorized(authorization string) bool {
	if s.cfg.CleanupToken == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.CleanupToken)) == 1
}

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

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ReconcileInterval)
	defer ticker.Stop()
	s.reconcileAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileAll(ctx)
		}
	}
}

func (s *Service) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
	if err := decodeJSON(r, &request); err != nil || strings.TrimSpace(request.Description) == "" || len(request.Description) > 32*1024 {
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

func (s *Service) getTask(w http.ResponseWriter, r *http.Request) {
	_, task, ok := s.authorizedTask(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, task)
}

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
	if err := decodeJSON(r, &request); err != nil || request.Path == "" || path.IsAbs(request.Path) || strings.Contains(request.Path, "..") {
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

func (s *Service) reconcileAll(ctx context.Context) {
	tasks, err := s.kube.ListActiveRecords(ctx)
	if err != nil {
		slog.Error("list active mediator tasks", "error", err)
		return
	}
	for i := range tasks {
		task := tasks[i]
		workspace, exists := s.cfg.Workspaces[task.WorkspaceID]
		if !exists {
			continue
		}
		mutexValue, _ := s.workMu.LoadOrStore(task.ID, &sync.Mutex{})
		mutex := mutexValue.(*sync.Mutex)
		if !mutex.TryLock() {
			continue
		}
		go func() {
			defer mutex.Unlock()
			s.lifecycleMu.RLock()
			defer s.lifecycleMu.RUnlock()
			s.reconcile(ctx, workspace, &task)
		}()
	}
}

func (s *Service) reconcile(ctx context.Context, workspace config.Workspace, task *model.Task) {
	if err := s.kube.RenewWorkspace(ctx, workspace, task.ID); err != nil {
		slog.Error("renew workspace lease", "task", task.ID, "error", err)
		return
	}
	if task.CancelRequest {
		_ = s.kube.DeleteKubeTask(ctx, workspace, *task)
		task.Phase, task.Message, task.UpdatedAt = model.PhaseCancelled, firstNonEmpty(task.Message, "Cancelled by user"), time.Now().UTC()
		_ = s.kube.UpdateRecord(ctx, workspace, *task)
		_ = s.kube.ReleaseWorkspace(ctx, workspace, task.ID)
		return
	}
	switch task.Phase {
	case model.PhaseSnapshotting:
		phase, message, err := s.kube.SnapshotJobPhase(ctx, workspace, *task, false)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				slog.Error("inspect snapshot job", "task", task.ID, "error", err)
			}
			return
		}
		if phase == kube.JobFailed {
			s.fail(ctx, workspace, task, firstNonEmpty(message, "Snapshot failed"))
			return
		}
		if phase != kube.JobSucceeded {
			return
		}
		if err := s.kube.EnsureAgent(ctx, workspace, *task); err != nil {
			s.fail(ctx, workspace, task, "Could not create isolated agent: "+err.Error())
			return
		}
		if err := s.kube.CreateKubeTask(ctx, workspace, *task); err != nil {
			s.fail(ctx, workspace, task, "Could not submit KubeOpenCode task: "+err.Error())
			return
		}
		task.Phase, task.Message = model.PhaseQueued, "Snapshot complete; waiting for KubeOpenCode"
	case model.PhaseQueued, model.PhaseRunning:
		phase, message, files, err := s.kube.KubeTaskPhase(ctx, workspace, *task)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				slog.Error("inspect KubeOpenCode task", "task", task.ID, "error", err)
			}
			return
		}
		task.Phase, task.Message, task.ChangedFiles = phase, message, files
		if task.Terminal() {
			task.CompletionPhase = phase
			if err := s.kube.CreateChangesJob(ctx, workspace, *task); err != nil {
				s.fail(ctx, workspace, task, "Could not inspect workspace changes: "+err.Error())
				return
			}
			task.Phase, task.Message = model.PhaseCollecting, "Collecting changed files"
		}
	case model.PhaseCollecting:
		phase, files, message, err := s.kube.ChangesJobPhase(ctx, workspace, *task)
		if err != nil {
			return
		}
		if phase == kube.JobFailed {
			s.fail(ctx, workspace, task, firstNonEmpty(message, "Change scan failed"))
			return
		}
		if phase != kube.JobSucceeded {
			return
		}
		task.ChangedFiles = files
		task.Phase = task.CompletionPhase
		task.CompletionPhase = ""
		_ = s.kube.ReleaseWorkspace(ctx, workspace, task.ID)
	case model.PhaseRestoring:
		phase, message, err := s.kube.SnapshotJobPhase(ctx, workspace, *task, true)
		if err != nil {
			return
		}
		if phase == kube.JobFailed {
			s.fail(ctx, workspace, task, firstNonEmpty(message, "Restore failed"))
			return
		}
		if phase != kube.JobSucceeded {
			return
		}
		task.Phase, task.Message = model.PhaseRestored, "Protected snapshot restored"
		_ = s.kube.ReleaseWorkspace(ctx, workspace, task.ID)
	default:
		return
	}
	task.UpdatedAt = time.Now().UTC()
	if err := s.kube.UpdateRecord(ctx, workspace, *task); err != nil {
		slog.Error("update mediator task", "task", task.ID, "error", err)
	}
}

func (s *Service) fail(ctx context.Context, workspace config.Workspace, task *model.Task, message string) {
	task.Phase, task.Message, task.UpdatedAt = model.PhaseFailed, message, time.Now().UTC()
	_ = s.kube.UpdateRecord(ctx, workspace, *task)
	_ = s.kube.ReleaseWorkspace(ctx, workspace, task.ID)
}

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

func (s *Service) ownedWorkspace(id, user string) (config.Workspace, bool) {
	workspace, ok := s.cfg.Workspaces[id]
	return workspace, ok && workspace.Owner == user
}
func currentUser(ctx context.Context) string {
	value, _ := ctx.Value(userContextKey{}).(string)
	return value
}

type userContextKey struct{}

func randomID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
func agentName(workspaceID, agentID string) string {
	if firstAgentID(agentID) != "default" {
		return "vscode-" + safeAPIName(workspaceID) + "-" + safeAPIName(agentID)
	}
	return "vscode-" + safeAPIName(workspaceID)
}
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
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nilResponseWriter{}, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

type nilResponseWriter struct{}

func (nilResponseWriter) Header() http.Header       { return make(http.Header) }
func (nilResponseWriter) Write([]byte) (int, error) { return 0, nil }
func (nilResponseWriter) WriteHeader(int)           {}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, map[string]any{"type": "about:blank", "title": code, "status": status, "detail": detail})
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ = errors.Is
