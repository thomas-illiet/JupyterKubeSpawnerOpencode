# Cluster acceptance

Static checks do not prove tenant isolation. Execute this checklist against the
target cluster with two real OIDC identities and retain evidence with the
release. A production approval requires every mandatory item to pass.

## Identity and ownership

- [ ] Alice can list, create, update and delete only Agents for `alice-main`.
- [ ] Alice requesting `bob-main` receives HTTP 404 and creates no resource.
- [ ] Alice cannot read Bob's Agent configuration, logs, PVC, snapshots or
  Secrets.
- [ ] Agent and ConfigMap resources carry the expected owner/workspace labels.
- [ ] The mediator rejects an unknown workspace, invalid Agent ID, disallowed
  model, credential-bearing Git URL and unknown JSON field.

## Interactive terminal

- [ ] `opencloud` presents only Alice's Agents.
- [ ] `opencloud reviewer` starts the OpenCloud TUI through the mediator relay.
- [ ] Two Agents attach simultaneously using distinct ephemeral local ports.
- [ ] No automatic VS Code port-forward is offered for the OpenCode server.
- [ ] `KUBEOPENCODE_ENV_BUILD_TARGET=test opencloud reviewer` rolls the Agent,
  waits for the new pod, and exposes `BUILD_TARGET=test` remotely.
- [ ] A later launch without the prefix removes `BUILD_TARGET`.
- [ ] `KUBEOPENCODE_ENV_GITHUB_TOKEN=value` is rejected before attachment.
- [ ] SSE/streaming remains stable through the JupyterHub subpath.

## Kubernetes credentials and RBAC

- [ ] The Jupyter/code-server pod has `automountServiceAccountToken: false`.
- [ ] It has no projected Kubernetes token or kubeconfig.
- [ ] Its JupyterHub token cannot be used directly against Kubernetes or the
  KubeOpenCode server and is removed before mediator proxying.
- [ ] Alice and Bob can share a namespace without listing or attaching to one
  another's Agents.
- [ ] Agent and Task pods contain no Kubernetes token.

## Runtime containment

- [ ] The Agent runs non-root with all capabilities dropped,
  `allowPrivilegeEscalation=false`, RuntimeDefault seccomp and read-only root.
- [ ] `/tmp` and home are bounded volumes; CPU, memory and ephemeral limits are
  enforced under load.
- [ ] The Agent cannot mount host paths, runtime sockets, another PVC, snapshot
  PVC, JupyterHub token, kubeconfig, or general platform Secrets.
- [ ] The Agent cannot contact the Kubernetes API, another user namespace, or
  an unapproved Internet destination.
- [ ] Model traffic traverses the approved gateway and validates TLS.

## Storage and concurrency

- [ ] The Agent sees only its configured workspace subpath.
- [ ] Concurrent VS Code/OpenCode edits are covered by the operating procedure;
  direct interactive sessions are explicitly acknowledged as non-transactional.
- [ ] For RWO, the CSI driver and scheduling policy have proven simultaneous
  code-server/Agent/Job mounts. Otherwise RWX is used.

## Optional Task API

- [ ] A Task acquires one workspace Lease and a second Task receives HTTP 409.
- [ ] The snapshot is written to a PVC that is not mounted into the Agent.
- [ ] Cancel/failure releases the Lease.
- [ ] Restore reproduces the exact pre-task workspace tree.
- [ ] A mediator restart during Snapshotting and Running resumes safely.
- [ ] Deleting Alice in JupyterHub removes only Alice's mediator-owned Agents,
  Tasks, ConfigMaps, Jobs and Lease; Bob's resources remain untouched.
- [ ] A failed cleanup prevents JupyterHub user deletion and can be retried.
- [ ] User PVCs, snapshot PVCs and model Secrets remain present after cleanup.

## Operations

- [ ] Liveness/readiness probes, alerting, quotas, audit logs, Secret encryption,
  image policies and Pod Security Admission are enabled.
- [ ] Upgrade and rollback were exercised with the exact release artifacts.
- [ ] Workspace backup restoration was tested independently of Task snapshots.
