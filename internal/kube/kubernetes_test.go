package kube

import (
	"context"
	"testing"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDeleteWorkspaceResourcesUsesExactManagedLabels(t *testing.T) {
	workspace := config.Workspace{ID: "alice-main", Owner: "alice", Namespace: "shared"}
	labels := map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/owner": ownerLabel("alice"), "mediator.kubeopencode.io/workspace-id": "alice-main"}
	bobLabels := map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/owner": ownerLabel("bob"), "mediator.kubeopencode.io/workspace-id": "bob-main"}
	objects := []runtime.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "alice-config", Namespace: "shared", Labels: labels}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bob-config", Namespace: "shared", Labels: bobLabels}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "alice-job", Namespace: "shared", Labels: labels}},
		&coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: lockName(workspace.ID), Namespace: "shared", Labels: labels}},
	}
	core := kubernetesfake.NewSimpleClientset(objects...)
	agent := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubeopencode.io/v1alpha1", "kind": "Agent", "metadata": map[string]any{"name": "alice-agent", "namespace": "shared", "labels": stringMapAny(labels)}}}
	task := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubeopencode.io/v1alpha1", "kind": "Task", "metadata": map[string]any{"name": "alice-task", "namespace": "shared", "labels": stringMapAny(labels)}}}
	dynamic := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{agentGVR: "AgentList", taskGVR: "TaskList"}, agent, task)
	client := &Kubernetes{core: core, dynamic: dynamic}

	if err := client.DeleteWorkspaceResources(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := core.CoreV1().ConfigMaps("shared").Get(context.Background(), "bob-config", metav1.GetOptions{}); err != nil {
		t.Fatalf("Bob ConfigMap was deleted: %v", err)
	}
	wantSelector := "app.kubernetes.io/managed-by=" + managedBy + ",mediator.kubeopencode.io/owner=" + ownerLabel("alice") + ",mediator.kubeopencode.io/workspace-id=alice-main"
	for _, actions := range [][]testingAction{normalizeActions(core.Actions()), normalizeActions(dynamic.Actions())} {
		for _, action := range actions {
			if action.verb == "delete-collection" && action.selector != wantSelector {
				t.Fatalf("unsafe cleanup selector %q, want %q", action.selector, wantSelector)
			}
		}
	}
	if len(dynamic.Actions()) != 2 || len(core.Actions()) != 4 {
		t.Fatalf("unexpected cleanup actions: dynamic=%d core=%d", len(dynamic.Actions()), len(core.Actions()))
	}
}

type testingAction struct{ verb, selector string }

func normalizeActions(actions []k8stesting.Action) []testingAction {
	result := make([]testingAction, 0, len(actions))
	for _, action := range actions {
		selector := ""
		if deleteAction, ok := action.(k8stesting.DeleteCollectionAction); ok {
			selector = deleteAction.GetListRestrictions().Labels.String()
		}
		result = append(result, testingAction{verb: action.GetVerb(), selector: selector})
	}
	return result
}

