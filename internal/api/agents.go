package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
)

// requestWorkspace resolves the workspace addressed by a request and verifies ownership.
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

// getAgentConfig returns the configuration of an owned Agent.
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

// listAgentConfigs lists the Agents available in the request workspace.
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

// createAgentConfig validates and creates a non-default Agent.
func (s *Service) createAgentConfig(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	var request model.AgentConfig
	if err := decodeJSON(w, r, &request); err != nil || !validAgentID(request.ID) || request.ID == "default" {
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

// updateAgentConfig validates and updates an existing Agent.
func (s *Service) updateAgentConfig(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	var request model.AgentConfig
	if err := decodeJSON(w, r, &request); err != nil {
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

// saveAgentConfig persists a validated Agent configuration and reconciles its runtime.
func (s *Service) saveAgentConfig(w http.ResponseWriter, r *http.Request, workspace config.Workspace, agentID string, request model.AgentConfig, status int) {
	user := currentUser(r.Context())
	request.Profile = strings.TrimSpace(request.Profile)
	request.Instructions = strings.TrimSpace(request.Instructions)
	request.Model = strings.TrimSpace(request.Model)
	if request.Profile == "" || len(request.Profile) > 200 || len(request.Instructions) > 16*1024 || !stringIn(workspace.AllowedModels, request.Model) || !validSkillSources(request.CustomSkillSources) || !validEnvironment(request.Environment) {
		problem(w, http.StatusBadRequest, "invalid_configuration", "Profile, instructions, selected model or environment is invalid")
		return
	}
	registry, err := s.registry(r.Context(), workspace)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "registry_unavailable", "Could not read the platform Registry")
		return
	}
	if !resolveRegistrySelection(workspace, registry, &request) {
		problem(w, http.StatusBadRequest, "invalid_registry_selection", "Selected skills or plugins are not allowed by the platform Registry")
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

// validEnvironment reports whether Agent environment overrides are bounded and non-sensitive.
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

// validSkillSources validates and normalizes custom Git skill sources.
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

// deleteAgentConfig removes an idle non-default Agent.
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

// agentStatus reports whether an owned Agent rollout is ready.
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

// attachAgent relays an authenticated request to the selected KubeOpenCode Agent.
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

// normalizedAgentConfig returns a stable, user-safe Agent configuration.
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
	for _, required := range workspace.RequiredSkills {
		if !stringIn(value.SelectedSkills, required) {
			value.SelectedSkills = append(value.SelectedSkills, required)
		}
	}
	sort.Strings(value.SelectedSkills)
	// Resolved CRD payloads remain mediator-internal. Clients only receive stable
	// Registry identifiers and cannot discover Secret names or inject sources.
	value.SkillSources = nil
	value.PluginSpecs = nil
	return value
}

// requestedAgentID returns the requested Agent identifier or the default identifier.
func requestedAgentID(r *http.Request) string { return firstAgentID(r.PathValue("agent")) }

// firstAgentID normalizes an empty Agent identifier to default.
func firstAgentID(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

// validAgentID reports whether a value is a bounded DNS-style Agent identifier.
func validAgentID(value string) bool {
	return len(value) <= 30 && regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(value)
}

// stringIn reports whether wanted appears in values.
func stringIn(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
