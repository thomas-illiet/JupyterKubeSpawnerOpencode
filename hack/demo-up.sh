#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root_dir"

./hack/doctor.sh

if ! docker image inspect jupyterhub-code-server:local >/dev/null 2>&1; then
  echo "Missing local base image: jupyterhub-code-server:local" >&2
  echo "Build the JupyterHub code-server platform image before running this demo." >&2
  exit 1
fi

if ! kubectl get crd agents.kubeopencode.io >/dev/null 2>&1; then
  helm upgrade --install kubeopencode \
    oci://ghcr.io/kubeopencode/helm-charts/kubeopencode \
    --version 0.1.9 \
    --namespace kubeopencode-system \
    --create-namespace \
    --set server.enabled=true \
    --atomic --wait --timeout 5m
fi

make images
docker build --pull -t jko-demo-auth:demo -f demo/Dockerfile.demo-auth .

kubectl create namespace vscode-hub --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f demo/resources.yaml
helm upgrade --install jupyter-kubeopencode charts/jupyter-kubeopencode \
  --namespace kubeopencode-system \
  -f demo/values.local.yaml \
  --atomic --wait --timeout 5m

kubectl rollout restart deployment/jko-demo-code-server -n vscode-hub
kubectl rollout restart deployment/jupyter-kubeopencode-mediator -n kubeopencode-system
kubectl rollout status deployment/jko-demo-code-server -n vscode-hub --timeout=120s
kubectl rollout status deployment/jupyter-kubeopencode-mediator -n kubeopencode-system --timeout=120s

echo "Demo ready. Run: make demo-forward"
