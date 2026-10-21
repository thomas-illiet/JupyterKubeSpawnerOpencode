package config

import "testing"

func TestValidateWorkspaceRejectsTraversal(t *testing.T) {
	workspace := Workspace{ID: "alice", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "../bob", ModelSecret: "model", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY"}
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestValidateWorkspaceAcceptsRelativeSubPath(t *testing.T) {
	workspace := Workspace{ID: "alice", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "work/project", ModelSecret: "model", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY"}
	if err := validateWorkspace(workspace); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkspaceAcceptsInternalModelProxy(t *testing.T) {
	workspace := Workspace{ID: "alice", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "work", OpenCodeBaseURL: "http://model-proxy.models.svc.cluster.local", OpenCodeModel: "model-proxy/auto"}
	if err := validateWorkspace(workspace); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkspaceRejectsNonDNSID(t *testing.T) {
	workspace := Workspace{ID: "Alice_Main", Owner: "alice", Namespace: "users", PVC: "claim", SnapshotPVC: "snapshots", SubPath: "work", ModelSecret: "model", ModelSecretKey: "key", ModelEnvironment: "OPENAI_API_KEY"}
	if err := validateWorkspace(workspace); err == nil {
		t.Fatal("expected non-DNS workspace id to be rejected")
	}
}
