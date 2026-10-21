# Configuration reference

## Mediator environment

The Helm chart renders these values; direct process configuration is intended
for development only.

| Variable | Default | Purpose |
| --- | --- | --- |
| `LISTEN_ADDRESS` | `:8080` | HTTP listen address |
| `WORKSPACES_FILE` | `/etc/kubeopencode-mediator/workspaces.json` | Administrator-owned workspace allowlist |
| `JUPYTERHUB_USER_URL` | required in production | JupyterHub token introspection endpoint |
| `KUBEOPENCODE_SERVER_URL` | internal service | Direct KubeOpenCode server URL used only by the mediator relay |
| `AUTH_TIMEOUT` | `5s` | Identity lookup timeout |
| `RECONCILE_INTERVAL` | `2s` | Task reconciliation interval |
| `TASK_TIMEOUT` | `1h` | Maximum automated Task duration |
| `SNAPSHOT_IMAGE` | `alpine:3.22` | Snapshot/restore Job image |
| `CLEANUP_TOKEN_FILE` | unset | Enables the internal user-cleanup endpoint with a token read from a mounted Secret |
| `AGENT_*` | chart values | Agent ServiceAccount and resource bounds |

## Workspace allowlist

Each workspace entry binds one canonical JupyterHub username to one PVC,
subpath, model policy, and runtime identity. Multiple entries may use the same
namespace, but each owner must occur exactly once. Requests cannot override
these fields.

Model access uses one of two modes:

- `modelSecret`, `modelSecretKey`, and `modelEnvironment` reference an existing
  Secret in the user namespace; or
- `openCodeBaseUrl` and `openCodeModel` route through an internal compatible
  gateway. `allowedModels` controls the extension selection.

`subPath` must be relative and cannot contain `..`. The snapshot PVC must be
separate from the workspace PVC so the Agent cannot modify its rollback point.

## Extension settings

| Setting | Source | Sensitive |
| --- | --- | --- |
| `kubeOpenCode.mediatorUrl` | Platform settings | No |
| `JUPYTERHUB_API_TOKEN` | Process environment | Yes |

Users configure only profile, persistent instructions, an allow-listed model,
and up to five public HTTPS skill repositories. Repository URLs with embedded
credentials, query strings, fragments, or unsafe paths are rejected.

## Terminal environment forwarding

Only variables explicitly prefixed with `KUBEOPENCODE_ENV_` are synchronized:

```sh
KUBEOPENCODE_ENV_BUILD_TARGET=test opencloud reviewer
```

The Agent receives `BUILD_TARGET=test`. The synchronized set is exact: a later
launch without the prefix removes previously forwarded variables. At most 32
variables, 4096 bytes per value, and 16 KiB total are accepted.

Names suggesting credentials (`TOKEN`, `SECRET`, `PASSWORD`, `CREDENTIAL`,
`API_KEY`, `PRIVATE_KEY`, or `ACCESS_KEY`) are rejected both by the launcher and
the mediator. Use administrator-managed Kubernetes Secrets for sensitive data.

## Managed OpenCode runtime policy

Every Agent enables only the administrator-defined `model-proxy` provider.
Session sharing, automatic updates, AI SDK OpenTelemetry, and remote model
catalog downloads are disabled. Automatic context compaction and pruning remain
enabled to bound memory and context growth.

`DO_NOT_TRACK`, `OPENCODE_DISABLE_AUTOUPDATE`, and
`OPENCODE_DISABLE_MODELS_FETCH` are platform-owned and cannot be overridden by
user launch variables.

## User deletion lifecycle

When cleanup is enabled, `DELETE /api/v1/admin/users/{username}/resources`
removes only resources carrying the mediator's exact owner and workspace labels:
KubeOpenCode Agents and Tasks, task/configuration ConfigMaps, snapshot/change
Jobs, and the workspace Lease. The operation is idempotent and uses a dedicated
bearer token; ordinary JupyterHub user tokens cannot call it.

PVCs, snapshot data and model Secrets are preserved because they are
provisioned outside this chart. Delete those through the platform's separate
data-retention workflow when policy requires it.
