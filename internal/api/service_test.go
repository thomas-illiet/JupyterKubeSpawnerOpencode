package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
)

type staticAuth struct{ user string }

func (a staticAuth) Authenticate(context.Context, string) (string, error) {
	if a.user == "" {
		return "", errors.New("unauthorized")
	}
	return a.user, nil
}

func TestHealthEndpointsAndStrictJSON(t *testing.T) {
	service := NewService(testConfig(), newFakeKube(), staticAuth{user: "alice"})

	live := request(t, service.Handler(), http.MethodGet, "/livez", nil)
	if live.Code != http.StatusOK {
		t.Fatalf("live status = %d", live.Code)
	}
	ready := request(t, service.Handler(), http.MethodGet, "/readyz", nil)
	if ready.Code != http.StatusServiceUnavailable || ready.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("ready status = %d content-type = %q", ready.Code, ready.Header().Get("Content-Type"))
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/alice-main/tasks", bytes.NewBufferString(`{"description":"one"} {"description":"two"}`))
	req.Header.Set("Authorization", "Bearer test-token-with-enough-length")
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func FuzzSafeAPIName(f *testing.F) {
	f.Add("reviewer")
	f.Add("Agent With Spaces")
	f.Add("../escape")
	f.Fuzz(func(t *testing.T, value string) {
		result := safeAPIName(value)
		if strings.ContainsAny(result, "_/. ") {
			t.Fatalf("unsafe normalized value %q", result)
		}
	})
}

type fakeKube struct {
	mu                sync.Mutex
	records           map[string]model.Task
	locked            map[string]string
	snapshotPhase     kube.JobPhase
	output            string
	agentConfigs      map[string]model.AgentConfig
	deletedWorkspaces []string
	registry          model.Registry
}

func newFakeKube() *fakeKube {
	return &fakeKube{records: map[string]model.Task{}, locked: map[string]string{}, snapshotPhase: kube.JobPending, agentConfigs: map[string]model.AgentConfig{}, registry: model.Registry{Name: "platform", Skills: []model.RegistrySkill{{Name: "platform-baseline", Description: "Mandatory platform rules", Source: model.SkillSource{Name: "platform-baseline", Repository: "https://github.com/example/platform-skills.git", Ref: "main", Path: "skills", Names: []string{"baseline"}}}, {Name: "code-review", Source: model.SkillSource{Name: "code-review", Repository: "https://github.com/example/review-skills.git", Ref: "v1"}}}, Plugins: []model.RegistryPlugin{{Name: "safety-net", Plugin: model.PluginSpec{Name: "cc-safety-net@0.8.2", Target: "server"}}}}}
}

func (f *fakeKube) GetRegistry(context.Context, config.Workspace) (model.Registry, error) {
	return f.registry, nil
}
func (f *fakeKube) CreateRecord(_ context.Context, task model.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[task.ID] = task
	return nil
}
func (f *fakeKube) GetRecord(_ context.Context, _ config.Workspace, id string) (model.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.records[id]
	if !ok {
		return model.Task{}, errors.New("not found")
	}
	return task, nil
}
func (f *fakeKube) UpdateRecord(_ context.Context, _ config.Workspace, task model.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[task.ID] = task
	return nil
}
func (f *fakeKube) ListActiveRecords(context.Context) ([]model.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []model.Task
	for _, task := range f.records {
		if !task.Terminal() {
			result = append(result, task)
		}
	}
	return result, nil
}
func (f *fakeKube) AcquireWorkspace(_ context.Context, workspace config.Workspace, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if holder := f.locked[workspace.ID]; holder != "" && holder != id {
		return false, nil
	}
	f.locked[workspace.ID] = id
	return true, nil
}
func (f *fakeKube) RenewWorkspace(_ context.Context, workspace config.Workspace, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked[workspace.ID] != id {
		return errors.New("lease lost")
	}
	return nil
}
func (f *fakeKube) ReleaseWorkspace(_ context.Context, workspace config.Workspace, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked[workspace.ID] == id {
		delete(f.locked, workspace.ID)
	}
	return nil
}
func (f *fakeKube) CreateSnapshotJob(context.Context, config.Workspace, model.Task, bool) error {
	return nil
}
func (f *fakeKube) SnapshotJobPhase(context.Context, config.Workspace, model.Task, bool) (kube.JobPhase, string, error) {
	return f.snapshotPhase, "", nil
}
func (f *fakeKube) CreateChangesJob(context.Context, config.Workspace, model.Task) error { return nil }
func (f *fakeKube) ChangesJobPhase(context.Context, config.Workspace, model.Task) (kube.JobPhase, []string, string, error) {
	return kube.JobSucceeded, []string{"Files README.md differ"}, "", nil
}
func (f *fakeKube) EnsureAgent(context.Context, config.Workspace, model.Task) error    { return nil }
func (f *fakeKube) CreateKubeTask(context.Context, config.Workspace, model.Task) error { return nil }
func (f *fakeKube) KubeTaskPhase(context.Context, config.Workspace, model.Task) (model.Phase, string, []string, error) {
	return model.PhaseRunning, "running", nil, nil
}
func (f *fakeKube) TaskOutput(context.Context, config.Workspace, model.Task) (string, error) {
	return f.output, nil
}
func (f *fakeKube) DeleteKubeTask(context.Context, config.Workspace, model.Task) error { return nil }
func agentConfigKey(workspace config.Workspace, agentID string) string {
	return workspace.ID + "/" + agentID
}
func (f *fakeKube) ListAgentConfigs(_ context.Context, workspace config.Workspace) ([]model.AgentConfig, error) {
	values := []model.AgentConfig{{ID: "default"}}
	for key, value := range f.agentConfigs {
		if key != agentConfigKey(workspace, "default") && len(key) > len(workspace.ID)+1 && key[:len(workspace.ID)+1] == workspace.ID+"/" {
			values = append(values, value)
		}
	}
	return values, nil
}
func (f *fakeKube) GetAgentConfig(_ context.Context, workspace config.Workspace, agentID string) (model.AgentConfig, error) {
	return f.agentConfigs[agentConfigKey(workspace, agentID)], nil
}
func (f *fakeKube) SetAgentConfig(_ context.Context, workspace config.Workspace, agentID string, value model.AgentConfig) error {
	f.agentConfigs[agentConfigKey(workspace, agentID)] = value
	return nil
}
func (f *fakeKube) DeleteAgent(_ context.Context, workspace config.Workspace, agentID string) error {
	delete(f.agentConfigs, agentConfigKey(workspace, agentID))
	return nil
}
func (f *fakeKube) DeleteWorkspaceResources(_ context.Context, workspace config.Workspace) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedWorkspaces = append(f.deletedWorkspaces, workspace.ID)
	for id, task := range f.records {
		if task.WorkspaceID == workspace.ID {
			delete(f.records, id)
		}
	}
	for key := range f.agentConfigs {
		if len(key) > len(workspace.ID) && key[:len(workspace.ID)+1] == workspace.ID+"/" {
			delete(f.agentConfigs, key)
		}
	}
	delete(f.locked, workspace.ID)
	return nil
}
func (f *fakeKube) AgentRolloutReady(context.Context, config.Workspace, string) (bool, error) {
	return true, nil
}

