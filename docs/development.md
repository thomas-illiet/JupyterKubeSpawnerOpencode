# Development

## Reproducible toolchain

Versions are pinned in `.tool-versions`; Node dependencies use
`package-lock.json`; Go dependencies use `go.sum`; container bases and
KubeOpenCode artifacts are pinned in their Dockerfiles/chart metadata.

```sh
make doctor
make bootstrap
make verify
```

`make bootstrap` installs extension dependencies with `npm ci`. Go modules are
downloaded during tests/builds. The commands use repository-local output paths
or `/tmp` caches and do not write generated assets into source directories.

## Targets

| Target | Result |
| --- | --- |
| `make test` | Go tests and vet |
| `make build` | Versioned mediator and attach-proxy binaries in `dist/` |
| `make extension` | Type-check and package VSIX |
| `make helm` | Lint and render chart/default/example values |
| `make verify` | All static/repository checks |
| `make images` | Build the mediator with provenance/SBOM plus demo images |
| `make demo-up` | Build and deploy the local demo |
| `make demo-forward` | Forward code-server to `127.0.0.1:18080` |
| `make demo-down` | Remove only demo/integration resources |

## Repository layout

```text
cmd/                 Cobra entrypoints for mediator, attach proxy and demo auth
internal/api/        HTTP API split by agents, registry, tasks and lifecycle
internal/            authentication, Viper configuration and Kubernetes adapters
charts/               production Helm chart
vscode-extension/     configuration-only code-server extension
demo/                 local Docker Desktop manifests and launcher
docs/                 architecture, operations and API documentation
examples/             non-secret production values example
hack/                 reproducible developer workflows
```

Every declared Go function, including unexported helpers, carries a GoDoc-style
comment. Keep API handlers in their domain file instead of growing a single
service implementation.

## Release checklist

1. Update versions in Chart.yaml, package.json and compatibility docs.
2. Run `make verify` from a clean clone.
3. Build images with provenance/SBOM and scan them.
4. Publish immutable digests and the VSIX checksum.
5. Install the exact artifacts in staging and run `docs/acceptance.md`.
6. Tag only after acceptance evidence is retained.
