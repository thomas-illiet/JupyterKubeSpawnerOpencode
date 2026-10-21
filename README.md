# Jupyter OpenCloud

Securely configure and use isolated OpenCloud Agents from a JupyterHub-hosted
VS Code workspace. The platform uses [KubeOpenCode](https://github.com/kubeopencode/kubeopencode)
and OpenCode as pinned runtime dependencies.

The VS Code extension is a configuration surface only. Interactive work starts
from the integrated terminal with `opencloud`, through an authenticated mediator
relay. Users never receive mediator credentials,
model tokens, unrestricted Kubernetes access, or access to another workspace.

![Architecture](docs/images/architecture.svg)

## What is included

- A Go mediator that authenticates JupyterHub users and owns Kubernetes writes.
- A Helm chart with namespace-scoped RBAC and default-deny NetworkPolicies.
- A code-server extension for managing up to five Agents, instructions, models,
  and public Git skill repositories.
- A terminal launcher that selects an Agent, forwards explicitly prefixed
  non-secret variables, allocates a collision-free local port, and starts the
  OpenCloud-branded TUI through a loopback-only authenticated proxy.
- Snapshot, locking, cancellation, and restore APIs for optional automated Task
  clients. The VS Code extension does not expose Task execution.
- An authenticated JupyterHub user-deletion hook that removes the user's
  mediator-owned KubeOpenCode resources while preserving external PVCs and
  Secrets for the platform's data-retention workflow.
- A reproducible Docker Desktop demonstration and an OpenAPI contract.

## Quick start

For the local demonstration:

```sh
make doctor
make demo-up
make demo-forward
```

Then open <http://127.0.0.1:18080/?folder=/home/jovyan/work>. See
[the local demo guide](demo/README.md) for prerequisites and teardown.

For a cluster installation, start with
[docs/installation.md](docs/installation.md) and copy
[`examples/values.production.yaml`](examples/values.production.yaml) outside
the repository before adapting it.

## Documentation

| Document | Purpose |
| --- | --- |
| [Architecture](docs/architecture.md) | Components, request flows, ownership, and design constraints |
| [Installation](docs/installation.md) | Production prerequisites and ordered deployment procedure |
| [Configuration](docs/configuration.md) | Helm values, workspace mapping, extension and terminal variables |
| [Security](docs/security.md) | Trust boundaries, threat model, RBAC, secrets, storage and network controls |
| [Operations](docs/operations.md) | Health, upgrades, rollback, backup, troubleshooting and limitations |
| [Development](docs/development.md) | Reproducible toolchain, commands, tests, images and release checklist |
| [Acceptance](docs/acceptance.md) | Cluster-level multi-user verification checklist |
| [OpenAPI](docs/openapi.yaml) | Stable mediator API contract |

## Compatibility

| Component | Supported/tested version |
| --- | --- |
| Kubernetes | 1.28 or newer |
| KubeOpenCode | `v0.1.9` / chart `0.1.9` |
| KubeOpenCode API | `kubeopencode.io/v1alpha1` |
| Go | `1.24.7` |
| Node.js | `22.14.0` |
| VS Code / code-server API | `^1.96.0` |

KubeOpenCode is still an alpha API dependency. Pin its chart and images, test
upgrades in a non-production cluster, and do not point this adapter at a newer
CRD without running the acceptance suite.

## Verification

```sh
make verify
```

This runs Go tests and vet, extension type-check/package, Helm lint/template,
OpenAPI parsing, shell lint when available, and repository hygiene checks.
Cluster isolation still requires the manual tests in
[docs/acceptance.md](docs/acceptance.md).

`opencloud` is the user-facing command. The `opencode` executable name remains
available only as a compatibility alias for existing automation and upstream
subcommands.

## Security reporting

Do not open a public issue containing credentials, tenant names, PVC names, or
cluster topology. Follow [SECURITY.md](SECURITY.md).

Licensed under Apache-2.0. See [LICENSE](LICENSE).
