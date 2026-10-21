package kube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	agentGVR = schema.GroupVersionResource{Group: "kubeopencode.io", Version: "v1alpha1", Resource: "agents"}
	taskGVR  = schema.GroupVersionResource{Group: "kubeopencode.io", Version: "v1alpha1", Resource: "tasks"}
)

const managedBy = "jupyter-kubeopencode-mediator"

type Kubernetes struct {
	cfg     config.Config
	core    kubernetes.Interface
	dynamic dynamic.Interface
}

func NewClient(cfg config.Config) (*Kubernetes, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster configuration: %w", err)
	}
	core, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	return &Kubernetes{cfg: cfg, core: core, dynamic: dyn}, nil
}

func (k *Kubernetes) CreateRecord(ctx context.Context, task model.Task) error {
	workspace := k.cfg.Workspaces[task.WorkspaceID]
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = k.core.CoreV1().ConfigMaps(workspace.Namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: recordName(task.ID), Labels: baseLabels(task)},
		Data:       map[string]string{"task.json": string(data)},
	}, metav1.CreateOptions{})
	return err
}

func (k *Kubernetes) GetRecord(ctx context.Context, workspace config.Workspace, id string) (model.Task, error) {
	cm, err := k.core.CoreV1().ConfigMaps(workspace.Namespace).Get(ctx, recordName(id), metav1.GetOptions{})
	if err != nil {
		return model.Task{}, err
	}
	var task model.Task
	err = json.Unmarshal([]byte(cm.Data["task.json"]), &task)
	return task, err
}

