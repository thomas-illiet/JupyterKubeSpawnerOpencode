package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Workspace describes the administrator-owned runtime boundary for one user.
type Workspace struct {
	ID               string   `json:"id"`
	Owner            string   `json:"owner"`
	Namespace        string   `json:"namespace"`
	PVC              string   `json:"pvc"`
	SubPath          string   `json:"subPath"`
	ModelSecret      string   `json:"modelSecret"`
	ModelSecretKey   string   `json:"modelSecretKey"`
	ModelEnvironment string   `json:"modelEnvironment"`
	OpenCodeBaseURL  string   `json:"openCodeBaseUrl,omitempty"`
	OpenCodeModel    string   `json:"openCodeModel,omitempty"`
	AllowedModels    []string `json:"allowedModels,omitempty"`
	ModelMTLS        bool     `json:"modelMTLS,omitempty"`
	SnapshotPVC      string   `json:"snapshotPvc"`
	RuntimeClassName string   `json:"runtimeClassName,omitempty"`
	ExecutorImage    string   `json:"executorImage,omitempty"`
	AgentImage       string   `json:"agentImage,omitempty"`
	AttachImage      string   `json:"attachImage,omitempty"`
	RunAsUser        int64    `json:"runAsUser,omitempty"`
	RunAsGroup       int64    `json:"runAsGroup,omitempty"`
	FSGroup          int64    `json:"fsGroup,omitempty"`
	ModelProxyURL    string   `json:"modelProxyUrl,omitempty"`
	Registry         string   `json:"registry"`
	RequiredSkills   []string `json:"requiredSkills,omitempty"`
}

// Config contains the validated mediator runtime configuration.
type Config struct {
	ListenAddress      string
	JupyterHubUserURL  string
	KubeOpenCodeURL    string
	AuthTimeout        time.Duration
	ReconcileInterval  time.Duration
	SnapshotImage      string
	AgentCPURequest    string
	AgentCPULimit      string
	AgentMemoryRequest string
	AgentMemoryLimit   string
	EphemeralLimit     string
	TaskTimeout        time.Duration
	CleanupToken       string
	Workspaces         map[string]Workspace
}

// NewViper returns an isolated Viper instance configured with mediator defaults
// and direct environment-variable lookup.
func NewViper() *viper.Viper {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	v.SetDefault("listen-address", ":8080")
	v.SetDefault("jupyterhub-user-url", "http://hub:8081/hub/api/user")
	v.SetDefault("kubeopencode-server-url", "http://kubeopencode-server.kubeopencode-system.svc.cluster.local:2746")
	v.SetDefault("auth-timeout", 5*time.Second)
	v.SetDefault("reconcile-interval", 2*time.Second)
	v.SetDefault("snapshot-image", "alpine:3.22")
	v.SetDefault("agent-cpu-request", "250m")
	v.SetDefault("agent-cpu-limit", "2")
	v.SetDefault("agent-memory-request", "512Mi")
	v.SetDefault("agent-memory-limit", "4Gi")
	v.SetDefault("agent-ephemeral-storage-limit", "4Gi")
	v.SetDefault("task-timeout", time.Hour)
	v.SetDefault("workspaces-file", "/etc/kubeopencode-mediator/workspaces.json")
	return v
}

// FromEnv loads mediator configuration from environment variables via Viper.
func FromEnv() (Config, error) {
	return FromViper(NewViper())
}

