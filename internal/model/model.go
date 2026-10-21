package model

import "time"

type AgentConfig struct {
	ID            string            `json:"id"`
	ResourceName  string            `json:"resourceName"`
	Profile       string            `json:"profile"`
	Instructions  string            `json:"instructions"`
	Model         string            `json:"model"`
	SkillSources  []SkillSource     `json:"skillSources,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	AllowedModels []string          `json:"allowedModels,omitempty"`
	UpdatedAt     time.Time         `json:"updatedAt,omitempty"`
}

type SkillSource struct {
	Name       string   `json:"name"`
	Repository string   `json:"repository"`
	Ref        string   `json:"ref,omitempty"`
	Path       string   `json:"path,omitempty"`
	Names      []string `json:"names,omitempty"`
}

type Phase string

const (
	PhaseSnapshotting Phase = "Snapshotting"
	PhaseQueued       Phase = "Queued"
	PhaseRunning      Phase = "Running"
	PhaseSucceeded    Phase = "Succeeded"
	PhaseFailed       Phase = "Failed"
	PhaseCancelled    Phase = "Cancelled"
	PhaseRestoring    Phase = "Restoring"
	PhaseRestored     Phase = "Restored"
	PhaseCollecting   Phase = "CollectingChanges"
)

type Task struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspaceId"`
	Owner           string    `json:"owner"`
	Description     string    `json:"description,omitempty"`
	Phase           Phase     `json:"phase"`
	Message         string    `json:"message,omitempty"`
	KubeTaskName    string    `json:"kubeTaskName,omitempty"`
	AgentName       string    `json:"agentName,omitempty"`
	AgentID         string    `json:"agentId,omitempty"`
	SnapshotName    string    `json:"snapshotName"`
	ChangedFiles    []string  `json:"changedFiles,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	CancelRequest   bool      `json:"cancelRequested,omitempty"`
	CompletionPhase Phase     `json:"completionPhase,omitempty"`
}

func (t Task) Terminal() bool {
	return t.Phase == PhaseSucceeded || t.Phase == PhaseFailed || t.Phase == PhaseCancelled || t.Phase == PhaseRestored
}
