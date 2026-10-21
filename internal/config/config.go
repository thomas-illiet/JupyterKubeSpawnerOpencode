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

	"k8s.io/apimachinery/pkg/api/resource"
)

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
	SnapshotPVC      string   `json:"snapshotPvc"`
	RuntimeClassName string   `json:"runtimeClassName,omitempty"`
	ExecutorImage    string   `json:"executorImage,omitempty"`
	AgentImage       string   `json:"agentImage,omitempty"`
	AttachImage      string   `json:"attachImage,omitempty"`
	RunAsUser        int64    `json:"runAsUser,omitempty"`
	RunAsGroup       int64    `json:"runAsGroup,omitempty"`
	FSGroup          int64    `json:"fsGroup,omitempty"`
	ModelProxyURL    string   `json:"modelProxyUrl,omitempty"`
}

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

func FromEnv() (Config, error) {
	cfg := Config{
		ListenAddress:      env("LISTEN_ADDRESS", ":8080"),
		JupyterHubUserURL:  env("JUPYTERHUB_USER_URL", "http://hub:8081/hub/api/user"),
		KubeOpenCodeURL:    env("KUBEOPENCODE_SERVER_URL", "http://kubeopencode-server.kubeopencode-system.svc.cluster.local:2746"),
		AuthTimeout:        durationEnv("AUTH_TIMEOUT", 5*time.Second),
		ReconcileInterval:  durationEnv("RECONCILE_INTERVAL", 2*time.Second),
		SnapshotImage:      env("SNAPSHOT_IMAGE", "alpine:3.22"),
		AgentCPURequest:    env("AGENT_CPU_REQUEST", "250m"),
		AgentCPULimit:      env("AGENT_CPU_LIMIT", "2"),
		AgentMemoryRequest: env("AGENT_MEMORY_REQUEST", "512Mi"),
		AgentMemoryLimit:   env("AGENT_MEMORY_LIMIT", "4Gi"),
		EphemeralLimit:     env("AGENT_EPHEMERAL_STORAGE_LIMIT", "4Gi"),
		TaskTimeout:        durationEnv("TASK_TIMEOUT", time.Hour),
	}
	if tokenFile := os.Getenv("CLEANUP_TOKEN_FILE"); tokenFile != "" {
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

	path := env("WORKSPACES_FILE", "/etc/kubeopencode-mediator/workspaces.json")
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

func validateWorkspace(workspace Workspace) error {
	if len(workspace.ID) > 50 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(workspace.ID) {
		return fmt.Errorf("workspace id %q must be a lowercase DNS label of at most 50 characters", workspace.ID)
	}
	if workspace.ID == "" || workspace.Owner == "" || workspace.Namespace == "" || workspace.PVC == "" || workspace.SnapshotPVC == "" {
		return fmt.Errorf("workspace %q is missing a required field", workspace.ID)
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
	if workspace.ModelProxyURL != "" && !strings.HasPrefix(workspace.ModelProxyURL, "http://") && !strings.HasPrefix(workspace.ModelProxyURL, "https://") {
		return fmt.Errorf("workspace %q has invalid modelProxyUrl", workspace.ID)
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
