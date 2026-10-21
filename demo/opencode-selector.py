#!/opt/conda/bin/python3
"""Select an owned Agent, then attach through the authenticated mediator."""
import json
import os
import sys
import time
import urllib.request

ENV_PREFIX = "KUBEOPENCODE_ENV_"
SENSITIVE_PARTS = ("TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "API_KEY", "PRIVATE_KEY", "ACCESS_KEY")
PLATFORM_VARIABLES = {"DO_NOT_TRACK", "OPENCODE_DISABLE_AUTOUPDATE", "OPENCODE_DISABLE_MODELS_FETCH"}

def mediator_request(path, method="GET", body=None):
    base = os.environ["KUBEOPENCODE_MEDIATOR_URL"].rstrip("/")
    payload = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(
        f"{base}{path}", data=payload, method=method,
        headers={"Authorization": f"Bearer {os.environ['JUPYTERHUB_API_TOKEN']}", "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)

def agents():
    return mediator_request("/api/v1/me/agents")

def forwarded_environment():
    values = {}
    for source_name, value in os.environ.items():
        if not source_name.startswith(ENV_PREFIX):
            continue
        name = source_name[len(ENV_PREFIX):]
        if name in PLATFORM_VARIABLES:
            print(f"Rejected variable: {source_name} is managed by the platform", file=sys.stderr)
            raise SystemExit(2)
        if any(part in name for part in SENSITIVE_PARTS):
            print(f"Rejected variable: {source_name} looks like a Kubernetes secret", file=sys.stderr)
            raise SystemExit(2)
        values[name] = value
    return values

def sync_environment(agent):
    environment = forwarded_environment()
    if environment == (agent.get("environment") or {}):
        return
    print(f"Synchronizing {len(environment)} variable(s) and safely restarting the Agent…", flush=True)
    updated = {key: agent.get(key) for key in ("profile", "instructions", "model", "customSkillSources", "selectedSkills", "selectedPlugins")}
    updated["environment"] = environment
    mediator_request(f"/api/v1/me/agents/{agent['id']}", "PUT", updated)
    wait_for_agent(agent["id"])

def wait_for_agent(agent_id):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if mediator_request(f"/api/v1/me/agents/{agent_id}/status").get("ready"):
            return
        time.sleep(1)
    print("The Agent did not become ready after synchronizing the environment", file=sys.stderr)
    raise SystemExit(1)

def attach(agent):
    sync_environment(agent)
    base = os.environ["KUBEOPENCODE_MEDIATOR_URL"].rstrip("/")
    os.execv("/usr/local/bin/jko-attach-proxy", ["jko-attach-proxy", f"{base}/api/v1/me/agents/{agent['id']}/attach"])

def main():
    available = agents()
    by_id = {agent["id"]: agent for agent in available}
    if len(sys.argv) > 1 and sys.argv[1] in by_id:
        attach(by_id[sys.argv[1]])
    if len(sys.argv) > 1:
        os.execv("/usr/local/libexec/opencode-real", ["opencode", *sys.argv[1:]])
    if len(available) == 1:
        attach(available[0])
    print("Choose an OpenCloud Agent:")
    for index, agent in enumerate(available, 1):
        print(f"  {index}. {agent['id']} — {agent['profile']}")
    try:
        attach(available[int(input("Agent [1]: ") or "1") - 1])
    except (ValueError, IndexError):
        print("Invalid selection", file=sys.stderr)
        raise SystemExit(2)

if __name__ == "__main__":
    main()
