# Architecture

## Product boundary

Jupyter OpenCloud separates the developer interface from agent execution.
code-server provides the editor and terminal. The extension configures Agents
through a mediator. The `opencloud` terminal command selects one owned Agent and
uses a loopback proxy authenticated with the JupyterHub server token. OpenCode runs in a
separate, restricted Kubernetes pod that mounts only the authorised workspace.

The extension deliberately has no Task execution, log streaming, cancellation,
or restore UI. The mediator retains those APIs for non-interactive automation,
but they are a separate client surface.

![Component architecture](images/architecture.svg)

## Components

| Component | Responsibility | Trust level |
| --- | --- | --- |
| Browser and code-server | User interface and workspace editing | User-controlled |
| VS Code extension | Validate UX input and call the mediator | User-controlled |
| `opencloud` launcher | Select owned Agent and run the authenticated loopback proxy | User-controlled |
| Mediator | Authenticate identity, authorize workspace, validate configuration and write Kubernetes resources | Trusted control plane |
| JupyterHub user API | Resolve bearer token to canonical username | Identity authority |
| KubeOpenCode controller/server | Reconcile Agent CRs and proxy interactive connections | Trusted platform |
| Agent pod | Run OpenCode and commands inside the workspace sandbox | Untrusted workload |
| Model gateway | Hold/provider-route model access | Trusted egress boundary |

## Interactive sequence

1. JupyterHub spawns code-server with its server-specific
   `JUPYTERHUB_API_TOKEN`, the mediator URL, and no Kubernetes credential.
2. The extension submits Agent configuration to the mediator.
3. The mediator authenticates the bearer token against JupyterHub and resolves
   the unique workspace owned by that username from its administrator allowlist.
4. The mediator stores the credential-free Agent configuration in a ConfigMap
   and reconciles the KubeOpenCode Agent resource.
5. In the terminal, `opencloud [agent-id]` lists only configurations returned by
   the owner-authorized mediator endpoint.
6. `KUBEOPENCODE_ENV_*` variables are validated and synchronized. A changed set
   rolls the Agent; the launcher waits for the new Deployment to be fully ready.
7. The launcher allocates an ephemeral loopback port, injects the bearer token
   toward the mediator, and executes `opencode attach` locally.
8. The mediator removes the bearer credential and proxies HTTP/SSE/WebSocket
   through the KubeOpenCode server. OpenCode commands
   execute in the Agent pod and can write only the mounted PVC subpath.

## Configuration sequence

```text
extension ─Bearer token─> mediator ─token introspection─> JupyterHub
                              │
                              ├─ ConfigMap (credential-free user settings)
                              └─ Agent CR ─> KubeOpenCode controller ─> Agent pod
```

The model credential never traverses the extension or mediator request body.
The Agent references an administrator-provisioned Kubernetes Secret or an
internal model gateway configured in the workspace allowlist.

## Optional Task API

The API also implements snapshot-backed Task orchestration for API clients. It
uses a workspace Lease, a protected snapshot PVC, KubeOpenCode Task resources,
and explicit restore. This path is documented in the OpenAPI specification and
tested independently; it is not reachable from the extension UI.

## State and ownership

| State | Storage | Owner |
| --- | --- | --- |
| Workspace authorization | Helm values rendered as mediator ConfigMap | Platform administrator |
| Agent profile/instructions/model/Registry selections/safe environment | Per-Agent ConfigMap | Mediator |
| Skill and plugin catalog | Namespaced KubeOpenCode Registry CR | Platform administrator; mediator read-only |
| Agent desired state | KubeOpenCode Agent CR | Mediator |
| Model and private Git credentials | Kubernetes Secret | Platform administrator |
| Workspace files | User PVC subpath | User |
| Task snapshots | Separate snapshot PVC | Mediator Jobs |

## Availability and consistency

The current chart intentionally runs one mediator replica. Reconciliation is
restart-safe because Task records and Leases live in Kubernetes, but no leader
election is implemented. Do not increase the replica count until leader
election and concurrent reconciliation tests exist.

Agent environment updates are rollout-aware. The launcher verifies the desired
environment in the Deployment template and waits until old replicas are gone,
preventing attachment to a stale server pod.