func testConfig() config.Config {
	return config.Config{ReconcileInterval: time.Hour, TaskTimeout: time.Hour, Workspaces: map[string]config.Workspace{
		"alice-main": {ID: "alice-main", Owner: "alice", Namespace: "jupyter-shared", PVC: "claim-alice", SnapshotPVC: "snapshots-alice", SubPath: "work/alice", ModelSecret: "model-alice", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY", OpenCodeModel: "model-proxy/auto", AllowedModels: []string{"model-proxy/auto", "model-proxy/code"}, Registry: "platform", RequiredSkills: []string{"platform-baseline"}},
		"bob-main":   {ID: "bob-main", Owner: "bob", Namespace: "jupyter-shared", PVC: "claim-bob", SnapshotPVC: "snapshots-bob", SubPath: "work/bob", ModelSecret: "model-bob", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY", Registry: "platform", RequiredSkills: []string{"platform-baseline"}},
	}}
}

func TestMeRoutesResolveWorkspaceFromAuthenticatedUser(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	response := request(t, service.Handler(), http.MethodGet, "/api/v1/me/agents", nil)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"resourceName":"vscode-alice-main"`)) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRegistryProjectionMarksRequiredSkillsWithoutExposingSources(t *testing.T) {
	service := NewService(testConfig(), newFakeKube(), staticAuth{user: "alice"})
	response := request(t, service.Handler(), http.MethodGet, "/api/v1/me/registry", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("registry status = %d body=%s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"name":"platform-baseline","description":"Mandatory platform rules","required":true`)) {
		t.Fatalf("required skill is not marked: %s", response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("github.com")) || bytes.Contains(response.Body.Bytes(), []byte("cc-safety-net")) {
		t.Fatalf("registry implementation details leaked: %s", response.Body.String())
	}
}

func TestAdminCleanupRequiresDedicatedTokenAndDeletesOwnedWorkspace(t *testing.T) {
	client := newFakeKube()
	cfg := testConfig()
	cfg.CleanupToken = "cleanup-token-that-is-at-least-32-characters"
	service := NewService(cfg, client, staticAuth{user: "alice"})

	denied := requestWithToken(t, service.Handler(), http.MethodDelete, "/api/v1/admin/users/alice/resources", nil, "ordinary-jupyter-user-token")
	if denied.Code != http.StatusUnauthorized || len(client.deletedWorkspaces) != 0 {
		t.Fatalf("unauthorized cleanup status=%d deleted=%v", denied.Code, client.deletedWorkspaces)
	}
	deleted := requestWithToken(t, service.Handler(), http.MethodDelete, "/api/v1/admin/users/alice/resources", nil, cfg.CleanupToken)
	if deleted.Code != http.StatusNoContent || len(client.deletedWorkspaces) != 1 || client.deletedWorkspaces[0] != "alice-main" {
		t.Fatalf("cleanup status=%d deleted=%v", deleted.Code, client.deletedWorkspaces)
	}
	unknown := requestWithToken(t, service.Handler(), http.MethodDelete, "/api/v1/admin/users/unknown/resources", nil, cfg.CleanupToken)
	if unknown.Code != http.StatusNoContent || len(client.deletedWorkspaces) != 1 {
		t.Fatalf("idempotent cleanup status=%d deleted=%v", unknown.Code, client.deletedWorkspaces)
	}
}

func TestAttachRelaysOwnedAgentWithoutForwardingCredentials(t *testing.T) {
	var gotPath, gotAuthorization, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuthorization, gotCookie = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	cfg := testConfig()
	cfg.KubeOpenCodeURL = upstream.URL
	service := NewService(cfg, newFakeKube(), staticAuth{user: "alice"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/agents/default/attach/session", bytes.NewBufferString("{}"))
	req.Header.Set("Authorization", "Bearer test-token-that-is-long-enough")
	req.Header.Set("Cookie", "sensitive=true")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if gotPath != "/api/v1/namespaces/jupyter-shared/agents/vscode-alice-main/proxy/session" {
		t.Fatalf("upstream path=%q", gotPath)
	}
	if gotAuthorization != "" || gotCookie != "" {
		t.Fatalf("credentials leaked upstream: authorization=%q cookie=%q", gotAuthorization, gotCookie)
	}
}

func TestAgentConfigurationIsOwnedAndModelAllowListed(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})

	read := request(t, service.Handler(), http.MethodGet, "/api/v1/workspaces/alice-main/agent", nil)
	if read.Code != http.StatusOK || !bytes.Contains(read.Body.Bytes(), []byte(`"allowedModels":["model-proxy/auto","model-proxy/code"]`)) {
		t.Fatalf("status = %d body=%s", read.Code, read.Body.String())
	}
	denied := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", map[string]string{"profile": "Alice agent", "instructions": "Use tests", "model": "external/stolen-token"})
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("disallowed model status = %d", denied.Code)
	}
	secretAttempt := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", map[string]string{"profile": "Alice agent", "instructions": "Use tests", "model": "model-proxy/auto", "apiToken": "must-not-be-accepted"})
	if secretAttempt.Code != http.StatusBadRequest {
		t.Fatalf("token field status = %d, want 400", secretAttempt.Code)
	}
	accepted := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", map[string]string{"profile": "Alice agent", "instructions": "Use tests", "model": "model-proxy/code"})
	if accepted.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", accepted.Code, accepted.Body.String())
	}
	if client.agentConfigs["alice-main/default"].Instructions != "Use tests" {
		t.Fatal("configuration was not persisted")
	}
	bob := request(t, service.Handler(), http.MethodGet, "/api/v1/workspaces/bob-main/agent", nil)
	if bob.Code != http.StatusNotFound {
		t.Fatalf("bob status = %d", bob.Code)
	}
}

