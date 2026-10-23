# BuildKit mTLS mount targets

This directory intentionally contains no credentials. Never copy a client
certificate, private key, or gateway CA into the checkout. As a final guard,
`.dockerignore` excludes every file here except this README from Docker build
contexts; it is not a credential-storage workflow.

`Dockerfile.agent-mtls` uses this directory only as the temporary target for
the `model_mtls_client_cert`, `model_mtls_client_key`, and
`model_mtls_gateway_ca` BuildKit secret mounts. Pass absolute source paths from
outside the checkout; pass paths only, never PEM contents:

```sh
make agent-mtls-image \
  MTLS_CLIENT_CERT=/secure/identities/alice/client.crt \
  MTLS_CLIENT_KEY=/secure/identities/alice/client.key \
  MTLS_GATEWAY_CA=/secure/model-gateway/ca.crt \
  MTLS_AGENT_IMAGE=registry.example.com/platform/jko-agent-mtls-alice:VERSION
```

The resulting image remains a secret-bearing artifact even though the PEM
files are not copied into its final layers. Restrict access and follow the
[model gateway mTLS guide](../../../docs/model-gateway-mtls.md) for identity,
rotation, rollout, and verification requirements.
