#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root_dir"

helm uninstall jupyter-kubeopencode -n kubeopencode-system --ignore-not-found
kubectl delete deployment,service,secret,serviceaccount,role,rolebinding,configmap \
  --namespace vscode-hub \
  --selector app.kubernetes.io/part-of=jko-demo \
  --ignore-not-found
kubectl delete deployment,service,secret,serviceaccount,role,rolebinding,configmap \
  --namespace kubeopencode-system \
  --selector app.kubernetes.io/part-of=jko-demo \
  --ignore-not-found
echo "Demo resources removed. KubeOpenCode itself and user PVC data were preserved."