func (k *Kubernetes) UpdateRecord(ctx context.Context, workspace config.Workspace, task model.Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	cm, err := k.core.CoreV1().ConfigMaps(workspace.Namespace).Get(ctx, recordName(task.ID), metav1.GetOptions{})
	if err != nil {
		return err
	}
	cm.Data["task.json"] = string(data)
	cm.Labels["mediator.kubeopencode.io/phase"] = strings.ToLower(string(task.Phase))
	_, err = k.core.CoreV1().ConfigMaps(workspace.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (k *Kubernetes) ListActiveRecords(ctx context.Context) ([]model.Task, error) {
	var result []model.Task
	seenNamespaces := map[string]struct{}{}
	for _, workspace := range k.cfg.Workspaces {
		if _, seen := seenNamespaces[workspace.Namespace]; seen {
			continue
		}
		seenNamespaces[workspace.Namespace] = struct{}{}
		cms, err := k.core.CoreV1().ConfigMaps(workspace.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/managed-by=" + managedBy})
		if err != nil {
			return nil, err
		}
		for _, cm := range cms.Items {
			var task model.Task
			if json.Unmarshal([]byte(cm.Data["task.json"]), &task) == nil && !task.Terminal() {
				result = append(result, task)
			}
		}
	}
	return result, nil
}

func (k *Kubernetes) AcquireWorkspace(ctx context.Context, workspace config.Workspace, taskID string) (bool, error) {
	leases := k.core.CoordinationV1().Leases(workspace.Namespace)
	duration := int32((k.cfg.TaskTimeout + 10*time.Minute).Seconds())
	now := metav1.NowMicro()
	lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: lockName(workspace.ID), Labels: map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/owner": ownerLabel(workspace.Owner), "mediator.kubeopencode.io/workspace-id": safeLabel(workspace.ID)}}, Spec: coordinationv1.LeaseSpec{HolderIdentity: &taskID, LeaseDurationSeconds: &duration, AcquireTime: &now, RenewTime: &now}}
	_, err := leases.Create(ctx, lease, metav1.CreateOptions{})
	if err == nil {
		return true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	existing, err := leases.Get(ctx, lease.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == taskID {
		return true, nil
	}
	if existing.Spec.RenewTime != nil && existing.Spec.LeaseDurationSeconds != nil && existing.Spec.RenewTime.Add(time.Duration(*existing.Spec.LeaseDurationSeconds)*time.Second).Before(time.Now()) {
		existing.Spec = lease.Spec
		_, err = leases.Update(ctx, existing, metav1.UpdateOptions{})
		return err == nil, err
	}
	return false, nil
}

func (k *Kubernetes) ReleaseWorkspace(ctx context.Context, workspace config.Workspace, taskID string) error {
	leases := k.core.CoordinationV1().Leases(workspace.Namespace)
	lease, err := leases.Get(ctx, lockName(workspace.ID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != taskID {
		return nil
	}
	err = leases.Delete(ctx, lease.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &lease.UID}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *Kubernetes) RenewWorkspace(ctx context.Context, workspace config.Workspace, taskID string) error {
	leases := k.core.CoordinationV1().Leases(workspace.Namespace)
	lease, err := leases.Get(ctx, lockName(workspace.ID), metav1.GetOptions{})
	if err != nil {
		return err
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != taskID {
		return errors.New("workspace lease ownership changed")
	}
	now := metav1.NowMicro()
	lease.Spec.RenewTime = &now
	_, err = leases.Update(ctx, lease, metav1.UpdateOptions{})
	return err
}

func (k *Kubernetes) CreateSnapshotJob(ctx context.Context, workspace config.Workspace, task model.Task, restore bool) error {
	name := snapshotJobName(task.ID, restore)
	command := fmt.Sprintf("set -eu; mkdir -p /snapshots/%s; tar -C /workspace -cf /snapshots/%s/workspace.tar .", task.ID, task.ID)
	if restore {
		command = fmt.Sprintf("set -eu; test -f /snapshots/%s/workspace.tar; find /workspace -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; tar -C /workspace -xf /snapshots/%s/workspace.tar", task.ID, task.ID)
	}
	backoff := int32(0)
	ttl := int32(3600)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: baseLabels(task)},
		Spec: batchv1.JobSpec{BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl, Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "kubeopencode-snapshot", "mediator.kubeopencode.io/task-id": task.ID}},
			Spec: corev1.PodSpec{AutomountServiceAccountToken: boolPtr(false), RestartPolicy: corev1.RestartPolicyNever,
				SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true), RunAsUser: int64Ptr(effectiveID(workspace.RunAsUser)), RunAsGroup: int64Ptr(effectiveID(workspace.RunAsGroup)), FSGroup: int64Ptr(effectiveID(workspace.FSGroup)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
				Containers:      []corev1.Container{{Name: "snapshot", Image: k.cfg.SnapshotImage, Command: []string{"/bin/sh", "-c", command}, SecurityContext: restrictedSecurityContext(), Resources: resourceRequirements(k.cfg), VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace", SubPath: workspace.SubPath}, {Name: "snapshots", MountPath: "/snapshots"}}}},
				Volumes:         []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.PVC}}}, {Name: "snapshots", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.SnapshotPVC}}}},
			},
		}},
	}
	_, err := k.core.BatchV1().Jobs(workspace.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *Kubernetes) SnapshotJobPhase(ctx context.Context, workspace config.Workspace, task model.Task, restore bool) (JobPhase, string, error) {
	job, err := k.core.BatchV1().Jobs(workspace.Namespace).Get(ctx, snapshotJobName(task.ID, restore), metav1.GetOptions{})
	if err != nil {
		return JobPending, "", err
	}
	if job.Status.Succeeded > 0 {
		return JobSucceeded, "", nil
	}
	if job.Status.Failed > 0 {
		return JobFailed, k.jobLogs(ctx, workspace.Namespace, job.Name), nil
	}
	return JobPending, "", nil
}

