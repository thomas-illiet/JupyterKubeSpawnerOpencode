#!/bin/sh
set -eu

missing=""
for tool in go node npm docker kubectl helm python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    missing="$missing $tool"
  fi
done
if [ -n "$missing" ]; then
  echo "Missing required tools:$missing" >&2
  exit 1
fi

printf '%-12s %s\n' go "$(go version)"
printf '%-12s %s\n' node "$(node --version)"
printf '%-12s %s\n' npm "$(npm --version)"
printf '%-12s %s\n' docker "$(docker --version)"
printf '%-12s %s\n' kubectl "$(kubectl version --client=true 2>/dev/null | head -n 1)"
printf '%-12s %s\n' helm "$(helm version --short)"

docker info >/dev/null 2>&1 || { echo "Docker daemon is not available" >&2; exit 1; }
kubectl cluster-info >/dev/null 2>&1 || { echo "Current Kubernetes context is not reachable" >&2; exit 1; }
echo "Environment ready. Context: $(kubectl config current-context)"