func TestMultipleAgentsCanBeManagedAndTargeted(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	created := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/agents", map[string]string{"id": "reviewer", "profile": "Reviewer", "instructions": "Review only", "model": "model-proxy/code"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	listed := request(t, service.Handler(), http.MethodGet, "/api/v1/workspaces/alice-main/agents", nil)
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte(`"id":"reviewer"`)) {
		t.Fatalf("list status = %d body=%s", listed.Code, listed.Body.String())
	}
	taskResponse := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/agents/reviewer/tasks", map[string]string{"description": "review the workspace"})
	if taskResponse.Code != http.StatusAccepted {
		t.Fatalf("task status = %d body=%s", taskResponse.Code, taskResponse.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal(taskResponse.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.AgentID != "reviewer" || task.AgentName != "vscode-alice-main-reviewer" {
		t.Fatalf("unexpected targeted task: %+v", task)
	}
}

func TestAgentSkillSourcesAreValidated(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	valid := map[string]any{"profile": "Alice agent", "instructions": "Use shared skills", "model": "model-proxy/auto", "customSkillSources": []map[string]any{{"name": "team-skills", "repository": "https://github.com/example/team-skills.git", "ref": "main"}}, "selectedSkills": []string{"code-review"}, "selectedPlugins": []string{"safety-net"}}
	accepted := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", valid)
	if accepted.Code != http.StatusOK {
		t.Fatalf("valid skills status = %d body=%s", accepted.Code, accepted.Body.String())
	}
	if len(client.agentConfigs["alice-main/default"].SkillSources) != 3 || len(client.agentConfigs["alice-main/default"].CustomSkillSources) != 1 || len(client.agentConfigs["alice-main/default"].PluginSpecs) != 1 {
		t.Fatal("custom source, registry selection, and mandatory skill were not resolved")
	}
	invalid := map[string]any{"profile": "Alice agent", "instructions": "", "model": "model-proxy/auto", "selectedSkills": []string{"not-in-registry"}}
	denied := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", invalid)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("unknown registry skill status = %d, want 400", denied.Code)
	}
	credentialed := map[string]any{"profile": "Alice agent", "instructions": "", "model": "model-proxy/auto", "customSkillSources": []map[string]any{{"name": "private", "repository": "https://token@github.com/example/private.git"}}}
	if response := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", credentialed); response.Code != http.StatusBadRequest {
		t.Fatalf("credentialed custom repository status = %d, want 400", response.Code)
	}
}

