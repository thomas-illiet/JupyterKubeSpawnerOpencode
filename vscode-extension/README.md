# Jupyter OpenCloud extension

Configuration-only VS Code/code-server extension for owned OpenCloud Agents.
It supports Agent selection, create/update/delete, persistent instructions,
allow-listed models, platform Registry skills/plugins, up to five validated
public Git skill repositories per Agent, and opening the packaged terminal
launcher.

Registry assets are managed through a responsive searchable dialog embedded in
the extension. The same in-extension dialog foundation handles Agent creation,
deletion, and unsaved-change confirmations, with keyboard focus contained in
the active task.

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
