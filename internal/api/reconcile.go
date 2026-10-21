package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// reconcileAll schedules policy and task reconciliation without overlapping work.
func (s *Service) reconcileAll(ctx context.Context) {
	tasks, err := s.kube.ListActiveRecords(ctx)
	if err != nil {
		slog.Error("list active mediator tasks", "error", err)
		return
	}
	busyWorkspaces := map[string]bool{}
	for i := range tasks {
		busyWorkspaces[tasks[i].WorkspaceID] = true
	}
	for _, workspace := range s.cfg.Workspaces {
		if !busyWorkspaces[workspace.ID] {
			s.reconcileAgentPolicy(ctx, workspace)
		}
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

// reconcileAgentPolicy reapplies required Registry policy to idle workspace Agents once.
func (s *Service) reconcileAgentPolicy(ctx context.Context, workspace config.Workspace) {
	if _, ready := s.policyReady.Load(workspace.ID); ready {
		return
	}
	values, err := s.kube.ListAgentConfigs(ctx, workspace)
	if err != nil {
		slog.Error("list agents for platform policy reconciliation", "workspace_id", workspace.ID, "error", err)
		return
	}
	for _, value := range values {
		agentID := firstAgentID(value.ID)
		bootstrap := model.Task{ID: "policy-" + agentID, WorkspaceID: workspace.ID, Owner: workspace.Owner, AgentID: agentID, AgentName: agentName(workspace.ID, agentID), Phase: model.PhaseQueued}
		if err := s.kube.EnsureAgent(ctx, workspace, bootstrap); err != nil {
			slog.Error("reconcile platform Registry policy", "workspace_id", workspace.ID, "agent_id", agentID, "error", err)
			return
		}
	}
	s.policyReady.Store(workspace.ID, true)
	slog.Info("platform Registry policy reconciled", "workspace_id", workspace.ID, "agents", len(values))
}

// reconcile advances one task through its persisted lifecycle state machine.
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

// fail marks a task failed and releases its workspace lease.
func (s *Service) fail(ctx context.Context, workspace config.Workspace, task *model.Task, message string) {
	task.Phase, task.Message, task.UpdatedAt = model.PhaseFailed, message, time.Now().UTC()
	_ = s.kube.UpdateRecord(ctx, workspace, *task)
	_ = s.kube.ReleaseWorkspace(ctx, workspace, task.ID)
}