func TestChangedPath(t *testing.T) {
	tests := map[string]string{
		"Files /baseline/README.md and /workspace/README.md differ": "README.md",
		"Only in /workspace/src: new.go":                            "src/new.go",
		"Only in /baseline: removed.txt":                            "removed.txt",
		"":                                                          "",
	}
	for input, want := range tests {
		if got := changedPath(input); got != want {
			t.Errorf("changedPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetRegistryReadsNativeKubeOpenCodeAssets(t *testing.T) {
	registry := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubeopencode.io/v1alpha1", "kind": "Registry",
		"metadata": map[string]any{"name": "platform", "namespace": "shared"},
		"spec": map[string]any{
			"skills":  []any{map[string]any{"name": "baseline", "git": map[string]any{"repository": "https://example.test/skills.git", "ref": "v1", "names": []any{"secure-defaults"}, "secretRef": map[string]any{"name": "git-readonly"}}, "metadata": map[string]any{"description": "Required baseline", "tags": []any{"security"}}}},
			"plugins": []any{map[string]any{"name": "safety", "plugin": map[string]any{"name": "cc-safety-net@0.8.2", "target": "server"}}},
		},
	}}
	dynamic := fake.NewSimpleDynamicClient(runtime.NewScheme(), registry)
	client := &Kubernetes{dynamic: dynamic}
	got, err := client.GetRegistry(context.Background(), config.Workspace{Namespace: "shared", Registry: "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Source.SecretRef != "git-readonly" || len(got.Plugins) != 1 || got.Plugins[0].Plugin.Target != "server" {
		t.Fatalf("unexpected Registry projection: %#v", got)
	}
}

func TestApplyAgentCustomizationAddsExternalSkills(t *testing.T) {
	spec := map[string]any{"podSpec": map[string]any{}}
	applyAgentCustomization(spec, config.Workspace{ID: "alice-main", OpenCodeModel: "proxy/auto", AllowedModels: []string{"proxy/auto"}}, model.AgentConfig{Profile: "Reviewer", Model: "proxy/auto", SkillSources: []model.SkillSource{{Name: "team-skills", Repository: "https://github.com/example/skills.git", Ref: "main", Path: "skills", Names: []string{"review"}}}, PluginSpecs: []model.PluginSpec{{Name: "cc-safety-net@0.8.2", Target: "server"}}})
	skills, ok := spec["skills"].([]any)
	if !ok || len(skills) != 1 {
		t.Fatalf("unexpected skills: %#v", spec["skills"])
	}
	git := skills[0].(map[string]any)["git"].(map[string]any)
	if git["repository"] != "https://github.com/example/skills.git" {
		t.Fatalf("unexpected git source: %#v", git)
	}
	plugins := spec["plugins"].([]any)
	if plugins[0].(map[string]any)["name"] != "cc-safety-net@0.8.2" || plugins[0].(map[string]any)["target"] != "server" {
		t.Fatalf("unexpected plugins: %#v", plugins)
	}
	if err := unstructured.SetNestedMap(map[string]any{}, spec, "spec"); err != nil {
		t.Fatalf("spec is not valid unstructured JSON: %v", err)
	}
}

func TestApplyAgentCustomizationAddsSortedEnvironment(t *testing.T) {
	spec := map[string]any{"podSpec": map[string]any{}}
	applyAgentCustomization(spec, config.Workspace{}, model.AgentConfig{Environment: map[string]string{"Z_MODE": "strict", "BUILD_TARGET": "test"}})
	got := spec["podSpec"].(map[string]any)["extraEnv"].([]any)
	if got[0].(map[string]any)["name"] != "BUILD_TARGET" || got[1].(map[string]any)["name"] != "DO_NOT_TRACK" || got[2].(map[string]any)["name"] != "OPENCODE_DISABLE_AUTOUPDATE" || got[3].(map[string]any)["name"] != "OPENCODE_DISABLE_MODELS_FETCH" || got[4].(map[string]any)["name"] != "Z_MODE" {
		t.Fatalf("environment is not stable and sorted: %#v", got)
	}
	if got[1].(map[string]any)["value"] != "1" || got[2].(map[string]any)["value"] != "1" || got[3].(map[string]any)["value"] != "1" {
		t.Fatalf("platform privacy environment is not enforced: %#v", got)
	}
	annotations := spec["podSpec"].(map[string]any)["annotations"].(map[string]any)
	if annotations["mediator.kubeopencode.io/environment-hash"] != environmentHash(map[string]string{"BUILD_TARGET": "test", "Z_MODE": "strict"}) {
		t.Fatalf("unexpected environment hash: %#v", annotations)
	}
}

func TestOwnerLabelIsStableAndCollisionResistant(t *testing.T) {
	first := ownerLabel("Alice@example.com")
	if first != ownerLabel("Alice@example.com") || first == ownerLabel("alice-example-com") || len(first) > 63 {
		t.Fatalf("unsafe owner labels: %q", first)
	}
}

func TestResourceOwnedByAcceptsCurrentAndExactLegacyIdentity(t *testing.T) {
	owner := "Alice@example.com"
	if !resourceOwnedBy(map[string]string{"mediator.kubeopencode.io/owner": ownerLabel(owner)}, nil, owner) {
		t.Fatal("current owner label was rejected")
	}
	if !resourceOwnedBy(map[string]string{"mediator.kubeopencode.io/owner": safeName(owner)}, map[string]string{"mediator.kubeopencode.io/jupyterhub-username": owner}, owner) {
		t.Fatal("exact annotated legacy owner label was rejected")
	}
}

func TestResourceOwnedByRejectsAmbiguousLegacyIdentity(t *testing.T) {
	labels := map[string]string{"mediator.kubeopencode.io/owner": safeName("Alice@example.com")}
	if resourceOwnedBy(labels, nil, "Alice@example.com") {
		t.Fatal("legacy owner label without an exact username annotation was accepted")
	}
	if resourceOwnedBy(labels, map[string]string{"mediator.kubeopencode.io/jupyterhub-username": "alice-example-com"}, "Alice@example.com") {
		t.Fatal("colliding legacy identity was accepted")
	}
}
