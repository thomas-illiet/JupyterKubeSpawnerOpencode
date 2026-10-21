#!/bin/sh
set -eu

namespace=${1:-vscode-hub}
deployment=${2:-vscode-alice-main-server}
container=${3:-opencode-server}

run() {
  kubectl exec -n "$namespace" "deployment/$deployment" -c "$container" -- "$@"
}

version=$(run rtk --version)
case "$version" in
  *"rtk 1.0.1"*) ;;
  *) echo "Unexpected RTK version: $version" >&2; exit 1 ;;
esac

run node --input-type=module -e '
  const { SafetyNetPlugin } = await import("file:///plugins/node_modules/cc-safety-net/dist/index.js");
  const hooks = await SafetyNetPlugin({ directory: "/workspace/project" });
  const before = hooks["tool.execute.before"];
  await before({ tool: "bash" }, { args: { command: "git checkout -b safety-net-e2e" } });
  let blocked = false;
  try {
    await before({ tool: "bash" }, { args: { command: "git checkout -- README.md" } });
  } catch (error) {
    blocked = String(error).includes("Safety Net") && String(error).includes("BLOCKED");
  }
  if (!blocked) throw new Error("safety-net did not block a destructive checkout");
  console.log("safety-net: safe command allowed; destructive command blocked");
'

run node --input-type=module -e '
  const { rewrite } = await import("file:///plugins/node_modules/openrtk/lib/rewrite.js");
  const rewritten = rewrite("git status");
  if (rewritten !== "rtk git status") throw new Error("openrtk did not rewrite command: " + rewritten);
  console.log("openrtk: git status rewritten as " + rewritten);
'

run sh -lc 'mkdir -p /tmp/rtk-e2e && git -C /tmp/rtk-e2e init -q && cd /tmp/rtk-e2e && rtk git status >/tmp/rtk-e2e.out && test -s /tmp/rtk-e2e.out'
echo "Agent plugin acceptance passed ($version)"
