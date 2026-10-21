# Contributing

## Development workflow

1. Install the versions in `.tool-versions` or use the development container.
2. Run `make bootstrap` once.
3. Make a focused change with tests and documentation.
4. Run `make verify` before opening a pull request.

Do not commit generated VSIX files, `node_modules`, caches, credentials, local
values files, or cluster output. New user-controlled fields must be validated at
the mediator boundary and represented in `docs/openapi.yaml`.

Changes to RBAC, NetworkPolicy, PVC mounts, identity, secrets, or the Agent CRD
must update `docs/security.md` and the relevant acceptance tests. Never widen a
permission merely to silence an optional client warning.

Use conventional commits (`type(scope): subject`) and keep unrelated changes in
separate commits.
