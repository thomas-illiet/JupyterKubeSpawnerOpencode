# Operations

## Health and observability

The mediator exposes `GET /livez` for process liveness and `GET /readyz` for
readiness after the first reconciliation attempt. The legacy `GET /healthz`
remains a liveness alias. Readiness deliberately does not depend on JupyterHub,
storage, the model gateway, or KubeOpenCode, which must be monitored separately.

`GET /metrics` exposes bounded Prometheus metrics without user, workspace,
request-path, prompt, or credential labels. Production logs are JSON by default;
set `LOG_LEVEL` to `debug`, `info`, `warn`, or `error`, and `LOG_FORMAT` to `json`
or `text`.

Alert on mediator restarts, 5xx rate, authentication failures, Agent rollout
timeouts, failed snapshot Jobs, PVC capacity, OOMKills, denied egress, and
KubeOpenCode controller/server availability. For mTLS workspaces, also monitor
certificate expiry/revocation, gateway client-auth failures, local connection
refusals, generic proxy 502s, and a real model health transaction. Keep logs free
of payloads and credentials.

The Agent `postStart` hook backgrounds `jko-model-mtls-proxy`; the existing
Agent readiness probe does not supervise it. An Agent can remain Ready after the
proxy exits.

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

## mTLS certificate lifecycle

The mTLS client certificate, private key, and gateway CA are fixed inside the
executor binary. Rotation is an image rollout, not a Kubernetes Secret update:

1. Issue the new material and force a no-cache image build.
2. Publish and sign a new immutable digest.
3. Update `executorImage`; keep the Task `attachImage` credential-free.
4. Wait for the complete Agent rollout and verify the new fingerprint at the
   gateway.
5. Revoke the old certificate, block the old digest, and terminate remaining
   pods or connections that use it.

If an image is disclosed, immediately revoke its certificate at the gateway.
Deleting a runtime Secret has no effect on the embedded key. Follow the detailed
CA-rotation and compromise procedure in
[Model gateway mTLS](model-gateway-mtls.md#rotation-and-compromise-response).

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

Check the model gateway Service, namespace labels, egress NetworkPolicy, and
provider configuration. Do not temporarily allow unrestricted Internet egress
in production.

For an mTLS workspace, a connection refusal on `127.0.0.1:9443` means the local
proxy is not listening. A generic HTTP 502 means the proxy is alive but its
upstream DNS/routing/TLS connection or gateway response failed. Inspect the
sanitized `/tmp/model-mtls-proxy.log` and gateway-side TLS authentication logs;
the backgrounded hook can fail after returning success, so do not rely on a
`FailedPostStartHook` event. Confirm that only loopback is listening on port 9443
and that the deployed executor image digest is expected. Never print the private
key or full credential bundle while troubleshooting.

If the Agent is Ready while this check fails, treat it as a proxy-health failure;
readiness does not cover the mTLS process. If direct gateway access succeeds
without a client certificate, fix the gateway policy rather than relying on the
Agent's local proxy or NetworkPolicy.

### PVC multi-attach error

Use RWX storage or validate RWO same-node scheduling with the CSI provider. Do
not copy a different user's PVC or mount node storage as a workaround.
