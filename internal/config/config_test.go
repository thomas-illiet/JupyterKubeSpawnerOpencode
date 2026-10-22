package config

import "testing"

func TestNewViperReadsEnvironmentUsingFlagKeyNames(t *testing.T) {
	t.Setenv("LISTEN_ADDRESS", "127.0.0.1:9090")
	t.Setenv("TASK_TIMEOUT", "45m")
	v := NewViper()
	if got := v.GetString("listen-address"); got != "127.0.0.1:9090" {
		t.Fatalf("listen address = %q", got)
	}
	if got := v.GetDuration("task-timeout"); got.String() != "45m0s" {
		t.Fatalf("task timeout = %s", got)
	}
}

func TestFromViperRejectsInvalidDurationBeforeReadingFiles(t *testing.T) {
	v := NewViper()
	v.Set("reconcile-interval", "not-a-duration")
	if _, err := FromViper(v); err == nil {
		t.Fatal("expected invalid reconciliation duration to be rejected")
	}
}

func TestValidateWorkspaceRejectsTraversal(t *testing.T) {
	workspace := validTestWorkspace()
	workspace.SubPath = "../bob"
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestValidateWorkspaceAcceptsRelativeSubPath(t *testing.T) {
	workspace := validTestWorkspace()
	if err := validateWorkspace(workspace); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkspaceAcceptsInternalModelProxy(t *testing.T) {
	workspace := validTestWorkspace()
	workspace.ModelSecret, workspace.ModelSecretKey, workspace.ModelEnvironment = "", "", ""
	workspace.OpenCodeBaseURL, workspace.OpenCodeModel = "http://model-proxy.models.svc.cluster.local", "model-proxy/auto"
	if err := validateWorkspace(workspace); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkspaceRejectsNonDNSID(t *testing.T) {
	workspace := validTestWorkspace()
	workspace.ID = "Alice_Main"
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected non-DNS workspace id to be rejected")
	}
}

func TestValidateWorkspaceModelMTLSRequiresHTTPSAndExecutorImage(t *testing.T) {
	workspace := validTestWorkspace()
	workspace.ModelSecret, workspace.ModelSecretKey, workspace.ModelEnvironment = "", "", ""
	workspace.OpenCodeModel = "model-proxy/auto"
	workspace.OpenCodeBaseURL = "http://model-gateway.models.svc.cluster.local"
	workspace.ExecutorImage = "registry.example/agent-mtls:1"
	workspace.ModelMTLS = true
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected modelMTLS to reject an HTTP gateway")
	}
	workspace.OpenCodeBaseURL = "https://model-gateway.models.svc.cluster.local"
	workspace.ExecutorImage = ""
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected modelMTLS to require an executor image")
	}
	workspace.ExecutorImage = "registry.example/agent-mtls:1"
	if err := validateWorkspace(workspace); err != nil {
		t.Fatalf("valid modelMTLS workspace rejected: %v", err)
	}
}

func TestValidateWorkspaceRejectsCredentialedOrAmbiguousURLs(t *testing.T) {
	for _, value := range []string{
		"https://user:secret@gateway.example",
		"https://gateway.example/path?token=secret",
		"https://gateway.example/path#fragment",
		"https://",
	} {
		workspace := validTestWorkspace()
		workspace.ModelProxyURL = value
		if err := validateWorkspace(workspace); err == nil {
			t.Errorf("expected modelProxyUrl %q to be rejected", value)
		}
	}
}

func FuzzValidHTTPURL(f *testing.F) {
	f.Add("https://gateway.example/v1")
	f.Add("https://user:secret@gateway.example")
	f.Add("not-a-url")
	f.Fuzz(func(t *testing.T, value string) {
		_ = validHTTPURL(value)
	})
}

func validTestWorkspace() Workspace {
	return Workspace{ID: "alice", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "work/project", ModelSecret: "model", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY", Registry: "platform", RequiredSkills: []string{"platform-baseline"}}
}
