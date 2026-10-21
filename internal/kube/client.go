package kube

import (
	"context"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
)

type JobPhase string

const (
	JobPending   JobPhase = "Pending"
	JobSucceeded JobPhase = "Succeeded"
	JobFailed    JobPhase = "Failed"
)

type Client interface {
	CreateRecord(context.Context, model.Task) error
	GetRecord(context.Context, config.Workspace, string) (model.Task, error)
	UpdateRecord(context.Context, config.Workspace, model.Task) error
	ListActiveRecords(context.Context) ([]model.Task, error)
	AcquireWorkspace(context.Context, config.Workspace, string) (bool, error)
	RenewWorkspace(context.Context, config.Workspace, string) error
	ReleaseWorkspace(context.Context, config.Workspace, string) error
	CreateSnapshotJob(context.Context, config.Workspace, model.Task, bool) error
	SnapshotJobPhase(context.Context, config.Workspace, model.Task, bool) (JobPhase, string, error)
	CreateChangesJob(context.Context, config.Workspace, model.Task) error
	ChangesJobPhase(context.Context, config.Workspace, model.Task) (JobPhase, []string, string, error)
	EnsureAgent(context.Context, config.Workspace, model.Task) error
	CreateKubeTask(context.Context, config.Workspace, model.Task) error
	KubeTaskPhase(context.Context, config.Workspace, model.Task) (model.Phase, string, []string, error)
	TaskOutput(context.Context, config.Workspace, model.Task) (string, error)
	DeleteKubeTask(context.Context, config.Workspace, model.Task) error
	ListAgentConfigs(context.Context, config.Workspace) ([]model.AgentConfig, error)
	GetAgentConfig(context.Context, config.Workspace, string) (model.AgentConfig, error)
	SetAgentConfig(context.Context, config.Workspace, string, model.AgentConfig) error
	DeleteAgent(context.Context, config.Workspace, string) error
	DeleteWorkspaceResources(context.Context, config.Workspace) error
	AgentRolloutReady(context.Context, config.Workspace, string) (bool, error)
}