// FromViper decodes and validates mediator configuration from an isolated Viper instance.
func FromViper(v *viper.Viper) (Config, error) {
	cfg := Config{
		ListenAddress:      v.GetString("listen-address"),
		JupyterHubUserURL:  v.GetString("jupyterhub-user-url"),
		KubeOpenCodeURL:    v.GetString("kubeopencode-server-url"),
		AuthTimeout:        v.GetDuration("auth-timeout"),
		ReconcileInterval:  v.GetDuration("reconcile-interval"),
		SnapshotImage:      v.GetString("snapshot-image"),
		AgentCPURequest:    v.GetString("agent-cpu-request"),
		AgentCPULimit:      v.GetString("agent-cpu-limit"),
		AgentMemoryRequest: v.GetString("agent-memory-request"),
		AgentMemoryLimit:   v.GetString("agent-memory-limit"),
		EphemeralLimit:     v.GetString("agent-ephemeral-storage-limit"),
		TaskTimeout:        v.GetDuration("task-timeout"),
	}
	for name, value := range map[string]time.Duration{
		"AUTH_TIMEOUT":       cfg.AuthTimeout,
		"RECONCILE_INTERVAL": cfg.ReconcileInterval,
		"TASK_TIMEOUT":       cfg.TaskTimeout,
	} {
		if value <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration", name)
		}
	}
	if tokenFile := v.GetString("cleanup-token-file"); tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return Config{}, fmt.Errorf("read cleanup token file: %w", err)
		}
		cfg.CleanupToken = strings.TrimSpace(string(data))
		if len(cfg.CleanupToken) < 32 {
			return Config{}, errors.New("cleanup token must contain at least 32 characters")
		}
	}

	for name, value := range map[string]string{
		"AGENT_CPU_REQUEST":             cfg.AgentCPURequest,
		"AGENT_CPU_LIMIT":               cfg.AgentCPULimit,
		"AGENT_MEMORY_REQUEST":          cfg.AgentMemoryRequest,
		"AGENT_MEMORY_LIMIT":            cfg.AgentMemoryLimit,
		"AGENT_EPHEMERAL_STORAGE_LIMIT": cfg.EphemeralLimit,
	} {
		if _, err := resource.ParseQuantity(value); err != nil {
			return Config{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	for name, value := range map[string]string{"JUPYTERHUB_USER_URL": cfg.JupyterHubUserURL, "KUBEOPENCODE_SERVER_URL": cfg.KubeOpenCodeURL} {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return Config{}, fmt.Errorf("%s must be an HTTP(S) URL without embedded credentials", name)
		}
	}

	path := v.GetString("workspaces-file")
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read workspaces file: %w", err)
	}
	var workspaces []Workspace
	if err := json.Unmarshal(data, &workspaces); err != nil {
		return Config{}, fmt.Errorf("decode workspaces file: %w", err)
	}
	cfg.Workspaces = make(map[string]Workspace, len(workspaces))
	for i := range workspaces {
		workspace := workspaces[i]
		if workspace.RunAsUser == 0 {
			workspace.RunAsUser = 1000
		}
		if len(workspace.AllowedModels) == 0 && workspace.OpenCodeModel != "" {
			workspace.AllowedModels = []string{workspace.OpenCodeModel}
		}
		if workspace.RunAsGroup == 0 {
			workspace.RunAsGroup = 1000
		}
		if workspace.FSGroup == 0 {
			workspace.FSGroup = workspace.RunAsGroup
		}
		if err := validateWorkspace(workspace); err != nil {
			return Config{}, err
		}
		if _, exists := cfg.Workspaces[workspace.ID]; exists {
			return Config{}, fmt.Errorf("duplicate workspace id %q", workspace.ID)
		}
		for _, existing := range cfg.Workspaces {
			if existing.Owner == workspace.Owner {
				return Config{}, fmt.Errorf("owner %q must map to exactly one workspace", workspace.Owner)
			}
		}
		cfg.Workspaces[workspace.ID] = workspace
	}
	if len(cfg.Workspaces) == 0 {
		return Config{}, errors.New("at least one workspace is required")
	}
	return cfg, nil
}

// validateWorkspace verifies required fields and storage, identity, model, and Registry constraints.
func validateWorkspace(workspace Workspace) error {
	if len(workspace.ID) > 50 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(workspace.ID) {
		return fmt.Errorf("workspace id %q must be a lowercase DNS label of at most 50 characters", workspace.ID)
	}
	if workspace.ID == "" || workspace.Owner == "" || workspace.Namespace == "" || workspace.PVC == "" || workspace.SnapshotPVC == "" || workspace.Registry == "" {
		return fmt.Errorf("workspace %q is missing a required field", workspace.ID)
	}
	if len(workspace.RequiredSkills) == 0 {
		return fmt.Errorf("workspace %q must define at least one required skill", workspace.ID)
	}
	seenSkills := map[string]bool{}
	for _, skill := range workspace.RequiredSkills {
		if !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(skill) || len(skill) > 63 || seenSkills[skill] {
			return fmt.Errorf("workspace %q has invalid or duplicate required skill %q", workspace.ID, skill)
		}
		seenSkills[skill] = true
	}
	if workspace.SubPath == "" || filepath.IsAbs(workspace.SubPath) || strings.Contains(workspace.SubPath, "..") {
		return fmt.Errorf("workspace %q has unsafe subPath %q", workspace.ID, workspace.SubPath)
	}
	hasSecret := workspace.ModelSecret != "" && workspace.ModelSecretKey != "" && workspace.ModelEnvironment != ""
	hasProxy := workspace.OpenCodeBaseURL != "" && workspace.OpenCodeModel != ""
	if !hasSecret && !hasProxy {
		return fmt.Errorf("workspace %q must define model secret mapping or an OpenCode-compatible proxy", workspace.ID)
	}
	if (workspace.OpenCodeBaseURL == "") != (workspace.OpenCodeModel == "") {
		return fmt.Errorf("workspace %q must define both openCodeBaseUrl and openCodeModel", workspace.ID)
	}
	for _, model := range workspace.AllowedModels {
		if strings.TrimSpace(model) == "" || len(model) > 200 {
			return fmt.Errorf("workspace %q has an invalid allowed model", workspace.ID)
		}
	}
	if workspace.OpenCodeBaseURL != "" && !strings.HasPrefix(workspace.OpenCodeBaseURL, "http://") && !strings.HasPrefix(workspace.OpenCodeBaseURL, "https://") {
		return fmt.Errorf("workspace %q has invalid openCodeBaseUrl", workspace.ID)
	}
	if workspace.ModelMTLS {
		parsed, err := url.Parse(workspace.OpenCodeBaseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("workspace %q modelMTLS requires an HTTPS openCodeBaseUrl without embedded credentials", workspace.ID)
		}
		if workspace.ExecutorImage == "" {
			return fmt.Errorf("workspace %q modelMTLS requires an executorImage containing jko-model-mtls-proxy", workspace.ID)
		}
	}
	if workspace.ModelProxyURL != "" && !strings.HasPrefix(workspace.ModelProxyURL, "http://") && !strings.HasPrefix(workspace.ModelProxyURL, "https://") {
		return fmt.Errorf("workspace %q has invalid modelProxyUrl", workspace.ID)
	}
	return nil
}