func (k *Kubernetes) CreateChangesJob(ctx context.Context, workspace config.Workspace, task model.Task) error {
	name := "changes-" + task.ID
	command := fmt.Sprintf("set -eu; mkdir -p /baseline; tar -C /baseline -xf /snapshots/%s/workspace.tar; diff -qr /baseline /workspace || true", task.ID)
	backoff, ttl := int32(0), int32(3600)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: baseLabels(task)}, Spec: batchv1.JobSpec{BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl, Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": "kubeopencode-change-scan", "mediator.kubeopencode.io/task-id": task.ID}},
		Spec: corev1.PodSpec{AutomountServiceAccountToken: boolPtr(false), RestartPolicy: corev1.RestartPolicyNever, SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true), RunAsUser: int64Ptr(effectiveID(workspace.RunAsUser)), RunAsGroup: int64Ptr(effectiveID(workspace.RunAsGroup)), FSGroup: int64Ptr(effectiveID(workspace.FSGroup)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
			Containers: []corev1.Container{{Name: "changes", Image: k.cfg.SnapshotImage, Command: []string{"/bin/sh", "-c", command}, SecurityContext: restrictedSecurityContext(), Resources: resourceRequirements(k.cfg), VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace", SubPath: workspace.SubPath, ReadOnly: true}, {Name: "snapshots", MountPath: "/snapshots", ReadOnly: true}, {Name: "baseline", MountPath: "/baseline"}}}},
			Volumes:    []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.PVC, ReadOnly: true}}}, {Name: "snapshots", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: workspace.SnapshotPVC, ReadOnly: true}}}, {Name: "baseline", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantityPtr("4Gi")}}}},
		},
	}}}
	_, err := k.core.BatchV1().Jobs(workspace.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *Kubernetes) ChangesJobPhase(ctx context.Context, workspace config.Workspace, task model.Task) (JobPhase, []string, string, error) {
	name := "changes-" + task.ID
	job, err := k.core.BatchV1().Jobs(workspace.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return JobPending, nil, "", err
	}
	if job.Status.Failed > 0 {
		return JobFailed, nil, k.jobLogs(ctx, workspace.Namespace, name), nil
	}
	if job.Status.Succeeded == 0 {
		return JobPending, nil, "", nil
	}
	logs := k.jobLogs(ctx, workspace.Namespace, name)
	files := make([]string, 0)
	for _, line := range strings.Split(logs, "\n") {
		file := changedPath(line)
		if file == "" {
			continue
		}
		if len(file) > 512 {
			file = file[:512]
		}
		if len(files) < 500 {
			files = append(files, file)
		}
	}
	return JobSucceeded, files, "", nil
}

