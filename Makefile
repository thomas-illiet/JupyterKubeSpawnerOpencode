SHELL := /bin/sh
.DEFAULT_GOAL := help

GO_CACHE ?= /tmp/jko-go-cache
GO_MOD_CACHE ?= /tmp/jko-go-mod
MEDIATOR_IMAGE ?= jupyter-kubeopencode-mediator:demo
CODE_SERVER_IMAGE ?= jko-code-server:demo
AGENT_IMAGE ?= jko-agent:demo
MTLS_AGENT_IMAGE ?= jko-agent-mtls:production
MTLS_CLIENT_CERT ?=
MTLS_CLIENT_KEY ?=
MTLS_GATEWAY_CA ?=
DIST_DIR ?= dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS := -s -w -buildid= -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Version=$(VERSION) -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Commit=$(COMMIT) -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Date=$(BUILD_DATE)

.PHONY: help doctor bootstrap build test extension helm docs shell hygiene verify images agent-mtls-image demo-up demo-forward demo-down

help: ## List available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

doctor: ## Check required local tools and print versions
	@./hack/doctor.sh

bootstrap: ## Install locked extension dependencies and download Go modules
	npm --prefix vscode-extension ci
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go mod download

build: ## Build versioned production Go binaries in dist
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -mod=readonly -trimpath -buildvcs=true -ldflags='$(GO_LDFLAGS)' -o $(DIST_DIR)/jupyter-kubeopencode-mediator ./cmd/mediator
	CGO_ENABLED=0 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -mod=readonly -trimpath -buildvcs=true -ldflags='$(GO_LDFLAGS)' -o $(DIST_DIR)/jko-attach-proxy ./cmd/attach-proxy

test: ## Run Go tests and static analysis
	test -z "$$(gofmt -l cmd internal)"
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go test -race ./...
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go vet ./...
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -mod=readonly ./cmd/...

extension: ## Type-check and package the VS Code extension
	npm --prefix vscode-extension run check
	npm --prefix vscode-extension run compile
	npm --prefix vscode-extension run package

helm: ## Validate and render the Helm chart
	helm lint charts/jupyter-kubeopencode
	helm lint charts/jupyter-kubeopencode -f examples/values.production.yaml
	helm template jupyter-kubeopencode charts/jupyter-kubeopencode >/dev/null
	helm template jupyter-kubeopencode charts/jupyter-kubeopencode -f examples/values.production.yaml >/dev/null

docs: ## Validate OpenAPI, JSON and SVG documentation artifacts
	python3 ./hack/validate-docs.py

shell: ## Lint shell scripts when shellcheck is available
	@if command -v shellcheck >/dev/null 2>&1; then shellcheck hack/*.sh demo/*.sh; else echo "shellcheck not installed; skipping"; fi

hygiene: ## Check formatting, generated files and repository cleanliness rules
	test -z "$$(find . -path ./.git -prune -o -path ./vscode-extension/node_modules -prune -o \( -name .DS_Store -o -name __pycache__ \) -print)"
	test -z "$$(find . -path ./.git -prune -o -path ./vscode-extension/node_modules -prune -o -type f \( -name '*.pyc' -o -name '*.log' \) -print)"
	git diff --check

verify: test extension helm docs shell hygiene ## Run all repository validation

images: extension ## Build local mediator, code-server, and RTK-enabled Agent images
	docker build --pull --provenance=mode=max --sbom=true --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t $(MEDIATOR_IMAGE) .
	docker build -t $(CODE_SERVER_IMAGE) -f demo/Dockerfile.code-server .
	docker build -t $(AGENT_IMAGE) -f demo/Dockerfile.agent .

agent-mtls-image: ## Build an Agent executor with a client identity embedded via BuildKit secrets
	test -n "$(MTLS_CLIENT_CERT)" && test -n "$(MTLS_CLIENT_KEY)" && test -n "$(MTLS_GATEWAY_CA)"
	docker build --secret id=model_mtls_client_cert,src=$(MTLS_CLIENT_CERT) --secret id=model_mtls_client_key,src=$(MTLS_CLIENT_KEY) --secret id=model_mtls_gateway_ca,src=$(MTLS_GATEWAY_CA) -t $(MTLS_AGENT_IMAGE) -f Dockerfile.agent-mtls .

demo-up: ## Build and deploy the reproducible Docker Desktop demo
	./hack/demo-up.sh

demo-forward: ## Forward local port 18080 to code-server
	kubectl port-forward -n vscode-hub service/jko-demo-code-server 18080:8080

demo-down: ## Remove only the demo and integration chart resources
	./hack/demo-down.sh
