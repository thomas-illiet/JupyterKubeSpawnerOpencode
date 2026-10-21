# jupyter-kubeopencode Helm chart

Installs the trusted mediator, its namespace-scoped RBAC, and optional
per-workspace NetworkPolicies for shared user namespaces. It does not create
user or Agent ServiceAccounts and does not
install JupyterHub, KubeOpenCode, PVCs, model Secrets, the extension, or the
code-server image.

See the repository's `docs/installation.md` before using this chart. Values are
validated by `values.schema.json`; the `workspaces` list and native KubeOpenCode
`registries` are authorization policy and must be controlled by platform administrators.

```sh
helm upgrade --install jupyter-kubeopencode ./charts/jupyter-kubeopencode \
  --namespace kubeopencode-system --create-namespace \
  --atomic --wait -f values.production.yaml
```

Do not place credentials in values. Use existing Secrets referenced by
`modelSecret`, or an internal model gateway configured with `openCodeBaseUrl`.
The shared namespace's existing `default` ServiceAccount must have
`automountServiceAccountToken: false` for KubeOpenCode 0.1.9 Agent pods.

Set `cleanup.enabled=true` and `cleanup.existingSecret` to expose the internal
user-cleanup endpoint to a trusted JupyterHub Authenticator hook. The Secret is
mounted as a file and must contain a random token of at least 32 characters.
Cleanup deletes only mediator-labelled resources, never PVCs or model Secrets.
