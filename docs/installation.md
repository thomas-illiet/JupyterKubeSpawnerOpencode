# Production installation

## 1. Prerequisites

- Kubernetes 1.28+ and Helm 3.17+.
- A CNI that enforces ingress and egress NetworkPolicies.
- JupyterHub with OIDC and KubeSpawner.
- KubeOpenCode chart `0.1.9`, installed with its server enabled.
- One or more shared user namespaces with trusted KubeSpawner pod construction.
- Workspace and snapshot PVCs. Prefer `ReadWriteMany`; validate CSI semantics
  and same-node placement before accepting `ReadWriteOnce`.
- An internal OpenAI-compatible model gateway, or per-user/team model Secrets.
- For `modelMTLS`: a platform PKI, a TLS 1.3 gateway that requires client
  certificates and applies certificate-specific model/quota policy, and an
  explicit decision on shared versus per-workspace certificate identity.
- A private registry containing the mediator, code-server and optional mTLS
  executor images by immutable digest. Access to an mTLS image is access to a
  recoverable client identity.

Install the pinned upstream chart:

```sh
helm upgrade --install kubeopencode \
  oci://ghcr.io/kubeopencode/helm-charts/kubeopencode \
  --version 0.1.9 \
  --namespace kubeopencode-system \
  --create-namespace \
  --set server.enabled=true
```

## 2. Build and publish immutable artifacts

```sh
make verify
make images
docker push REGISTRY/jupyter-kubeopencode-mediator:VERSION
```

Build the code-server image from `demo/Dockerfile.code-server` only as a
reference. Production platforms should copy the launcher, OpenCode runtime, and
VSIX stages into their maintained Jupyter image and publish the result by
digest.

For every distinct model-gateway identity, build and publish the mTLS executor
separately. Keep the input files outside the repository and Docker context:

```sh
make agent-mtls-image \
  MTLS_CLIENT_CERT=/secure/workspace/client.crt \
  MTLS_CLIENT_KEY=/secure/workspace/client.key \
  MTLS_GATEWAY_CA=/secure/gateway/ca.crt \
  MTLS_AGENT_IMAGE=REGISTRY/jko-agent-mtls:VERSION
docker push REGISTRY/jko-agent-mtls:VERSION
docker inspect --format '{{index .RepoDigests 0}}' REGISTRY/jko-agent-mtls:VERSION
```

BuildKit does not include secret contents in its cache key. During certificate
rotation, force a no-cache rebuild (or an equivalent verified cache-busting
build), then prove the new fingerprint at the gateway before revoking the old
certificate. See [Model gateway mTLS](model-gateway-mtls.md).

## 3. Prepare each user workspace

Create distinct workspace and snapshot PVCs plus a model Secret or gateway
policy for each canonical JupyterHub username. Multiple users may share a
namespace because their pods receive no Kubernetes API credential; only trusted
KubeSpawner and mediator configuration may select these resources.

The Jupyter pod must use `automountServiceAccountToken: false` and must not mount
a kubeconfig or projected Kubernetes token. Ensure the namespace's `default`
ServiceAccount also disables automount for controller-created Agent pods, since
KubeOpenCode 0.1.9 does not expose a per-Agent automount field.

Load `examples/jupyterhub_config.py` from the protected Hub configuration and
provide `KUBEOPENCODE_WORKSPACE_BY_USER` as an administrator-managed JSON map.
The hook applies the immutable labels consumed by NetworkPolicy and explicitly
disables token automount; it does not create a ServiceAccount.

To clean KubeOpenCode resources when an administrator deletes a JupyterHub user:

1. Create a random token of at least 32 characters in an existing Kubernetes
   Secret readable by the Hub and mediator.
2. Enable `cleanup`, set `cleanup.existingSecret`, and mount the same Secret in
   JupyterHub at the path named by `KUBEOPENCODE_CLEANUP_TOKEN_FILE`.
3. Set `KUBEOPENCODE_MEDIATOR_URL` in the Hub and compose
   `KubeOpenCodeCleanupMixin` before the deployment's Authenticator class, as
   shown in `examples/jupyterhub_config.py`.

The Authenticator hook performs cleanup before completing user deletion. A
failure aborts deletion, and retrying is safe. Storage PVCs and Secrets remain
outside this lifecycle.

## 4. Configure the chart

Copy `examples/values.production.yaml` to a protected deployment repository.
Populate the authoritative `workspaces` list and set:

- an immutable mediator image digest;
- the exact JupyterHub `/hub/api/user` service URL;
- an exact API server CIDR;
- namespace names for DNS, JupyterHub and the model gateway, plus the internal
  KubeOpenCode server URL;
- resource limits and an optional runtime class such as gVisor or Kata.
- for an mTLS workspace, the HTTPS gateway URL plus an immutable `executorImage`
  carrying the intended certificate identity; keep `attachImage` credential-free
  because Task model execution remains in the persistent Agent.

Never place model keys, Git credentials, Jupyter tokens, or kubeconfigs in Helm
values.

Before installation, configure the model gateway to reject requests without an
accepted client certificate and to authorize the exact certificate identity.
NetworkPolicy only permits reachability to the gateway namespace; it does not
select a single gateway pod/port or enforce TLS.

Render and inspect before applying:

```sh
helm lint charts/jupyter-kubeopencode -f values.production.yaml
helm template jupyter-kubeopencode charts/jupyter-kubeopencode \
  --namespace kubeopencode-system -f values.production.yaml > rendered.yaml
```

## 5. Install the mediator

```sh
helm upgrade --install jupyter-kubeopencode charts/jupyter-kubeopencode \
  --namespace kubeopencode-system \
  --create-namespace \
  --atomic --wait --timeout 5m \
  -f values.production.yaml
```

## 6. Configure code-server

Install the packaged extension in the image or a read-only extensions
directory. Configure only the internal mediator URL:

```json
{
  "kubeOpenCode.mediatorUrl": "http://kubeopencode-mediator.kubeopencode-system.svc.cluster.local:8080",
  "remote.autoForwardPorts": false
}
```

Inject `JUPYTERHUB_API_TOKEN` into the process environment. Do not write it to
VS Code settings, the PVC, logs, or command-line arguments.

## 7. Validate before onboarding users

Run every item in `docs/acceptance.md` with two real OIDC identities. Retain the
rendered manifests, image digests, test output, and cluster policy evidence with
the release record. When mTLS is enabled, retain the gateway-observed client
fingerprint, negative certificate tests, rotation proof, and the declared
identity-sharing scope as well.
