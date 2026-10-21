# Local Docker Desktop demo

These resources are intentionally separate from the production JupyterHub
release. They exist because the local Keycloak/PostgreSQL deployment may be
unavailable while demonstrating the mediator and KubeOpenCode execution path.

The demo authenticator accepts only a fixed, explicitly non-production token
mounted into both code-server and the auth pod. It must never be used outside a
local disposable cluster. The production chart continues to validate real
JupyterHub user tokens.

The supplied local values use the existing OpenAI-compatible service
`knative-opencode-model-proxy.opencode.svc.cluster.local` and therefore do not
copy a provider credential into the user namespace. Change `openCodeBaseUrl`
and `openCodeModel` in `values.local.yaml` if that service is named differently
on your cluster.

The demo also expects the platform's code-server base image to exist locally as
`jupyterhub-code-server:local`. This repository layers the OpenCode binary,
authenticated launcher, and VSIX on top of that image; it does not redefine the
JupyterHub image itself.

The workflow is idempotent and pins KubeOpenCode chart `0.1.9`. It uses the
current Kubernetes context, so verify that it points to Docker Desktop (or an
equivalent disposable local cluster) before continuing:

```sh
make doctor
make demo-up
make demo-forward
```

Open `http://127.0.0.1:18080`, select the OpenCloud activity, configure an
Agent (including optional public skill repositories), then open it in the
integrated terminal.

The code-server terminal contains the OpenCloud-branded TUI and a loopback-only
mediator proxy. The user-facing `opencloud` command lists the owned Agents when several are
available. An Agent can also be selected directly:

```sh
opencloud
opencloud reviewer
```

Non-sensitive terminal variables can be synchronized into the selected Agent
by prefixing them with `KUBEOPENCODE_ENV_`. The prefix is removed remotely:

```sh
export KUBEOPENCODE_ENV_BUILD_TARGET=test
export KUBEOPENCODE_ENV_FEATURE_FLAGS=preview
opencloud reviewer
```

The Agent is rolled once when this set changes, and the launcher waits for the
new generation before attaching. Variables whose names look like credentials
(`TOKEN`, `SECRET`, `PASSWORD`, `API_KEY`, `PRIVATE_KEY`, and similar) are
rejected. Model and Git credentials must remain Kubernetes Secrets managed by
the platform administrator.

Explicit upstream subcommands such as `opencloud --version` and the internal
`opencode attach <url>` call are delegated to the pinned real binary.
The legacy `opencode` command remains available as a compatibility alias.

The code-server pod has `automountServiceAccountToken: false` and mounts no
Kubernetes token or kubeconfig. Its JupyterHub server token authenticates every
mediator request. The mediator resolves Alice's workspace and relays OpenCode
HTTP/SSE/WebSocket traffic without forwarding that token upstream.

Production deployments may place several users in one namespace. Users must
not receive Kubernetes API permissions; KubeSpawner and the mediator remain the
only components allowed to select PVCs, Secrets, pod labels, and Agent names.

Delete only demo resources with:

```sh
make demo-down
```

The teardown preserves the KubeOpenCode installation and user PVC data. Delete
those separately only when the local cluster is disposable and the data is no
longer needed.

