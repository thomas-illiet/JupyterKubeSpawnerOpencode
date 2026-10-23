# Security policy

## Supported versions

Only the latest tagged release is supported. KubeOpenCode compatibility is
explicitly pinned in `Chart.yaml`; newer alpha CRDs are unsupported until the
acceptance suite has passed.

## Reporting a vulnerability

Report vulnerabilities privately to the repository maintainers through the
hosting provider's private vulnerability reporting feature. Do not include live
tokens, model keys, kubeconfigs, tenant names, PVC contents, or production logs.

Include the affected version, impact, minimal reproduction, and whether the
issue crosses a user, namespace, workspace, or secret boundary. Maintainers
should acknowledge a report within three business days and coordinate a fix and
disclosure timeline with the reporter.

## Security assumptions

This project supports shared user namespaces only when Jupyter pods have no
Kubernetes credentials and all access is mediated. It also requires an
OIDC-backed JupyterHub identity, NetworkPolicy enforcement, and an administrator
owned workspace allowlist. See `docs/security.md` for the complete threat model.
The optional embedded model-gateway client identity is recoverable from its
executor image and must not be described as a non-exportable workload identity;
see `docs/model-gateway-mtls.md` for its exact guarantees and limitations.
