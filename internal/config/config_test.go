package config

import "testing"

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

func validTestWorkspace() Workspace {
	return Workspace{ID: "alice", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "work/project", ModelSecret: "model", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY", Registry: "platform", RequiredSkills: []string{"platform-baseline"}}
}
