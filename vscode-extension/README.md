# Jupyter OpenCloud extension

Configuration-only VS Code/code-server extension for owned OpenCloud Agents.
It supports Agent selection, create/update/delete, persistent instructions,
allow-listed models, public Git skill repositories, and opening the packaged
terminal launcher.

It intentionally does not submit Tasks, display Task logs, store tokens, edit
PVC/namespace settings, accept model credentials, or expose an OpenCode server
URL. `JUPYTERHUB_API_TOKEN` is read from the extension host environment and sent
only to the configured mediator.

Build with locked dependencies:

```sh
npm ci
npm run check
npm run package
```

Install the resulting VSIX in the maintained code-server image. Configure only
`kubeOpenCode.mediatorUrl` through platform settings. The workspace is resolved
server-side from the canonical username returned by JupyterHub.
