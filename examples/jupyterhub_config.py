"""KubeSpawner hardening for Jupyter KubeOpenCode shared namespaces.

Mount this file from the protected JupyterHub configuration. The JSON mapping
is operational metadata only; authorization remains in the mediator allowlist.
"""

import asyncio
import inspect
import json
import os
import urllib.error
import urllib.parse
import urllib.request


WORKSPACE_BY_USER = json.loads(os.environ["KUBEOPENCODE_WORKSPACE_BY_USER"])


class KubeOpenCodeCleanupMixin:
    """Delete mediator-owned resources before JupyterHub forgets a user.

    Put this mixin before the deployment's real Authenticator class in the MRO.
    A cleanup failure aborts deletion so an administrator can retry safely.
    """

    async def delete_user(self, user):
        await asyncio.to_thread(_delete_kubeopencode_resources, user.name)
        result = super().delete_user(user)
        if inspect.isawaitable(result):
            await result


def _delete_kubeopencode_resources(username):
    mediator_url = os.environ["KUBEOPENCODE_MEDIATOR_URL"].rstrip("/")
    token_path = os.environ["KUBEOPENCODE_CLEANUP_TOKEN_FILE"]
    with open(token_path, encoding="utf-8") as token_file:
        token = token_file.read().strip()
    if len(token) < 32:
        raise RuntimeError("KubeOpenCode cleanup token is missing or too short")
    target = (
        f"{mediator_url}/api/v1/admin/users/"
        f"{urllib.parse.quote(username, safe='')}/resources"
    )
    request = urllib.request.Request(
        target,
        method="DELETE",
        headers={"Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            if response.status != 204:
                raise RuntimeError(
                    f"KubeOpenCode cleanup returned HTTP {response.status}"
                )
    except urllib.error.URLError as error:
        raise RuntimeError("KubeOpenCode cleanup failed") from error


def kubeopencode_modify_pod(spawner, pod):
    username = spawner.user.name
    workspace_id = WORKSPACE_BY_USER.get(username)
    if not workspace_id:
        raise RuntimeError(f"No KubeOpenCode workspace is assigned to JupyterHub user {username!r}")
    pod.metadata.labels = dict(pod.metadata.labels or {})
    pod.metadata.labels.update(
        {
            "app.kubernetes.io/component": "jupyter-workspace",
            "mediator.kubeopencode.io/workspace-id": workspace_id,
        }
    )
    pod.spec.service_account_name = "default"
    pod.spec.automount_service_account_token = False
    return pod


c.KubeSpawner.modify_pod_hook = kubeopencode_modify_pod  # noqa: F821

# Compose the mixin with the deployment's Authenticator, for example:
#
# from oauthenticator.generic import GenericOAuthenticator
# class PlatformAuthenticator(KubeOpenCodeCleanupMixin, GenericOAuthenticator):
#     pass
# c.JupyterHub.authenticator_class = PlatformAuthenticator
