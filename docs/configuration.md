# Configuration reference

## Mediator environment

The binaries use Cobra for commands and flags and isolated Viper instances for
configuration. Environment variables remain the production interface rendered
by Helm. For local development, each mediator variable also has a matching
lowercase kebab-case flag, for example `--listen-address` and
`--workspaces-file`. Explicit flags take precedence over environment values.

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

Every workspace references one namespaced KubeOpenCode `Registry` CR through
`registry`. `requiredSkills` must contain at least one Registry skill and is an
administrator policy: the mediator adds these skills to every Agent even when a
client omits or attempts to remove them. The chart's `registries` values render
the native `kubeopencode.io/v1alpha1` resources; users receive read-only `get`
access through the mediator, never Kubernetes access to the Registry itself.

## Extension settings

| Setting | Source | Sensitive |
| --- | --- | --- |
| `kubeOpenCode.mediatorUrl` | Platform settings | No |
| `JUPYTERHUB_API_TOKEN` | Process environment | Yes |

Users configure profile, persistent instructions, an allow-listed model,
Registry asset identifiers, and up to five public HTTPS Git repositories for
custom skills. The extension displays skill and plugin metadata returned by
`GET /api/v1/me/registry`; required skills are checked and disabled. The
mediator validates custom repository URLs, rejects embedded credentials and
name collisions, then resolves both sources into `Agent.spec.skills[]`.
Plugins, plugin options, private repository credentials, and mandatory skills
remain exclusively administrator-managed through the Registry.

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

### Embedded mTLS model client

Set `modelMTLS: true`, use an HTTPS `openCodeBaseUrl`, and set `executorImage`
to an image built from `Dockerfile.agent-mtls`. Build it without placing PEM
files in the Docker context:

```sh
make agent-mtls-image \
  MTLS_CLIENT_CERT=/secure/client.crt \
  MTLS_CLIENT_KEY=/secure/client.key \
  MTLS_GATEWAY_CA=/secure/gateway-ca.crt \
  MTLS_AGENT_IMAGE=registry.example.com/platform/jko-agent-mtls:VERSION
```

The files are BuildKit secret mounts used only while compiling the static
proxy. The resulting binary contains the client key and is therefore itself a
secret-bearing artifact: restrict registry pulls, sign the image, use immutable
digests, rotate the certificate by rebuilding, and configure the gateway to
require and authorize the client certificate. The proxy binds only to
`127.0.0.1:9443`; OpenCode is automatically pointed at that local endpoint.
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