func (k *Kubernetes) EnsureAgent(ctx context.Context, workspace config.Workspace, task model.Task) error {
	agentID := firstAgentID(task.AgentID)
	agentName := agentName(workspace.ID, agentID)
	labels := baseLabels(task)
	labels["mediator.kubeopencode.io/workspace-id"] = workspace.ID
	podLabels := map[string]any{"app.kubernetes.io/name": "kubeopencode-workspace-agent", "mediator.kubeopencode.io/owner": ownerLabel(workspace.Owner), "mediator.kubeopencode.io/workspace-id": safeLabel(workspace.ID)}
	security := map[string]any{"runAsNonRoot": true, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}
	podSpec := map[string]any{
		"labels":             podLabels,
		"resources":          map[string]any{"requests": map[string]any{"cpu": k.cfg.AgentCPURequest, "memory": k.cfg.AgentMemoryRequest}, "limits": map[string]any{"cpu": k.cfg.AgentCPULimit, "memory": k.cfg.AgentMemoryLimit, "ephemeral-storage": k.cfg.EphemeralLimit}},
		"securityContext":    security,
		"podSecurityContext": map[string]any{"runAsNonRoot": true, "runAsUser": effectiveID(workspace.RunAsUser), "runAsGroup": effectiveID(workspace.RunAsGroup), "fsGroup": effectiveID(workspace.FSGroup), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"extraVolumes": []any{
			map[string]any{"name": "user-workspace", "persistentVolumeClaim": map[string]any{"claimName": workspace.PVC}},
			map[string]any{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "1Gi"}},
			map[string]any{"name": "home", "emptyDir": map[string]any{"sizeLimit": "1Gi"}},
		},
		"extraVolumeMounts": []any{
			map[string]any{"name": "user-workspace", "mountPath": "/workspace/project", "subPath": workspace.SubPath},
			map[string]any{"name": "tmp", "mountPath": "/tmp"},
			map[string]any{"name": "home", "mountPath": "/home/agent"},
		},
	}
	if workspace.RuntimeClassName != "" {
		podSpec["runtimeClassName"] = workspace.RuntimeClassName
	}
	spec := map[string]any{
		"profile":            "Jupyter workspace agent for " + workspace.ID,
		"workspaceDir":       "/workspace",
		"serviceAccountName": "default",
		"maxConcurrentTasks": int64(1),
		"podSpec":            podSpec,
	}
	custom, err := k.GetAgentConfig(ctx, workspace, agentID)
	if err != nil {
		return err
	}
	if workspace.ModelSecret != "" {
		spec["credentials"] = []any{map[string]any{"name": "model-api-key", "secretRef": map[string]any{"name": workspace.ModelSecret, "key": workspace.ModelSecretKey}, "env": workspace.ModelEnvironment}}
	}
	if workspace.OpenCodeBaseURL != "" {
		modelID := workspace.OpenCodeModel
		if _, value, found := strings.Cut(modelID, "/"); found {
			modelID = value
		}
		spec["config"] = map[string]any{
			"model":             workspace.OpenCodeModel,
			"small_model":       workspace.OpenCodeModel,
			"enabled_providers": []any{"model-proxy"},
			"share":             "disabled",
			"autoupdate":        false,
			"compaction": map[string]any{
				"auto":  true,
				"prune": true,
			},
			"experimental": map[string]any{
				"openTelemetry": false,
			},
			"provider": map[string]any{"model-proxy": map[string]any{
				"npm": "@ai-sdk/openai-compatible", "name": "Internal model proxy",
				"options": map[string]any{"baseURL": workspace.OpenCodeBaseURL},
				"models":  allowedModelObjects(workspace.AllowedModels, modelID),
			}},
		}
	}
	applyAgentCustomization(spec, workspace, custom)
	if workspace.ModelProxyURL != "" {
		spec["proxy"] = map[string]any{"httpProxy": workspace.ModelProxyURL, "httpsProxy": workspace.ModelProxyURL, "noProxy": "localhost,127.0.0.1,.svc,.cluster.local"}
	}
	if workspace.ExecutorImage != "" {
		spec["executorImage"] = workspace.ExecutorImage
	}
	if workspace.AgentImage != "" {
		spec["agentImage"] = workspace.AgentImage
	}
	if workspace.AttachImage != "" {
		spec["attachImage"] = workspace.AttachImage
	}
	agent := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "kubeopencode.io/v1alpha1", "kind": "Agent", "metadata": map[string]any{"name": agentName, "namespace": workspace.Namespace, "labels": stringMapAny(labels), "annotations": map[string]any{"mediator.kubeopencode.io/jupyterhub-username": workspace.Owner}}, "spec": spec}}
	resource := k.dynamic.Resource(agentGVR).Namespace(workspace.Namespace)
	_, err = resource.Create(ctx, agent, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := resource.Get(ctx, agentName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if !resourceOwnedBy(existing.GetLabels(), existing.GetAnnotations(), workspace.Owner) {
			return errors.New("agent ownership mismatch")
		}
		existing.SetLabels(labels)
		existing.SetAnnotations(agent.GetAnnotations())
		desiredSpec, _, _ := unstructured.NestedMap(agent.Object, "spec")
		if err := unstructured.SetNestedMap(existing.Object, desiredSpec, "spec"); err != nil {
			return err
		}
		_, err = resource.Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	return err
}

func (k *Kubernetes) AgentRolloutReady(ctx context.Context, workspace config.Workspace, agentID string) (bool, error) {
	configured, err := k.GetAgentConfig(ctx, workspace, agentID)
	if err != nil {
		return false, err
	}
	deployment, err := k.core.AppsV1().Deployments(workspace.Namespace).Get(ctx, agentName(workspace.ID, agentID)+"-server", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	wanted := int32(1)
	if deployment.Spec.Replicas != nil {
		wanted = *deployment.Spec.Replicas
	}
	if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.UpdatedReplicas != wanted || deployment.Status.AvailableReplicas != wanted || deployment.Status.UnavailableReplicas != 0 {
		return false, nil
	}
	actual := map[string]string{}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, item := range container.Env {
			actual[item.Name] = item.Value
		}
	}
	for name, value := range configured.Environment {
		if actual[name] != value {
			return false, nil
		}
	}
	return deployment.Spec.Template.Annotations["mediator.kubeopencode.io/environment-hash"] == environmentHash(configured.Environment), nil
}

func (k *Kubernetes) ListAgentConfigs(ctx context.Context, workspace config.Workspace) ([]model.AgentConfig, error) {
	items, err := k.core.CoreV1().ConfigMaps(workspace.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/managed-by=" + managedBy + ",mediator.kubeopencode.io/workspace-id=" + safeLabel(workspace.ID) + ",mediator.kubeopencode.io/config-kind=agent"})
	if err != nil {
		return nil, err
	}
	result := make([]model.AgentConfig, 0, len(items.Items))
	seen := map[string]bool{}
	for _, cm := range items.Items {
		var value model.AgentConfig
		if json.Unmarshal([]byte(cm.Data["config.json"]), &value) == nil {
			value.ID = firstAgentID(value.ID)
			value.ResourceName = agentName(workspace.ID, value.ID)
			result = append(result, value)
			seen[value.ID] = true
		}
	}
	if !seen["default"] {
		value, err := k.GetAgentConfig(ctx, workspace, "default")
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (k *Kubernetes) GetAgentConfig(ctx context.Context, workspace config.Workspace, agentID string) (model.AgentConfig, error) {
	agentID = firstAgentID(agentID)
	cm, err := k.core.CoreV1().ConfigMaps(workspace.Namespace).Get(ctx, agentConfigName(workspace.ID, agentID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return model.AgentConfig{ID: agentID, ResourceName: agentName(workspace.ID, agentID)}, nil
	}
	if err != nil {
		return model.AgentConfig{}, err
	}
	var result model.AgentConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &result); err != nil {
		return model.AgentConfig{}, fmt.Errorf("decode agent configuration: %w", err)
	}
	return result, nil
}

func (k *Kubernetes) SetAgentConfig(ctx context.Context, workspace config.Workspace, agentID string, value model.AgentConfig) error {
	agentID = firstAgentID(agentID)
	value.ID = agentID
	value.ResourceName = agentName(workspace.ID, agentID)
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	configMaps := k.core.CoreV1().ConfigMaps(workspace.Namespace)
	name := agentConfigName(workspace.ID, agentID)
	cm, err := configMaps.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = configMaps.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/owner": ownerLabel(workspace.Owner), "mediator.kubeopencode.io/workspace-id": safeLabel(workspace.ID), "mediator.kubeopencode.io/config-kind": "agent", "mediator.kubeopencode.io/agent-id": safeLabel(agentID)}, Annotations: map[string]string{"mediator.kubeopencode.io/jupyterhub-username": workspace.Owner}}, Data: map[string]string{"config.json": string(data)}}, metav1.CreateOptions{})
	} else if err == nil {
		if !resourceOwnedBy(cm.Labels, cm.Annotations, workspace.Owner) {
			return errors.New("agent configuration ownership mismatch")
		}
		cm.Data = map[string]string{"config.json": string(data)}
		cm.Labels = map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/owner": ownerLabel(workspace.Owner), "mediator.kubeopencode.io/workspace-id": safeLabel(workspace.ID), "mediator.kubeopencode.io/config-kind": "agent", "mediator.kubeopencode.io/agent-id": safeLabel(agentID)}
		cm.Annotations = map[string]string{"mediator.kubeopencode.io/jupyterhub-username": workspace.Owner}
		_, err = configMaps.Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return err
	}

	resource := k.dynamic.Resource(agentGVR).Namespace(workspace.Namespace)
	agent, err := resource.Get(ctx, agentName(workspace.ID, agentID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !resourceOwnedBy(agent.GetLabels(), agent.GetAnnotations(), workspace.Owner) {
		return errors.New("agent ownership mismatch")
	}
	agentLabels := agent.GetLabels()
	agentLabels["mediator.kubeopencode.io/owner"] = ownerLabel(workspace.Owner)
	agent.SetLabels(agentLabels)
	spec, _, _ := unstructured.NestedMap(agent.Object, "spec")
	applyAgentCustomization(spec, workspace, value)
	if err := unstructured.SetNestedMap(agent.Object, spec, "spec"); err != nil {
		return err
	}
	_, err = resource.Update(ctx, agent, metav1.UpdateOptions{})
	return err
}

func (k *Kubernetes) DeleteAgent(ctx context.Context, workspace config.Workspace, agentID string) error {
	agentID = firstAgentID(agentID)
	resource := k.dynamic.Resource(agentGVR).Namespace(workspace.Namespace)
	name := agentName(workspace.ID, agentID)
	if existing, err := resource.Get(ctx, name, metav1.GetOptions{}); err == nil {
		if !resourceOwnedBy(existing.GetLabels(), existing.GetAnnotations(), workspace.Owner) {
			return errors.New("agent ownership mismatch")
		}
		if err := resource.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	err := k.core.CoreV1().ConfigMaps(workspace.Namespace).Delete(ctx, agentConfigName(workspace.ID, agentID), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// DeleteWorkspaceResources removes only mediator-owned resources for one
// configured workspace. PVCs and Secrets are provisioned externally and are
// deliberately not part of this lifecycle.
func (k *Kubernetes) DeleteWorkspaceResources(ctx context.Context, workspace config.Workspace) error {
	selector := strings.Join([]string{
		"app.kubernetes.io/managed-by=" + managedBy,
		"mediator.kubeopencode.io/owner=" + ownerLabel(workspace.Owner),
		"mediator.kubeopencode.io/workspace-id=" + safeLabel(workspace.ID),
	}, ",")
	propagation := metav1.DeletePropagationBackground
	options := metav1.DeleteOptions{PropagationPolicy: &propagation}
	listOptions := metav1.ListOptions{LabelSelector: selector}
	operations := []func() error{
		func() error {
			return k.dynamic.Resource(taskGVR).Namespace(workspace.Namespace).DeleteCollection(ctx, options, listOptions)
		},
		func() error {
			return k.dynamic.Resource(agentGVR).Namespace(workspace.Namespace).DeleteCollection(ctx, options, listOptions)
		},
		func() error {
			return k.core.BatchV1().Jobs(workspace.Namespace).DeleteCollection(ctx, options, listOptions)
		},
		func() error {
			return k.core.CoreV1().ConfigMaps(workspace.Namespace).DeleteCollection(ctx, options, listOptions)
		},
		func() error {
			return k.core.CoordinationV1().Leases(workspace.Namespace).Delete(ctx, lockName(workspace.ID), options)
		},
	}
	for _, operation := range operations {
		if err := operation(); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (k *Kubernetes) CreateKubeTask(ctx context.Context, workspace config.Workspace, task model.Task) error {
	description := "The authorized project root is /workspace/project. Work only inside that directory. Treat it as the project root and do not modify /workspace outside project.\n\nUser request:\n" + task.Description
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubeopencode.io/v1alpha1", "kind": "Task",
		"metadata": map[string]any{"name": task.KubeTaskName, "namespace": workspace.Namespace, "labels": stringMapAny(baseLabels(task))},
		"spec":     map[string]any{"agentRef": map[string]any{"name": task.AgentName}, "description": description, "timeout": k.cfg.TaskTimeout.String()},
	}}
	_, err := k.dynamic.Resource(taskGVR).Namespace(workspace.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (k *Kubernetes) KubeTaskPhase(ctx context.Context, workspace config.Workspace, task model.Task) (model.Phase, string, []string, error) {
	obj, err := k.dynamic.Resource(taskGVR).Namespace(workspace.Namespace).Get(ctx, task.KubeTaskName, metav1.GetOptions{})
	if err != nil {
		return model.PhaseQueued, "", nil, err
	}
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	message := taskConditionMessage(obj)
	switch strings.ToLower(phase) {
	case "running":
		return model.PhaseRunning, message, nil, nil
	case "completed", "succeeded":
		if conditionReason(obj, "Failed", "Error") {
			return model.PhaseFailed, message, nil, nil
		}
		return model.PhaseSucceeded, message, nil, nil
	case "failed", "error":
		return model.PhaseFailed, message, nil, nil
	default:
		return model.PhaseQueued, message, nil, nil
	}
}

func (k *Kubernetes) TaskOutput(ctx context.Context, workspace config.Workspace, task model.Task) (string, error) {
	pods, err := k.core.CoreV1().Pods(workspace.Namespace).List(ctx, metav1.ListOptions{LabelSelector: fields.OneTermEqualSelector("kubeopencode.io/task", task.KubeTaskName).String()})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", apierrors.NewNotFound(corev1.Resource("pods"), task.KubeTaskName)
	}
	limit := int64(1024 * 1024)
	stream, err := k.core.CoreV1().Pods(workspace.Namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{Container: "agent", LimitBytes: &limit}).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, limit))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (k *Kubernetes) DeleteKubeTask(ctx context.Context, workspace config.Workspace, task model.Task) error {
	err := k.dynamic.Resource(taskGVR).Namespace(workspace.Namespace).Delete(ctx, task.KubeTaskName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *Kubernetes) jobLogs(ctx context.Context, namespace, jobName string) string {
	pods, err := k.core.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: fields.OneTermEqualSelector("job-name", jobName).String()})
	if err != nil || len(pods.Items) == 0 {
		return "snapshot job failed"
	}
	stream, err := k.core.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return "snapshot job failed"
	}
	defer stream.Close()
	data, _ := io.ReadAll(io.LimitReader(stream, 4096))
	return string(data)
}

func baseLabels(task model.Task) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": managedBy, "mediator.kubeopencode.io/task-id": task.ID, "mediator.kubeopencode.io/owner": ownerLabel(task.Owner), "mediator.kubeopencode.io/workspace-id": safeLabel(task.WorkspaceID), "mediator.kubeopencode.io/phase": strings.ToLower(string(task.Phase))}
}
func recordName(id string) string { return "mediator-task-" + id }
func lockName(id string) string   { return "workspace-" + safeName(id) }
func agentName(workspaceID, agentID string) string {
	if firstAgentID(agentID) == "default" {
		return "vscode-" + safeName(workspaceID)
	}
	return "vscode-" + safeName(workspaceID) + "-" + safeName(agentID)
}
func agentConfigName(workspaceID, agentID string) string {
	if firstAgentID(agentID) == "default" {
		return "mediator-agent-config-" + safeName(workspaceID)
	}
	return "mediator-agent-config-" + safeName(workspaceID) + "-" + safeName(agentID)
}
func firstAgentID(value string) string {
	if value == "" {
		return "default"
	}
	return value
}

func applyAgentCustomization(spec map[string]any, workspace config.Workspace, value model.AgentConfig) {
	profile := strings.TrimSpace(value.Profile)
	if profile == "" {
		profile = "Jupyter workspace agent for " + workspace.ID
	}
	spec["profile"] = profile
	if instructions := strings.TrimSpace(value.Instructions); instructions != "" {
		spec["contexts"] = []any{map[string]any{
			"name": "user-instructions", "description": "User-managed persistent agent instructions", "type": "Text", "text": instructions,
		}}
	} else {
		delete(spec, "contexts")
	}
	if len(value.SkillSources) == 0 {
		delete(spec, "skills")
	} else {
		skills := make([]any, 0, len(value.SkillSources))
		for _, source := range value.SkillSources {
			git := map[string]any{"repository": source.Repository, "depth": int64(1)}
			if source.Ref != "" {
				git["ref"] = source.Ref
			}
			if source.Path != "" {
				git["path"] = source.Path
			}
			if len(source.Names) > 0 {
				names := make([]any, len(source.Names))
				for i, name := range source.Names {
					names[i] = name
				}
				git["names"] = names
			}
			skills = append(skills, map[string]any{"name": source.Name, "git": git})
		}
		spec["skills"] = skills
	}
	if podSpec, ok := spec["podSpec"].(map[string]any); ok {
		podSpec["annotations"] = map[string]any{"mediator.kubeopencode.io/environment-hash": environmentHash(value.Environment)}
		environmentValues := make(map[string]string, len(value.Environment)+3)
		for name, item := range value.Environment {
			environmentValues[name] = item
		}
		// Platform-owned values reduce startup work and cannot be re-enabled by
		// a user-provided launch variable.
		environmentValues["DO_NOT_TRACK"] = "1"
		environmentValues["OPENCODE_DISABLE_AUTOUPDATE"] = "1"
		environmentValues["OPENCODE_DISABLE_MODELS_FETCH"] = "1"
		names := make([]string, 0, len(environmentValues))
		for name := range environmentValues {
			names = append(names, name)
		}
		sort.Strings(names)
		environment := make([]any, 0, len(names))
		for _, name := range names {
			environment = append(environment, map[string]any{"name": name, "value": environmentValues[name]})
		}
		podSpec["extraEnv"] = environment
	}
	selectedModel := value.Model
	if !contains(workspace.AllowedModels, selectedModel) {
		selectedModel = workspace.OpenCodeModel
	}
	if selectedModel != "" {
		if openCodeConfig, ok := spec["config"].(map[string]any); ok {
			openCodeConfig["model"] = selectedModel
			openCodeConfig["small_model"] = selectedModel
		}
	}
}

func allowedModelObjects(models []string, fallback string) map[string]any {
	if len(models) == 0 {
		models = []string{fallback}
	}
	result := make(map[string]any, len(models))
	for _, fullName := range models {
		modelID := fullName
		if _, value, found := strings.Cut(modelID, "/"); found {
			modelID = value
		}
		result[modelID] = map[string]any{"name": modelID}
	}
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func snapshotJobName(id string, restore bool) string {
	if restore {
		return "restore-" + id
	}
	return "snapshot-" + id
}
func safeName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
func safeLabel(value string) string {
	value = safeName(value)
	if len(value) > 63 {
		value = strings.Trim(value[:63], "-")
	}
	return value
}
func ownerLabel(value string) string {
	hash := sha256.Sum256([]byte(value))
	suffix := hex.EncodeToString(hash[:6])
	value = safeName(value)
	if value == "" {
		return "user-" + suffix
	}
	if len(value) > 50 {
		value = strings.Trim(value[:50], "-")
	}
	return value + "-" + suffix
}
func resourceOwnedBy(labels, annotations map[string]string, owner string) bool {
	label := labels["mediator.kubeopencode.io/owner"]
	if label == ownerLabel(owner) {
		return true
	}
	return label == safeName(owner) && annotations["mediator.kubeopencode.io/jupyterhub-username"] == owner
}
func environmentHash(environment map[string]string) string {
	data, _ := json.Marshal(environment)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:12])
}
func boolPtr(v bool) *bool    { return &v }
func int64Ptr(v int64) *int64 { return &v }
func effectiveID(v int64) int64 {
	if v == 0 {
		return 1000
	}
	return v
}
func quantityPtr(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}
func restrictedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{AllowPrivilegeEscalation: boolPtr(false), ReadOnlyRootFilesystem: boolPtr(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
}
func resourceRequirements(cfg config.Config) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi"), corev1.ResourceEphemeralStorage: resource.MustParse(cfg.EphemeralLimit)},
	}
}
func stringMapAny(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func taskConditionMessage(obj *unstructured.Unstructured) string {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for i := len(conditions) - 1; i >= 0; i-- {
		if c, ok := conditions[i].(map[string]any); ok {
			if message, ok := c["message"].(string); ok && message != "" {
				return message
			}
		}
	}
	return ""
}
func conditionReason(obj *unstructured.Unstructured, reasons ...string) bool {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, item := range conditions {
		c, ok := item.(map[string]any)
		if !ok {
			continue
		}
		reason, _ := c["reason"].(string)
		for _, wanted := range reasons {
			if strings.EqualFold(reason, wanted) {
				return true
			}
		}
	}
	return false
}

func changedPath(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "Files ") && strings.HasSuffix(line, " differ") {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "Files "), " differ"), " and ")
		if len(parts) == 2 {
			return strings.TrimPrefix(strings.TrimPrefix(parts[1], "/workspace/"), "/baseline/")
		}
	}
	if strings.HasPrefix(line, "Only in ") {
		parts := strings.SplitN(strings.TrimPrefix(line, "Only in "), ": ", 2)
		if len(parts) == 2 {
			directory := strings.TrimPrefix(strings.TrimPrefix(parts[0], "/workspace"), "/baseline")
			return strings.TrimPrefix(path.Join(directory, parts[1]), "/")
		}
	}
	return ""
}
