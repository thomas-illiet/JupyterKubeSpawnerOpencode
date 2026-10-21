package api

import (
	"net/http"
	"sync"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/auth"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
)

// Service exposes the authenticated mediator API and coordinates task reconciliation.
type Service struct {
	cfg         config.Config
	kube        kube.Client
	auth        auth.Authenticator
	mux         *http.ServeMux
	workMu      sync.Map
	policyReady sync.Map
	lifecycleMu sync.RWMutex
}

// NewService constructs an HTTP service and registers all API routes.
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
	s.mux.HandleFunc("GET /api/v1/me/registry", s.getRegistry)
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