func TestAgentEnvironmentRejectsSecrets(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	valid := map[string]any{"profile": "Alice agent", "instructions": "", "model": "model-proxy/auto", "environment": map[string]string{"BUILD_TARGET": "test"}}
	if response := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", valid); response.Code != http.StatusOK {
		t.Fatalf("safe environment status = %d body=%s", response.Code, response.Body.String())
	}
	invalid := map[string]any{"profile": "Alice agent", "instructions": "", "model": "model-proxy/auto", "environment": map[string]string{"GITHUB_TOKEN": "secret"}}
	if response := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", invalid); response.Code != http.StatusBadRequest {
		t.Fatalf("secret environment status = %d, want 400", response.Code)
	}
	reserved := map[string]any{"profile": "Alice agent", "instructions": "", "model": "model-proxy/auto", "environment": map[string]string{"OPENCODE_DISABLE_MODELS_FETCH": "0"}}
	if response := request(t, service.Handler(), http.MethodPut, "/api/v1/workspaces/alice-main/agent", reserved); response.Code != http.StatusBadRequest {
		t.Fatalf("platform environment status = %d, want 400", response.Code)
	}
}

func TestCreateTaskEnforcesWorkspaceOwnership(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})

	denied := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/bob-main/tasks", map[string]string{"description": "touch nothing"})
	if denied.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", denied.Code)
	}
	if len(client.records) != 0 {
		t.Fatal("unauthorized request created a record")
	}

	accepted := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/tasks", map[string]string{"description": "update README"})
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", accepted.Code, accepted.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal(accepted.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Owner != "alice" || task.Phase != model.PhaseSnapshotting {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestCreateTaskRejectsConcurrentWorkspaceUse(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	first := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/tasks", map[string]string{"description": "first"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}
	second := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/tasks", map[string]string{"description": "second"})
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409", second.Code)
	}
}

