# Operations

## Health and observability

The mediator exposes `GET /healthz` for liveness/readiness. A successful probe
means the process is serving, not that JupyterHub, Kubernetes, storage, the model
gateway, or KubeOpenCode are healthy. Monitor those dependencies separately.

Alert on mediator restarts, 5xx rate, authentication failures, Agent rollout
timeouts, failed snapshot Jobs, PVC capacity, OOMKills, denied egress, and
KubeOpenCode controller/server availability. Keep logs free of payloads and
credentials.

## Upgrade procedure

1. Read upstream KubeOpenCode release notes and diff CRDs/security defaults.
2. Back up workspace configuration ConfigMaps and snapshot metadata.
3. Pin the new chart and image digests in a staging environment.
4. Run `make verify` and the complete cluster acceptance suite.
5. Upgrade KubeOpenCode first, then the mediator, then code-server images.
6. Verify Agent create/update/delete, multi-agent attach, environment rollout,
   skills, cross-user denial, network egress and optional Task restore.
7. Promote the exact tested digests.

Use `helm upgrade --atomic --wait`. Roll back the mediator chart with
`helm rollback`; never downgrade CRDs without checking upstream compatibility.

## Backup and recovery

Back up workspace PVCs using the storage platform's supported snapshot product.
Back up mediator-owned Agent ConfigMaps and administrator deployment values.
Kubernetes Task snapshots are short-lived operational rollback points, not a
disaster-recovery backup system.

Model credentials should be recoverable from the organization's secret manager,
not from cluster YAML exports.

## Troubleshooting

### `local port 4096 is already in use`

The packaged launcher always passes a free ephemeral `--local-port`. Seeing this
error normally means an old launcher image is running. Roll code-server and
verify `/usr/local/libexec/opencode-selector.py` contains `--local-port`.

### Environment change never becomes ready

Inspect the Agent, its generated Deployment, events, image pulls, quota and
NetworkPolicy. The launcher waits up to 90 seconds for the exact Deployment
template and a completed rollout.

### Interactive relay unavailable

Check mediator reachability, JupyterHub token introspection and the internal
KubeOpenCode server Service. Never log the Authorization header while
troubleshooting the HTTP/SSE/WebSocket relay.

### Agent cannot reach a model

Check the model gateway Service, namespace labels, egress NetworkPolicy, CA
bundle and provider configuration. Do not temporarily allow unrestricted
Internet egress in production.

### PVC multi-attach error

Use RWX storage or validate RWO same-node scheduling with the CSI provider. Do
not copy a different user's PVC or mount node storage as a workaround.
