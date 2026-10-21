import * as vscode from "vscode";

type Registry = { name: string; skills: { name: string; description?: string; tags?: string[]; required: boolean }[]; plugins: { name: string; description?: string; tags?: string[] }[] };
type SkillSource = { name: string; repository: string; ref?: string; path?: string; names?: string[] };
type AgentConfig = { id: string; resourceName: string; profile: string; instructions: string; model: string; allowedModels: string[]; customSkillSources?: SkillSource[]; selectedSkills?: string[]; selectedPlugins?: string[]; environment?: Record<string, string>; updatedAt?: string };

class MediatorClient {
  private readonly token: string;
  private readonly baseUrl: URL;
  constructor() {
    const config = vscode.workspace.getConfiguration("kubeOpenCode");
    const rawUrl = config.get<string>("mediatorUrl", "").trim();
    this.token = process.env.JUPYTERHUB_API_TOKEN ?? "";
    if (!rawUrl || !this.token) throw new Error("mediatorUrl and JUPYTERHUB_API_TOKEN are required");
    this.baseUrl = new URL(rawUrl);
    const safeHTTP = ["127.0.0.1", "::1", "localhost"].includes(this.baseUrl.hostname) || this.baseUrl.hostname.endsWith(".svc") || this.baseUrl.hostname.endsWith(".svc.cluster.local");
    if (this.baseUrl.protocol !== "https:" && !safeHTTP) throw new Error("Plain HTTP is allowed only for loopback or Kubernetes service addresses");
  }
  listAgents(): Promise<AgentConfig[]> { return this.request("/api/v1/me/agents"); }
  getRegistry(): Promise<Registry> { return this.request("/api/v1/me/registry"); }
  createAgent(value: Pick<AgentConfig, "id" | "profile" | "instructions" | "model" | "customSkillSources" | "selectedSkills" | "selectedPlugins" | "environment">): Promise<AgentConfig> { return this.request("/api/v1/me/agents", { method: "POST", body: JSON.stringify(value) }); }
  updateAgent(id: string, value: Pick<AgentConfig, "profile" | "instructions" | "model" | "customSkillSources" | "selectedSkills" | "selectedPlugins" | "environment">): Promise<AgentConfig> { return this.request(`/api/v1/me/agents/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(value) }); }
  async deleteAgent(id: string): Promise<void> { await this.request(`/api/v1/me/agents/${encodeURIComponent(id)}`, { method: "DELETE" }); }
  private async request<T>(route: string, init: RequestInit = {}): Promise<T> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 30_000);
    try {
      const response = await fetch(new URL(route, this.baseUrl), { ...init, signal: controller.signal, headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json", ...(init.headers ?? {}) } });
      if (!response.ok) { const body = await response.json().catch(() => ({})) as { detail?: string }; throw new Error(body.detail ?? `Mediator returned HTTP ${response.status}`); }
      return response.status === 204 ? undefined as T : await response.json() as T;
    } catch (error) {
      if (controller.signal.aborted) throw new Error("The mediator did not respond within 30 seconds.");
      throw error;
    } finally {
      clearTimeout(timeout);
    }
  }
}

type WebEvent = { type?: string; id?: unknown; sourceId?: unknown; requestId?: unknown; profile?: unknown; instructions?: unknown; model?: unknown; customSkillSources?: unknown; selectedSkills?: unknown; selectedPlugins?: unknown; value?: unknown };
class AgentViewProvider implements vscode.WebviewViewProvider {
  private view?: vscode.WebviewView;
  private agents: AgentConfig[] = [];
  private registry?: Registry;
  private dirty = false;
  private modalOpen = false;
  private busy = false;
  private terminalOpening = false;
  selectedAgentId = "default";
  constructor(private readonly client: MediatorClient, private readonly extensionUri: vscode.Uri) {}
  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
    view.webview.options = { enableScripts: true, localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, "dist")] };
    view.webview.html = agentViewHTML(view.webview, this.extensionUri);
    view.webview.onDidReceiveMessage((event: WebEvent) => void this.handle(event));
    view.onDidDispose(() => { if (this.view === view) { this.view = undefined; this.dirty = false; this.modalOpen = false; } });
  }
  private async handle(event: WebEvent): Promise<void> {
    if (event.type === "dirty" && typeof event.value === "boolean") { this.dirty = event.value; return; }
    if (event.type === "modal" && typeof event.value === "boolean") { this.modalOpen = event.value; return; }
    if (event.type === "ready") {
      this.modalOpen = false;
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      this.busy = true;
      try { await this.load(); } finally { this.busy = false; }
      return;
    }
    if (event.type === "refresh") {
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      this.busy = true;
      try { await this.load("refreshFailed"); } finally { this.busy = false; }
      return;
    }
    if (event.type === "select" && typeof event.id === "string") {
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      if (this.agents.some((agent) => agent.id === event.id)) { this.dirty = false; this.selectedAgentId = event.id; }
      return this.postState();
    }
    if (event.type === "save") {
      if (typeof event.id !== "string" || event.id !== this.selectedAgentId || typeof event.profile !== "string" || typeof event.instructions !== "string" || typeof event.model !== "string" || !isSkillSourceArray(event.customSkillSources) || !isStringArray(event.selectedSkills) || !isStringArray(event.selectedPlugins)) { this.postState(); this.post({ type: "actionFailed", message: "The Agent changed before the configuration could be saved." }); return; }
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      const targetId = event.id;
      const current = this.agents.find((agent) => agent.id === targetId);
      if (!current || !current.allowedModels.includes(event.model)) { this.postState(); this.post({ type: "actionFailed", message: "The selected model is no longer available for this Agent." }); return; }
      this.busy = true;
      const saveError = await run(async () => {
        const updated = await this.client.updateAgent(targetId, { profile: event.profile as string, instructions: event.instructions as string, model: event.model as string, customSkillSources: event.customSkillSources as SkillSource[], selectedSkills: event.selectedSkills as string[], selectedPlugins: event.selectedPlugins as string[], environment: current.environment ?? {} });
        this.agents = this.agents.map((agent) => agent.id === targetId ? updated : agent);
        this.dirty = false;
        this.postState();
      });
      this.busy = false;
      if (saveError) this.post({ type: "saveFailed", message: `Could not save changes: ${saveError}` });
      return;
    }
    if (event.type === "create" && typeof event.id === "string" && typeof event.sourceId === "string") {
      if (event.sourceId !== this.selectedAgentId) { this.postState(); this.post({ type: "actionFailed", message: "The selected Agent changed before creation started." }); return; }
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      const id = event.id.trim();
      if (!/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(id) || id.length > 30) { this.post({ type: "actionFailed", message: "Use a lowercase identifier with letters, numbers, or hyphens." }); return; }
      if (this.agents.length >= 5) { this.post({ type: "actionFailed", message: "This workspace already has five Agents." }); return; }
      if (this.agents.some((agent) => agent.id === id)) { this.post({ type: "actionFailed", message: `Agent "${id}" already exists.` }); return; }
      const base = this.agents.find((agent) => agent.id === event.sourceId) ?? this.agents[0];
      if (!base) { this.post({ type: "actionFailed", message: "No Agent configuration is available to use as a base." }); return; }
      this.busy = true;
      const createError = await run(async () => {
        const createdAgent = await this.client.createAgent({ id, profile: `Agent ${id}`, instructions: "", model: base.model, customSkillSources: [], selectedSkills: this.registry?.skills.filter((skill) => skill.required).map((skill) => skill.name) ?? [], selectedPlugins: [], environment: {} });
        this.agents = [...this.agents.filter((agent) => agent.id !== id), createdAgent];
        this.selectedAgentId = id;
        this.dirty = false;
        this.postState();
      });
      this.busy = false;
      if (createError) this.post({ type: "actionFailed", message: `Could not create the Agent: ${createError}` });
      return;
    }
    if (event.type === "delete") {
      if (typeof event.id !== "string" || event.id !== this.selectedAgentId || this.selectedAgentId === "default") { this.post({ type: "actionFailed", message: "The Agent changed before deletion started." }); return; }
      if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
      const targetId = event.id;
      this.busy = true;
      const deleteError = await run(async () => {
        await this.client.deleteAgent(targetId);
        this.agents = this.agents.filter((agent) => agent.id !== targetId);
        this.selectedAgentId = this.agents.some((agent) => agent.id === "default") ? "default" : this.agents[0]?.id ?? "default";
        this.dirty = false;
        this.postState();
      });
      this.busy = false;
      if (deleteError) this.post({ type: "actionFailed", message: `Could not delete the Agent: ${deleteError}` });
      return;
    }
    if (event.type === "terminal") {
      if (typeof event.id !== "string" || event.id !== this.selectedAgentId || typeof event.requestId !== "string") {
        this.post({ type: "terminalFailed", requestId: event.requestId, id: event.id, message: "The selected Agent changed before the terminal could be opened." });
        return;
      }
      void this.openTerminal(event.id, event.requestId);
    }
  }
  async refresh(): Promise<void> {
    if (this.busy) { this.post({ type: "busy", message: "The current Agent action is still in progress." }); return; }
    if (this.modalOpen && this.view) { this.post({ type: "refreshBlocked" }); return; }
    if (this.dirty && this.view) { this.post({ type: "requestRefresh" }); return; }
    this.post({ type: "loading", message: "Refreshing…" });
    this.busy = true;
    try { await this.load("refreshFailed"); } finally { this.busy = false; }
  }
  async openTerminal(agentId = this.selectedAgentId, requestId?: string): Promise<void> {
    if (this.terminalOpening) {
      const busyMessage = "A terminal is already being opened.";
      if (requestId) this.post({ type: "terminalFailed", requestId, id: agentId, message: busyMessage });
      else void vscode.window.showWarningMessage(busyMessage);
      return;
    }
    this.terminalOpening = true;
    try {
      const terminal = vscode.window.createTerminal({ name: `OpenCloud · ${agentId}`, shellPath: "/usr/local/bin/opencloud", shellArgs: [agentId] });
      terminal.show();
      if (requestId) this.post({ type: "terminalOpened", requestId, id: agentId });
    } catch (error) {
      const failure = `Could not open the terminal: ${message(error)}`;
      if (requestId) this.post({ type: "terminalFailed", requestId, id: agentId, message: failure });
      else void vscode.window.showErrorMessage(failure);
    } finally {
      await new Promise((resolve) => setTimeout(resolve, 500));
      this.terminalOpening = false;
    }
  }
  async load(failureType: "error" | "refreshFailed" = "error"): Promise<void> { try { [this.agents, this.registry] = await Promise.all([this.client.listAgents(), this.client.getRegistry()]); if (!this.agents.some((agent) => agent.id === this.selectedAgentId)) this.selectedAgentId = this.agents[0]?.id ?? "default"; this.dirty = false; this.postState(); } catch (error) { this.post({ type: failureType, message: message(error) }); } }
  private postState(): void { this.post({ type: "config", agents: this.agents, registry: this.registry, selected: this.selectedAgentId }); }
  private post(value: unknown): void { void this.view?.webview.postMessage(value); }
}

export function activate(context: vscode.ExtensionContext): void {
  let client: MediatorClient;
  try { client = new MediatorClient(); } catch (error) { void vscode.window.showErrorMessage(`OpenCloud is not configured: ${message(error)}`); return; }
  const provider = new AgentViewProvider(client, context.extensionUri);
  context.subscriptions.push(vscode.window.registerWebviewViewProvider("kubeOpenCode.agent", provider, { webviewOptions: { retainContextWhenHidden: true } }), vscode.commands.registerCommand("kubeOpenCode.openTerminal", () => provider.openTerminal()), vscode.commands.registerCommand("kubeOpenCode.refreshAgents", () => provider.refresh()));
}
export function deactivate(): void {}
async function run(action: () => Promise<void>): Promise<string | undefined> { try { await action(); return undefined; } catch (error) { return message(error); } }
function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
function isStringArray(value: unknown): value is string[] { return Array.isArray(value) && value.every((item) => typeof item === "string"); }
function isSkillSourceArray(value: unknown): value is SkillSource[] {
  return Array.isArray(value) && value.every((item) => {
    if (!item || typeof item !== "object") return false;
    const source = item as Record<string, unknown>;
    return typeof source.name === "string" && typeof source.repository === "string" &&
      (source.ref === undefined || typeof source.ref === "string") &&
      (source.path === undefined || typeof source.path === "string") &&
      (source.names === undefined || isStringArray(source.names));
  });
}

function agentViewHTML(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const scriptUri = webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, "dist", "webview.js"));
  return `<!doctype html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src ${webview.cspSource};">
<style>
:root{color-scheme:light dark}*{box-sizing:border-box}html,body{min-height:100%;background:var(--vscode-sideBar-background,var(--vscode-editor-background));color:var(--vscode-sideBar-foreground,var(--vscode-editor-foreground))}body{margin:0;padding:0 12px 94px;font:var(--vscode-font-size) var(--vscode-font-family)}button,input,textarea,select{font:inherit}button:focus-visible,input:focus-visible,textarea:focus-visible,select:focus-visible,summary:focus-visible{outline:1px solid var(--vscode-focusBorder);outline-offset:2px}.top{padding:14px 0 12px;border-bottom:1px solid var(--vscode-panel-border)}.brand{display:flex;gap:10px;align-items:center}.mark{display:grid;place-items:center;width:31px;height:31px;border-radius:7px;background:var(--vscode-button-background);color:var(--vscode-button-foreground);font-size:17px}.title,.agent-name,.field label,.skill-title{color:var(--vscode-sideBar-foreground,var(--vscode-editor-foreground));font-weight:650}.title{font-size:14px}.eyebrow,.muted{font-size:11px;color:var(--vscode-descriptionForeground)}.workspace{margin-top:10px;padding:6px 8px;border:1px solid var(--vscode-panel-border);border-radius:4px;background:var(--vscode-editorWidget-background);font:11px var(--vscode-editor-font-family)}
.picker{display:grid;grid-template-columns:1fr 30px 30px;gap:6px;margin:14px 0 10px}.picker select,.field input,.field textarea,.field select{width:100%;border:1px solid var(--vscode-input-border,transparent);border-radius:3px;color:var(--vscode-input-foreground);background:var(--vscode-input-background);padding:7px 8px;outline:none}.picker select:focus,.field input:focus,.field textarea:focus,.field select:focus{border-color:var(--vscode-focusBorder)}button{border:0;border-radius:3px;padding:7px 9px;color:var(--vscode-button-foreground);background:var(--vscode-button-background);cursor:pointer}button:where(:not(:disabled)):hover{background:var(--vscode-button-hoverBackground)}button.icon{padding:0;font-size:16px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}button:disabled{opacity:.45;cursor:default}.summary{padding:10px;border:1px solid var(--vscode-panel-border);border-radius:6px;background:var(--vscode-editorWidget-background)}.summary-row{display:flex;justify-content:space-between;gap:8px;align-items:center}.badge{font-size:10px;padding:2px 6px;border-radius:9px;background:var(--vscode-badge-background);color:var(--vscode-badge-foreground)}.resource{margin-top:5px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:10px var(--vscode-editor-font-family);color:inherit;opacity:.68}
.tabs{display:flex;margin:14px 0 16px;border-bottom:1px solid var(--vscode-panel-border)}.tab{flex:1;min-width:0;padding:8px 2px;font-size:11px;background:transparent;color:var(--vscode-descriptionForeground);border-radius:0;border-bottom:2px solid transparent}.tab:hover{background:var(--vscode-toolbar-hoverBackground);color:var(--vscode-sideBar-foreground,var(--vscode-editor-foreground))}.tab.active{color:var(--vscode-sideBar-foreground,var(--vscode-editor-foreground));border-bottom-color:var(--vscode-focusBorder)}.panel{display:none;min-width:0}.panel.active{display:block}.panel-intro{margin-bottom:16px}.panel-intro strong{display:block;margin-bottom:4px;font-size:14px}.field{margin-bottom:14px}.field label{display:flex;justify-content:space-between;margin-bottom:6px}.field textarea{resize:vertical;min-height:150px}.help{margin-top:4px;font-size:11px;line-height:1.4;color:var(--vscode-descriptionForeground)}.asset-section{margin-bottom:20px}.section-head{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:8px}.section-title{margin:0;font-size:12px;font-weight:650}.section-count{font-size:10px;padding:2px 7px;border-radius:9px;color:var(--vscode-badge-foreground);background:var(--vscode-badge-background);white-space:nowrap}.required-pill{font-size:9px;line-height:1;padding:4px 6px;border-radius:8px;color:var(--vscode-badge-foreground);background:var(--vscode-badge-background)}.custom-card{display:grid;gap:9px;padding:10px;border:1px solid var(--vscode-panel-border);border-radius:5px;background:var(--vscode-editorWidget-background)}.custom-head{display:flex;justify-content:space-between;align-items:center;gap:8px}.custom-title{font-size:11px;font-weight:650}.custom-fields{display:grid;gap:8px}.custom-field{display:grid;gap:4px}.custom-field>span{font-size:10px;font-weight:650;color:var(--vscode-descriptionForeground)}.custom-fields input{width:100%;min-width:0;border:1px solid var(--vscode-input-border,transparent);border-radius:3px;color:var(--vscode-input-foreground);background:var(--vscode-input-background);padding:7px 8px;outline:none}.custom-fields input:focus{border-color:var(--vscode-focusBorder)}.advanced{border-top:1px solid var(--vscode-panel-border);padding-top:7px}.advanced>summary{font-size:10px;cursor:pointer;color:var(--vscode-descriptionForeground)}.advanced-fields{display:grid;gap:8px;margin-top:8px}.remove{padding:2px 6px;background:transparent;color:var(--vscode-errorForeground)}.empty{padding:15px 10px;text-align:center;border:1px dashed var(--vscode-panel-border);border-radius:5px;color:var(--vscode-descriptionForeground)}.add{width:100%;margin-top:8px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}.footer{position:fixed;z-index:10;right:0;bottom:0;left:0;padding:10px 12px 8px;background:var(--vscode-sideBar-background,var(--vscode-editor-background));border-top:1px solid var(--vscode-panel-border);box-shadow:0 -4px 12px var(--vscode-widget-shadow,rgba(0,0,0,.16))}.primary{width:100%;font-weight:650}.primary:disabled{opacity:1;background:var(--vscode-button-secondaryBackground);color:var(--vscode-disabledForeground,var(--vscode-button-secondaryForeground))}.terminal{position:relative;isolation:isolate;display:flex;align-items:center;justify-content:center;gap:7px;overflow:hidden;width:100%;margin-top:7px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground);font-family:var(--vscode-editor-font-family);letter-spacing:.1px}.terminal::before{content:'';position:absolute;z-index:-1;inset:0;background:linear-gradient(105deg,transparent 0 32%,rgba(63,185,80,.08) 43%,rgba(63,185,80,.22) 50%,rgba(63,185,80,.08) 57%,transparent 68%);transform:translateX(-125%)}.terminal:hover,.terminal:focus-visible,.terminal.launching{background:var(--vscode-button-secondaryHoverBackground);color:var(--vscode-terminal-ansiGreen,#3fb950);box-shadow:inset 0 0 0 1px var(--vscode-terminal-ansiGreen,#3fb950),0 0 10px rgba(63,185,80,.13)}.terminal:hover::before,.terminal:focus-visible::before{animation:terminal-scan .75s ease-out}.terminal.launching{cursor:wait}.terminal.launching::before{animation:terminal-scan .6s linear infinite}.terminal-prompt{color:var(--vscode-terminal-ansiGreen,#3fb950);font-weight:750}.terminal:hover .terminal-prompt,.terminal:focus-visible .terminal-prompt{animation:terminal-glitch .38s steps(2,end)}.terminal-cursor{width:.55ch;color:var(--vscode-terminal-ansiGreen,#3fb950);animation:terminal-blink 1.05s steps(1,end) infinite}.terminal.launching .terminal-cursor{animation-duration:.32s}@keyframes terminal-scan{to{transform:translateX(125%)}}@keyframes terminal-blink{0%,48%{opacity:1}49%,100%{opacity:.08}}@keyframes terminal-glitch{0%,100%{transform:none;text-shadow:none}33%{transform:translateX(-1px);text-shadow:1px 0 var(--vscode-terminal-ansiBrightMagenta,#d670d6),-1px 0 var(--vscode-terminal-ansiBrightCyan,#29b8db)}66%{transform:translateX(1px);text-shadow:-1px 0 var(--vscode-terminal-ansiBrightMagenta,#d670d6),1px 0 var(--vscode-terminal-ansiBrightCyan,#29b8db)}}.status{margin-top:8px;font-size:11px;color:var(--vscode-descriptionForeground)}.status:empty{display:none}.status.unsaved{color:var(--vscode-notificationsWarningIcon-foreground)}.error{color:var(--vscode-errorForeground)}
.custom-list,.selected-assets{display:grid;gap:7px}.selected-card{display:grid;grid-template-columns:18px minmax(0,1fr) auto;gap:7px;align-items:start;padding:9px 10px;border:1px solid var(--vscode-panel-border);border-radius:5px;background:var(--vscode-editorWidget-background)}.selected-icon{display:grid;place-items:center;width:16px;height:16px;margin-top:1px;border-radius:50%;background:var(--vscode-testing-iconPassed);color:var(--vscode-editor-background);font-size:10px;font-weight:700}.selected-copy{min-width:0}.selected-card strong{display:block;font-size:11px;line-height:1.3;overflow-wrap:anywhere}.selected-card .selected-description{display:block;margin-top:3px;font-size:10px;line-height:1.35;color:var(--vscode-descriptionForeground);overflow-wrap:anywhere}.selected-card .required-pill{align-self:start}.manage{width:100%;margin-top:9px}.advanced-section{margin-top:19px;border-top:1px solid var(--vscode-panel-border);padding-top:11px}.advanced-section>summary{display:grid;grid-template-columns:minmax(0,1fr) auto 12px;align-items:center;gap:7px;font-size:11px;font-weight:650;cursor:pointer;list-style:none}.advanced-section>summary::-webkit-details-marker{display:none}.advanced-section>summary:after{content:'›';font-size:17px;line-height:1;color:var(--vscode-descriptionForeground);transition:transform .12s ease}.advanced-section[open]>summary:after{transform:rotate(90deg)}.advanced-content{margin-top:11px}
.modal{width:min(520px,calc(100vw - 24px));max-width:none;max-height:calc(100dvh - 24px);margin:auto;padding:0;border:1px solid var(--vscode-widget-border,var(--vscode-panel-border));border-radius:8px;color:var(--vscode-editorWidget-foreground,var(--vscode-foreground));background:var(--vscode-editorWidget-background,var(--vscode-editor-background));box-shadow:0 10px 32px var(--vscode-widget-shadow,rgba(0,0,0,.35));overflow:hidden}.modal:not([open]){display:none}.modal[open]{display:grid;grid-template-rows:auto minmax(0,1fr) auto}.modal::backdrop{background:rgba(0,0,0,.48)}.modal-header{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px;padding:15px 16px 12px;border-bottom:1px solid var(--vscode-panel-border)}.modal-heading{min-width:0}.modal-title{font-size:14px;font-weight:650;line-height:1.3}.modal-description{margin-top:3px;font-size:11px;line-height:1.4;color:var(--vscode-descriptionForeground)}.modal-meta{margin-top:7px;font-size:10px;font-weight:650;color:var(--vscode-descriptionForeground)}.modal-notice{margin-top:7px;padding:6px 7px;border-left:2px solid var(--vscode-notificationsWarningIcon-foreground);background:var(--vscode-textBlockQuote-background);font-size:10px;line-height:1.35;color:var(--vscode-notificationsWarningIcon-foreground)}.modal-close{align-self:start;width:28px;height:28px;padding:0;border-radius:4px;background:transparent;color:var(--vscode-foreground);font-size:19px;line-height:1}.modal-close:hover:not(:disabled){background:var(--vscode-toolbar-hoverBackground)}.modal-content{min-height:0;padding:16px;overflow:auto}.modal-catalog .modal-content{display:grid;grid-template-rows:auto minmax(0,1fr);padding:0;overflow:hidden}.catalog-controls{display:grid;gap:9px;padding:12px 14px;border-bottom:1px solid var(--vscode-panel-border)}.catalog-search input,.modal-field input{width:100%;border:1px solid var(--vscode-input-border,transparent);border-radius:3px;color:var(--vscode-input-foreground);background:var(--vscode-input-background);padding:8px 9px;outline:none}.catalog-search input:focus,.modal-field input:focus{border-color:var(--vscode-focusBorder)}.catalog-filters{display:grid;grid-template-columns:1fr 1fr;gap:4px;padding:3px;border-radius:5px;background:var(--vscode-editor-background)}.catalog-filters button{padding:5px 8px;background:transparent;color:var(--vscode-descriptionForeground);font-size:10px}.catalog-filters button[aria-pressed=true]{background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}.catalog-result{font-size:9px;text-align:right;color:var(--vscode-descriptionForeground)}.asset-options{display:grid;align-content:start;gap:7px;min-height:110px;padding:10px 14px 14px;overflow:auto}.asset-option{display:grid;grid-template-columns:18px minmax(0,1fr);gap:9px;align-items:start;padding:10px;border:1px solid var(--vscode-panel-border);border-radius:5px;background:var(--vscode-sideBar-background,var(--vscode-editor-background));cursor:pointer}.asset-option:not(.required-option):hover{background:var(--vscode-list-hoverBackground)}.asset-option:focus-within{border-color:var(--vscode-focusBorder)}.asset-option input{width:15px;height:15px;margin:1px 0 0;accent-color:var(--vscode-focusBorder)}.asset-option input:disabled{opacity:.7}.required-option{cursor:default}.asset-option-copy{display:grid;gap:4px;min-width:0}.asset-option-title{display:flex;align-items:center;justify-content:space-between;gap:7px}.asset-option-title strong{font-size:11px;line-height:1.3;overflow-wrap:anywhere}.asset-option-description{font-size:10px;line-height:1.4;color:var(--vscode-descriptionForeground);overflow-wrap:anywhere}.asset-tags{display:flex;flex-wrap:wrap;gap:4px;margin-top:2px}.asset-tags span{padding:2px 5px;border-radius:8px;background:var(--vscode-textBlockQuote-background);color:var(--vscode-descriptionForeground);font-size:9px}.modal-actions{display:grid;grid-template-columns:1fr 1fr;gap:8px;padding:11px 14px;border-top:1px solid var(--vscode-panel-border);background:var(--vscode-editorWidget-background)}.secondary-button{background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}.secondary-button:hover:not(:disabled){background:var(--vscode-button-secondaryHoverBackground)}.danger-button{background:var(--vscode-inputValidation-errorBorder,var(--vscode-errorForeground));color:var(--vscode-button-foreground)}.modal-field{display:grid;gap:6px}.modal-field>span:first-child{font-size:11px;font-weight:650}.modal-help,.modal-error{font-size:10px;line-height:1.4}.modal-help{color:var(--vscode-descriptionForeground)}.modal-error{min-height:14px;color:var(--vscode-errorForeground)}.inline-warning,.confirm-callout{margin:13px 0 0;padding:9px 10px;border-left:3px solid var(--vscode-notificationsWarningIcon-foreground);background:var(--vscode-textBlockQuote-background);font-size:10px;line-height:1.45}.confirm-callout{display:grid;grid-template-columns:24px minmax(0,1fr);gap:9px;align-items:start;margin:0}.confirm-callout p{margin:0}.confirm-icon{display:grid;place-items:center;width:22px;height:22px;border-radius:50%;background:var(--vscode-notificationsWarningIcon-foreground);color:var(--vscode-editor-background);font-weight:700}.danger-callout{border-left-color:var(--vscode-errorForeground)}.danger-callout .confirm-icon{background:var(--vscode-errorForeground)}.modal-empty{display:grid;place-items:center;align-content:center;gap:5px;min-height:130px;padding:18px;text-align:center;color:var(--vscode-descriptionForeground)}.modal-empty strong{color:var(--vscode-foreground);font-size:11px}.modal-empty span{font-size:10px}.link-button{padding:4px 6px;background:transparent;color:var(--vscode-textLink-foreground)}.link-button:hover:not(:disabled){background:var(--vscode-toolbar-hoverBackground);color:var(--vscode-textLink-activeForeground)}.sr-only{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
.busy-layer{position:fixed;inset:0;z-index:40;display:grid;place-items:center;padding:16px;background:rgba(0,0,0,.26)}.busy-layer[hidden]{display:none}.busy-card{display:flex;align-items:center;gap:9px;max-width:260px;padding:11px 13px;border:1px solid var(--vscode-widget-border,var(--vscode-panel-border));border-radius:6px;background:var(--vscode-editorWidget-background,var(--vscode-editor-background));box-shadow:0 8px 24px var(--vscode-widget-shadow,rgba(0,0,0,.3));font-size:11px;font-weight:650}.busy-spinner{width:15px;height:15px;border:2px solid var(--vscode-progressBar-background);border-right-color:transparent;border-radius:50%;animation:busy-spin .8s linear infinite}@keyframes busy-spin{to{transform:rotate(360deg)}}
.modal{border-color:var(--vscode-panel-border,var(--vscode-widget-border,transparent));border-top-color:var(--vscode-focusBorder);border-top-width:3px;box-shadow:0 12px 28px rgba(0,0,0,.22)}.modal::backdrop{background:rgba(3,7,14,.4);backdrop-filter:blur(1px) saturate(.85)}.modal-header{position:relative;background:var(--vscode-sideBar-background,var(--vscode-editorWidget-background))}.modal-heading::before{content:'DIALOG';display:block;margin-bottom:4px;color:var(--vscode-focusBorder);font-size:8px;font-weight:750;letter-spacing:.13em}.modal-close{display:inline-flex;align-items:center;justify-content:center;width:32px;min-width:32px;height:32px;padding:0;border:1px solid var(--vscode-inputValidation-errorBorder,var(--vscode-errorForeground,#f14c4c));border-radius:5px;background:var(--vscode-inputValidation-errorBackground,rgba(241,76,76,.12));color:var(--vscode-errorForeground,#f14c4c);font-size:18px;font-weight:700;line-height:1}.modal-close:hover:not(:disabled){background:var(--vscode-inputValidation-errorBorder,var(--vscode-errorForeground,#c42b1c));color:var(--vscode-button-foreground,#fff);box-shadow:0 0 0 1px var(--vscode-errorForeground,#f14c4c)}.modal-close:active{transform:translateY(1px)}.modal-close:focus-visible{outline:2px solid var(--vscode-focusBorder);outline-offset:2px}.modal-close:disabled{opacity:.5;transform:none}
.modal-catalog{inset:0;width:100vw;height:100dvh;max-height:100dvh;margin:0;border-width:3px 0 0;border-radius:0;box-shadow:none}.modal-catalog .modal-content{min-width:0;min-height:0;overflow:hidden}.modal-catalog .asset-options{min-width:0;min-height:0;overflow-x:hidden;overflow-y:auto;overscroll-behavior:contain;scrollbar-gutter:stable;scrollbar-width:thin;scrollbar-color:var(--vscode-scrollbarSlider-background) transparent}.modal-catalog .asset-options::-webkit-scrollbar{width:10px}.modal-catalog .asset-options::-webkit-scrollbar-track{background:transparent}.modal-catalog .asset-options::-webkit-scrollbar-thumb{border:2px solid transparent;border-radius:999px;background:var(--vscode-scrollbarSlider-background);background-clip:padding-box}.modal-catalog .asset-options::-webkit-scrollbar-thumb:hover{background:var(--vscode-scrollbarSlider-hoverBackground);background-clip:padding-box}
button:disabled{pointer-events:none}button.icon:hover:not(:disabled),.add:hover:not(:disabled),.secondary-button:hover:not(:disabled){background:var(--vscode-button-secondaryHoverBackground);color:var(--vscode-button-secondaryForeground)}.remove:hover:not(:disabled){background:var(--vscode-inputValidation-errorBackground,rgba(241,76,76,.12));color:var(--vscode-errorForeground,#f14c4c);box-shadow:inset 0 0 0 1px var(--vscode-inputValidation-errorBorder,var(--vscode-errorForeground,#f14c4c))}.danger-button:hover:not(:disabled){background:var(--vscode-errorForeground,var(--vscode-inputValidation-errorBorder,#c42b1c));color:var(--vscode-button-foreground,#fff);box-shadow:inset 0 0 0 1px var(--vscode-button-foreground,#fff)}.catalog-filters button:hover:not(:disabled){background:var(--vscode-toolbar-hoverBackground);color:var(--vscode-foreground)}.catalog-filters button[aria-pressed=true]:hover:not(:disabled){background:var(--vscode-button-secondaryHoverBackground);color:var(--vscode-button-secondaryForeground)}
.terminal:hover,.terminal:focus-visible,.terminal.launching,.terminal.opened{color:var(--vscode-button-secondaryForeground)}.terminal-cursor{opacity:.35;animation:none}.terminal.launching .terminal-cursor{opacity:1;animation:terminal-blink .32s steps(1,end) infinite}.terminal.opened{box-shadow:inset 0 0 0 1px var(--vscode-terminal-ansiGreen,#3fb950),0 0 12px rgba(63,185,80,.18)}.terminal.opened .terminal-prompt{text-shadow:0 0 7px currentColor}.terminal.opened .terminal-cursor{opacity:1;animation:terminal-opened .7s ease-out}.terminal-feedback{margin-top:6px;font-size:10px;line-height:1.35}.terminal-feedback.error{color:var(--vscode-errorForeground)}@keyframes terminal-opened{0%{transform:scaleY(.2);opacity:.2}45%{transform:scaleY(1.25);opacity:1}100%{transform:none;opacity:1}}
.terminal{background:var(--vscode-button-background);color:var(--vscode-button-foreground)}.terminal::before{content:none}.terminal:hover,.terminal:focus-visible,.terminal.launching{background:var(--vscode-button-hoverBackground);color:var(--vscode-button-foreground);box-shadow:none}.terminal-prompt,.terminal-cursor{color:var(--vscode-button-foreground);opacity:1;animation:none}.terminal:hover .terminal-prompt,.terminal:focus-visible .terminal-prompt,.terminal.launching .terminal-cursor,.terminal.opened .terminal-cursor{animation:none}.terminal.opened{color:var(--vscode-button-foreground);box-shadow:none}.terminal.opened .terminal-prompt{text-shadow:none}
@media(max-width:480px){.modal{width:calc(100vw - 16px);max-height:calc(100dvh - 16px);border-radius:8px}.modal-catalog{inset:0;width:100vw;height:100dvh;max-height:100dvh;margin:0;border-radius:0}.modal-header{padding:13px 14px 11px}.modal-actions{padding:10px 12px}.asset-options{padding-right:12px;padding-left:12px}}
@media(forced-colors:active){.modal{border:2px solid CanvasText;box-shadow:none}.modal::backdrop{background:rgba(0,0,0,.78);backdrop-filter:none}.modal-close{border-color:LinkText;background:Canvas;color:LinkText}.modal-close:hover:not(:disabled){background:Highlight;color:HighlightText}button.icon:hover:not(:disabled),.add:hover:not(:disabled),.secondary-button:hover:not(:disabled),.catalog-filters button:hover:not(:disabled){outline:1px solid Highlight;outline-offset:-1px}.remove:hover:not(:disabled),.danger-button:hover:not(:disabled){background:Highlight;color:HighlightText;box-shadow:none}}
@media(prefers-reduced-transparency:reduce){.modal::backdrop{background:rgba(3,7,14,.58);backdrop-filter:none}}
@media(prefers-reduced-motion:reduce){.advanced-section>summary:after{transition:none}.busy-spinner,.terminal::before,.terminal-prompt,.terminal-cursor{animation:none!important}}
</style></head><body><div id="appShell">
<header class="top"><div class="brand"><div class="mark">◈</div><div><div class="title">OpenCloud Agents</div><div class="eyebrow">Identity managed by JupyterHub</div></div></div></header>
<div class="picker"><select id="agent" aria-label="Agent"></select><button id="create" class="icon" type="button" title="Create an Agent" aria-label="Create an Agent">＋</button><button id="delete" class="icon" type="button" title="Delete this Agent" aria-label="Delete this Agent"><svg aria-hidden="true" viewBox="0 0 16 16" width="15" height="15"><path fill="currentColor" d="M6.5 2h3l.5 1H13v1H3V3h3l.5-1ZM4 5h8l-.6 9H4.6L4 5Zm2 1 .3 7h1.1L7.2 6H6Zm2.8 0-.2 7h1.1l.3-7H8.8Z"/></svg></button></div><section class="summary" aria-label="Selected Agent"><div class="summary-row"><span id="agentName" class="agent-name">—</span><span id="countBadge" class="badge">0 of 5</span></div><div id="resource" class="resource">—</div></section>
<nav class="tabs" role="tablist" aria-label="Agent configuration"><button id="tab-general" class="tab active" data-tab="general" type="button" role="tab" aria-selected="true" aria-controls="general" tabindex="0">General</button><button id="tab-prompt" class="tab" data-tab="prompt" type="button" role="tab" aria-selected="false" aria-controls="prompt" tabindex="-1">Instructions</button><button id="tab-skills" class="tab" data-tab="skills" type="button" role="tab" aria-selected="false" aria-controls="skills" tabindex="-1">Skills</button><button id="tab-plugins" class="tab" data-tab="plugins" type="button" role="tab" aria-selected="false" aria-controls="plugins" tabindex="-1">Plugins</button></nav><form id="form" novalidate>
<section id="general" class="panel active" role="tabpanel" aria-labelledby="tab-general"><div class="panel-intro"><strong>General</strong><div class="help">Define how this Agent appears and which platform model it uses.</div></div><div class="field"><label for="profile">Display name</label><input id="profile" maxlength="200" placeholder="Go backend Agent"><div class="help">Shown in the Agent selector and terminal launcher.</div></div><div class="field"><label for="model">Model</label><select id="model"></select><div class="help">Only models approved by the platform are available.</div></div></section>
<section id="prompt" class="panel" role="tabpanel" aria-labelledby="tab-prompt" hidden><div class="panel-intro"><strong>Instructions</strong><div class="help">Set durable guidance that follows this Agent into every session.</div></div><div class="field"><label for="instructions"><span>Persistent instructions</span><span id="charCount" class="muted">0 / 16 384</span></label><textarea id="instructions" maxlength="16384" placeholder="Conventions, test commands, project constraints…"></textarea><div class="help">Applied to every interactive session for this Agent.</div></div></section>
<section id="skills" class="panel" role="tabpanel" aria-labelledby="tab-skills" hidden><div class="panel-intro"><strong>Skills</strong><div class="help">Combine platform-approved skills with your own public Git repositories.</div></div><div class="asset-section"><div class="section-head"><h3 class="section-title">Enabled skills</h3><span id="skillCount" class="section-count">0 enabled</span></div><div id="skillSummary" class="selected-assets"></div><button id="manageSkills" class="manage" type="button">Manage skills…</button><div class="help">Required skills remain enabled and are managed by the platform.</div></div><details id="customRepositories" class="advanced-section"><summary><span>Advanced · Custom repositories</span><span id="customCount" class="section-count">0 configured</span></summary><div class="advanced-content"><div class="help" style="margin-bottom:7px">Add public HTTPS Git repositories for skills not available in the catalogue.</div><div id="customList" class="custom-list"></div><button id="addCustom" class="add" type="button">＋ Add repository</button></div></details></section>
<section id="plugins" class="panel" role="tabpanel" aria-labelledby="tab-plugins" hidden><div class="panel-intro"><strong>Plugins</strong><div class="help">Extend the Agent with version-pinned plugins approved by the platform.</div></div><div class="asset-section"><div class="section-head"><h3 class="section-title">Enabled plugins</h3><span id="pluginCount" class="section-count">0 enabled</span></div><div id="pluginSummary" class="selected-assets"></div><button id="managePlugins" class="manage" type="button">Manage plugins…</button><div class="help">Plugin changes take effect after you save the Agent configuration.</div></div></section>
<div class="footer"><button id="save" class="primary" type="submit" disabled>Save changes</button><button id="terminal" class="terminal" type="button" aria-label="Open this Agent in a terminal"><span class="terminal-prompt" aria-hidden="true">&gt;_</span><span id="terminalLabel">Open in terminal</span><span class="terminal-cursor" aria-hidden="true">▮</span></button><div id="terminalFeedback" class="terminal-feedback sr-only" role="status" aria-live="polite"></div><div id="status" class="status" role="status" aria-live="polite">Loading…</div></div></form></div>
<div id="busyLayer" class="busy-layer" hidden role="status" aria-live="polite" tabindex="-1"><div class="busy-card"><span class="busy-spinner" aria-hidden="true"></span><span id="busyLabel">Working…</span></div></div>
<dialog id="modal" class="modal" aria-labelledby="modalTitle" aria-describedby="modalDescription" tabindex="-1"><header class="modal-header"><div class="modal-heading"><div id="modalTitle" class="modal-title"></div><div id="modalDescription" class="modal-description"></div><div id="modalMeta" class="modal-meta" aria-live="polite" hidden></div><div id="modalNotice" class="modal-notice" role="status" aria-live="polite" hidden></div></div><button id="modalClose" class="modal-close" type="button" aria-label="Close dialog">×</button></header><div id="modalContent" class="modal-content"></div><footer class="modal-actions"><button id="modalCancel" class="secondary-button" type="button">Cancel</button><button id="modalConfirm" class="primary" type="button">Apply</button></footer></dialog>
<script src="${scriptUri}"></script></body></html>`;
}
