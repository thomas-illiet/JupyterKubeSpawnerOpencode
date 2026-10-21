SHELL := /bin/sh
.DEFAULT_GOAL := help

GO_CACHE ?= /tmp/jko-go-cache
GO_MOD_CACHE ?= /tmp/jko-go-mod
MEDIATOR_IMAGE ?= jupyter-kubeopencode-mediator:demo
CODE_SERVER_IMAGE ?= jko-code-server:demo
AGENT_IMAGE ?= jko-agent:demo

.PHONY: help doctor bootstrap test extension helm docs shell hygiene verify images demo-up demo-forward demo-down

help: ## List available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

doctor: ## Check required local tools and print versions
	@./hack/doctor.sh

bootstrap: ## Install locked extension dependencies and download Go modules
	npm --prefix vscode-extension ci
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go mod download

test: ## Run Go tests and static analysis
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go test -race ./...
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go vet ./...

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
	docker build --pull -t $(MEDIATOR_IMAGE) .
	docker build -t $(CODE_SERVER_IMAGE) -f demo/Dockerfile.code-server .
	docker build -t $(AGENT_IMAGE) -f demo/Dockerfile.agent .

demo-up: ## Build and deploy the reproducible Docker Desktop demo
	./hack/demo-up.sh

demo-forward: ## Forward local port 18080 to code-server
	kubectl port-forward -n vscode-hub service/jko-demo-code-server 18080:8080

demo-down: ## Remove only the demo and integration chart resources
	./hack/demo-down.sh