func TestSavedDocumentRequestsCancellation(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	now := time.Now().UTC()
	task := model.Task{ID: "task1", WorkspaceID: "alice-main", Owner: "alice", Phase: model.PhaseRunning, CreatedAt: now, UpdatedAt: now}
	client.records[task.ID] = task
	recorder := request(t, service.Handler(), http.MethodPost, "/api/v1/workspaces/alice-main/activity", map[string]string{"path": "src/main.go"})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !client.records[task.ID].CancelRequest {
		t.Fatal("active task was not marked for cancellation")
	}
}

func TestRestoreRequiresTerminalTask(t *testing.T) {
	client := newFakeKube()
	service := NewService(testConfig(), client, staticAuth{user: "alice"})
	now := time.Now().UTC()
	client.records["task1"] = model.Task{ID: "task1", WorkspaceID: "alice-main", Owner: "alice", Phase: model.PhaseRunning, CreatedAt: now, UpdatedAt: now}
	recorder := request(t, service.Handler(), http.MethodPost, "/api/v1/tasks/task1/restore", nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
}

func TestTaskOutputRequiresOwnership(t *testing.T) {
	client := newFakeKube()
	client.output = "OpenCode CLI output"
	now := time.Now().UTC()
	client.records["alice-task"] = model.Task{ID: "alice-task", WorkspaceID: "alice-main", Owner: "alice", KubeTaskName: "vscode-task-alice", Phase: model.PhaseRunning, CreatedAt: now, UpdatedAt: now}
	client.records["bob-task"] = model.Task{ID: "bob-task", WorkspaceID: "bob-main", Owner: "bob", KubeTaskName: "vscode-task-bob", Phase: model.PhaseRunning, CreatedAt: now, UpdatedAt: now}
	service := NewService(testConfig(), client, staticAuth{user: "alice"})

	allowed := request(t, service.Handler(), http.MethodGet, "/api/v1/tasks/alice-task/output", nil)
	if allowed.Code != http.StatusOK || allowed.Body.String() != client.output {
		t.Fatalf("status = %d body=%q", allowed.Code, allowed.Body.String())
	}
	denied := request(t, service.Handler(), http.MethodGet, "/api/v1/tasks/bob-task/output", nil)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", denied.Code)
	}
}

func request(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	return requestWithToken(t, handler, method, target, body, "test-token-that-is-long-enough")
}

func requestWithToken(t *testing.T, handler http.Handler, method, target string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
