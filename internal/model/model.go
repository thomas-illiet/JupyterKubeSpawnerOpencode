package model

import "time"

type AgentConfig struct {
	ID                 string            `json:"id"`
	ResourceName       string            `json:"resourceName"`
	Profile            string            `json:"profile"`
	Instructions       string            `json:"instructions"`
	Model              string            `json:"model"`
	SkillSources       []SkillSource     `json:"skillSources,omitempty"`
	CustomSkillSources []SkillSource     `json:"customSkillSources,omitempty"`
	SelectedSkills     []string          `json:"selectedSkills,omitempty"`
	SelectedPlugins    []string          `json:"selectedPlugins,omitempty"`
	PluginSpecs        []PluginSpec      `json:"pluginSpecs,omitempty"`
	Environment        map[string]string `json:"environment,omitempty"`
	AllowedModels      []string          `json:"allowedModels,omitempty"`
	UpdatedAt          time.Time         `json:"updatedAt,omitempty"`
}

// Registry is the user-safe projection of a KubeOpenCode Registry CR.
type Registry struct {
	Name    string           `json:"name"`
	Skills  []RegistrySkill  `json:"skills"`
	Plugins []RegistryPlugin `json:"plugins"`
}

type RegistrySkill struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Required    bool        `json:"required"`
	Source      SkillSource `json:"-"`
}

type RegistryPlugin struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	Plugin      PluginSpec `json:"-"`
}

type PluginSpec struct {
	Name    string         `json:"name"`
	Target  string         `json:"target,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

type SkillSource struct {
	Name              string   `json:"name"`
	Repository        string   `json:"repository"`
	Ref               string   `json:"ref,omitempty"`
	Path              string   `json:"path,omitempty"`
	Names             []string `json:"names,omitempty"`
	Depth             *int64   `json:"depth,omitempty"`
	RecurseSubmodules bool     `json:"recurseSubmodules,omitempty"`
	SecretRef         string   `json:"secretRef,omitempty"`
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
