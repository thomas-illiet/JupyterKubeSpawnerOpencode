import * as vscode from "vscode";

type SkillSource = { name: string; repository: string; ref?: string; path?: string; names?: string[] };
type AgentConfig = { id: string; resourceName: string; profile: string; instructions: string; model: string; allowedModels: string[]; skillSources?: SkillSource[]; environment?: Record<string, string>; updatedAt?: string };

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
  createAgent(value: Pick<AgentConfig, "id" | "profile" | "instructions" | "model" | "skillSources" | "environment">): Promise<AgentConfig> { return this.request("/api/v1/me/agents", { method: "POST", body: JSON.stringify(value) }); }
  updateAgent(id: string, value: Pick<AgentConfig, "profile" | "instructions" | "model" | "skillSources" | "environment">): Promise<AgentConfig> { return this.request(`/api/v1/me/agents/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(value) }); }
  async deleteAgent(id: string): Promise<void> { await this.request(`/api/v1/me/agents/${encodeURIComponent(id)}`, { method: "DELETE" }); }
  private async request<T>(route: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(new URL(route, this.baseUrl), { ...init, headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json", ...(init.headers ?? {}) } });
    if (!response.ok) { const body = await response.json().catch(() => ({})) as { detail?: string }; throw new Error(body.detail ?? `Mediator returned HTTP ${response.status}`); }
    return response.status === 204 ? undefined as T : await response.json() as T;
  }
}

type WebEvent = { type?: string; id?: unknown; profile?: unknown; instructions?: unknown; model?: unknown; skillSources?: unknown };
class AgentViewProvider implements vscode.WebviewViewProvider {
  private view?: vscode.WebviewView;
  private agents: AgentConfig[] = [];
  selectedAgentId = "default";
  constructor(private readonly client: MediatorClient) {}
  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view; view.webview.options = { enableScripts: true }; view.webview.html = agentViewHTML();
    view.webview.onDidReceiveMessage((event: WebEvent) => void this.handle(event));
  }
  private async handle(event: WebEvent): Promise<void> {
    if (event.type === "ready" || event.type === "refresh") return this.load();
    if (event.type === "select" && typeof event.id === "string") { if (this.agents.some((agent) => agent.id === event.id)) this.selectedAgentId = event.id; return this.postState(); }
    if (event.type === "save") {
      if (typeof event.profile !== "string" || typeof event.instructions !== "string" || typeof event.model !== "string" || !Array.isArray(event.skillSources)) return;
      const current = this.agents.find((agent) => agent.id === this.selectedAgentId);
      const saved = await run("Applying configuration", async () => { await this.client.updateAgent(this.selectedAgentId, { profile: event.profile as string, instructions: event.instructions as string, model: event.model as string, skillSources: event.skillSources as SkillSource[], environment: current?.environment ?? {} }); await this.load(); void vscode.window.showInformationMessage(`Agent "${this.selectedAgentId}" updated.`); });
      if (!saved) this.post({ type: "saveFailed" });
      return;
    }
    if (event.type === "create") {
      const id = await vscode.window.showInputBox({ title: "New Agent", prompt: "Short identifier", placeHolder: "backend-go", ignoreFocusOut: true, validateInput: (value) => /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(value) && value.length <= 30 ? undefined : "Lowercase letters, numbers, and hyphens — 30 characters maximum" });
      if (!id) return;
      await run("Creating Agent", async () => { const base = this.agents.find((agent) => agent.id === this.selectedAgentId) ?? this.agents[0]; await this.client.createAgent({ id, profile: `Agent ${id}`, instructions: "", model: base.model, skillSources: [], environment: {} }); this.selectedAgentId = id; await this.load(); }); return;
    }
    if (event.type === "delete" && this.selectedAgentId !== "default") {
      const answer = await vscode.window.showWarningMessage(`Permanently delete Agent "${this.selectedAgentId}"?`, { modal: true }, "Delete");
      if (answer === "Delete") await run("Deleting Agent", async () => { await this.client.deleteAgent(this.selectedAgentId); this.selectedAgentId = "default"; await this.load(); }); return;
    }
    if (event.type === "terminal") void this.openTerminal();
  }
  async openTerminal(): Promise<void> {
    const agentId = this.selectedAgentId;
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Starting OpenCloud · ${agentId}…`, cancellable: false },
      async () => {
        const terminal = vscode.window.createTerminal({ name: `OpenCloud · ${agentId}`, shellPath: "/usr/local/bin/opencloud", shellArgs: [agentId] });
        terminal.show();
        await new Promise((resolve) => setTimeout(resolve, 12_000));
      },
    );
  }
  async load(): Promise<void> { try { this.agents = await this.client.listAgents(); if (!this.agents.some((agent) => agent.id === this.selectedAgentId)) this.selectedAgentId = this.agents[0]?.id ?? "default"; this.postState(); } catch (error) { this.post({ type: "error", message: message(error) }); } }
  private postState(): void { this.post({ type: "config", agents: this.agents, selected: this.selectedAgentId }); }
  private post(value: unknown): void { void this.view?.webview.postMessage(value); }
}

export function activate(context: vscode.ExtensionContext): void {
  let client: MediatorClient;
  try { client = new MediatorClient(); } catch (error) { void vscode.window.showErrorMessage(`OpenCloud is not configured: ${message(error)}`); return; }
  const provider = new AgentViewProvider(client);
  context.subscriptions.push(vscode.window.registerWebviewViewProvider("kubeOpenCode.agent", provider), vscode.commands.registerCommand("kubeOpenCode.openTerminal", () => provider.openTerminal()), vscode.commands.registerCommand("kubeOpenCode.refreshAgents", () => provider.load()));
}
export function deactivate(): void {}
async function run(label: string, action: () => Promise<void>): Promise<boolean> { try { await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: label }, action); return true; } catch (error) { void vscode.window.showErrorMessage(`${label} : ${message(error)}`); return false; } }
function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }

function agentViewHTML(): string {
  const nonce = Math.random().toString(36).slice(2);
  return `<!doctype html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
:root{color-scheme:light dark}*{box-sizing:border-box}html,body{min-height:100%;background:var(--vscode-editor-background);color:var(--vscode-editor-foreground)}body{margin:0;padding:0 12px 22px;font:var(--vscode-font-size) var(--vscode-font-family)}button,input,textarea,select{font:inherit}.top{padding:14px 0 12px;border-bottom:1px solid var(--vscode-panel-border)}.brand{display:flex;gap:10px;align-items:center}.mark{display:grid;place-items:center;width:31px;height:31px;border-radius:8px;background:var(--vscode-button-background);color:var(--vscode-button-foreground);font-size:17px}.title,.agent-name,.field label,.skill-title{color:var(--vscode-editor-foreground);font-weight:700}.title{font-size:14px}.eyebrow,.muted{font-size:11px;color:inherit;opacity:.68}.workspace{margin-top:10px;padding:6px 8px;border:1px solid var(--vscode-panel-border);border-radius:4px;background:var(--vscode-editorWidget-background);font:11px var(--vscode-editor-font-family)}
.picker{display:grid;grid-template-columns:1fr 30px 30px;gap:6px;margin:14px 0 10px}.picker select,.field input,.field textarea,.field select{width:100%;border:1px solid var(--vscode-input-border,transparent);border-radius:3px;color:var(--vscode-input-foreground);background:var(--vscode-input-background);padding:7px 8px;outline:none}.picker select:focus,.field input:focus,.field textarea:focus,.field select:focus{border-color:var(--vscode-focusBorder)}button{border:0;border-radius:3px;padding:7px 9px;color:var(--vscode-button-foreground);background:var(--vscode-button-background);cursor:pointer}button:hover{background:var(--vscode-button-hoverBackground)}button.icon{padding:0;font-size:16px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}button:disabled{opacity:.45;cursor:default}.summary{padding:10px;border:1px solid var(--vscode-panel-border);border-radius:6px;background:var(--vscode-editorWidget-background)}.summary-row{display:flex;justify-content:space-between;gap:8px;align-items:center}.badge{font-size:10px;padding:2px 6px;border-radius:9px;background:var(--vscode-badge-background);color:var(--vscode-badge-foreground)}.resource{margin-top:5px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:10px var(--vscode-editor-font-family);color:inherit;opacity:.68}
.tabs{display:flex;margin:14px 0 12px;border-bottom:1px solid var(--vscode-panel-border)}.tab{flex:1;padding:7px 3px;background:transparent;color:inherit;opacity:.62;border-radius:0;border-bottom:2px solid transparent}.tab:hover{background:var(--vscode-toolbar-hoverBackground);opacity:1}.tab.active{color:var(--vscode-editor-foreground);opacity:1;border-bottom-color:var(--vscode-focusBorder)}.panel{display:none}.panel.active{display:block}.field{margin-bottom:13px}.field label{display:flex;justify-content:space-between;margin-bottom:5px}.field textarea{resize:vertical;min-height:150px}.help{margin-top:4px;font-size:10px;line-height:1.4;color:inherit;opacity:.68}.skill{margin-bottom:9px;padding:10px;border:1px solid var(--vscode-panel-border);border-radius:5px;background:var(--vscode-editorWidget-background)}.skill-head{display:flex;justify-content:space-between;align-items:center;margin-bottom:8px}.remove{padding:2px 6px;background:transparent;color:var(--vscode-errorForeground)}.grid{display:grid;grid-template-columns:1fr 1fr;gap:8px}.grid .wide{grid-column:1/-1}.empty{padding:18px 10px;text-align:center;border:1px dashed var(--vscode-panel-border);color:inherit;opacity:.72}.add{width:100%;margin-top:8px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}.notice{margin:14px 0;padding:9px;border-left:3px solid var(--vscode-testing-iconPassed);background:var(--vscode-textBlockQuote-background);font-size:10px;line-height:1.5}.footer{position:sticky;bottom:0;margin:16px -12px -22px;padding:10px 12px;background:var(--vscode-editor-background);border-top:1px solid var(--vscode-panel-border)}.primary{width:100%;font-weight:650}.terminal{width:100%;margin-top:7px;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}.status{min-height:15px;margin-top:7px;font-size:10px;color:inherit;opacity:.72}.error{color:var(--vscode-errorForeground);opacity:1}
</style></head><body>
<header class="top"><div class="brand"><div class="mark">◈</div><div><div class="title">OpenCloud Agents</div><div class="eyebrow">Identity managed by JupyterHub</div></div></div></header>
<div class="picker"><select id="agent" aria-label="Agent"></select><button id="create" class="icon" title="Create an Agent">＋</button><button id="delete" class="icon" title="Delete this Agent">⌫</button></div><section class="summary"><div class="summary-row"><span id="agentName" class="agent-name">—</span><span id="countBadge" class="badge">0 / 5</span></div><div id="resource" class="resource">—</div></section>
<nav class="tabs"><button class="tab active" data-tab="identity" type="button">Identity</button><button class="tab" data-tab="prompt" type="button">Instructions</button><button class="tab" data-tab="skills" type="button">Skills</button></nav><form id="form">
<section id="identity" class="panel active"><div class="field"><label for="profile">Profile</label><input id="profile" maxlength="200" required placeholder="Go backend Agent"><div class="help">Describes this Agent's role in OpenCloud.</div></div><div class="field"><label for="model">Model</label><select id="model"></select><div class="help">Only models allowed by the platform are available.</div></div></section>
<section id="prompt" class="panel"><div class="field"><label for="instructions"><span>Persistent instructions</span><span id="charCount" class="muted">0 / 16 384</span></label><textarea id="instructions" maxlength="16384" placeholder="Conventions, test commands, project constraints…"></textarea><div class="help">Applied to every interactive session for this Agent.</div></div></section>
<section id="skills" class="panel"><div class="help" style="margin-bottom:10px">Add up to 5 public HTTPS repositories containing folders with a SKILL.md file.</div><div id="skillList"></div><button id="addSkill" class="add" type="button">＋ Add a skill repository</button></section>
<div class="notice">🔒 LLM tokens, Git credentials, PVCs, and Kubernetes permissions remain platform-managed. Pass a non-sensitive launch variable with: <code>KUBEOPENCODE_ENV_NAME=value opencloud &lt;agent&gt;</code>.</div><div class="footer"><button id="save" class="primary" type="submit">Save changes</button><button id="terminal" class="terminal" type="button">⌘ Open OpenCloud in terminal</button><div id="status" class="status">Loading…</div></div></form>
<script nonce="${nonce}">
const vscode=acquireVsCodeApi();let agents=[],selected='default',skills=[],dirty=false;const $=id=>document.getElementById(id),agent=$('agent'),profile=$('profile'),model=$('model'),instructions=$('instructions'),skillList=$('skillList'),status=$('status'),save=$('save');const esc=value=>String(value??'').replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
function setDirty(value=true){dirty=value;save.textContent=value?'Save changes':'Configuration saved';save.disabled=!value}
function renderSkills(){skillList.innerHTML=skills.length?skills.map((s,i)=>'<div class="skill"><div class="skill-head"><span class="skill-title">'+esc(s.name||'New repository')+'</span><button type="button" class="remove" data-remove="'+i+'">Remove</button></div><div class="grid"><div class="field wide"><label>Name</label><input data-skill="name" data-index="'+i+'" value="'+esc(s.name)+'" placeholder="team-skills"></div><div class="field wide"><label>HTTPS repository</label><input data-skill="repository" data-index="'+i+'" value="'+esc(s.repository)+'" placeholder="https://github.com/org/skills.git"></div><div class="field"><label>Reference</label><input data-skill="ref" data-index="'+i+'" value="'+esc(s.ref||'')+'" placeholder="main"></div><div class="field"><label>Subpath</label><input data-skill="path" data-index="'+i+'" value="'+esc(s.path||'')+'" placeholder="skills/"></div><div class="field wide"><label>Selected skills</label><input data-skill="names" data-index="'+i+'" value="'+esc((s.names||[]).join(', '))+'" placeholder="code-review, testing (empty = all)"></div></div></div>').join(''):'<div class="empty">No external repository.<br><span class="muted">Skills in the workspace remain available.</span></div>'}
function render(){const value=agents.find(a=>a.id===selected)||agents[0];if(!value)return;agent.replaceChildren(...agents.map(a=>{const o=document.createElement('option');o.value=a.id;o.textContent=a.profile+' · '+a.id;o.selected=a.id===selected;return o}));profile.value=value.profile;instructions.value=value.instructions;model.replaceChildren(...value.allowedModels.map(name=>{const o=document.createElement('option');o.value=name;o.textContent=name;o.selected=name===value.model;return o}));skills=structuredClone(value.skillSources||[]);$('agentName').textContent=value.profile;$('resource').textContent=value.resourceName;$('countBadge').textContent=agents.length+' / 5';$('delete').disabled=value.id==='default';$('create').disabled=agents.length>=5;$('charCount').textContent=instructions.value.length+' / 16 384';renderSkills();setDirty(false);status.textContent=value.updatedAt?'Last updated · '+new Date(value.updatedAt).toLocaleString():'Default configuration';status.className='status'}
document.querySelectorAll('.tab').forEach(tab=>tab.addEventListener('click',()=>{document.querySelectorAll('.tab,.panel').forEach(x=>x.classList.remove('active'));tab.classList.add('active');$(tab.dataset.tab).classList.add('active')}));agent.addEventListener('change',()=>{selected=agent.value;vscode.postMessage({type:'select',id:selected})});$('create').addEventListener('click',()=>vscode.postMessage({type:'create'}));$('delete').addEventListener('click',()=>vscode.postMessage({type:'delete'}));$('terminal').addEventListener('click',()=>vscode.postMessage({type:'terminal'}));
$('addSkill').addEventListener('click',()=>{if(skills.length>=5)return;skills.push({name:'',repository:'',ref:'main',path:'',names:[]});renderSkills();setDirty()});skillList.addEventListener('click',e=>{const i=e.target.dataset.remove;if(i!==undefined){skills.splice(Number(i),1);renderSkills();setDirty()}});skillList.addEventListener('input',e=>{const i=Number(e.target.dataset.index),key=e.target.dataset.skill;if(!key)return;skills[i][key]=key==='names'?e.target.value.split(',').map(x=>x.trim()).filter(Boolean):e.target.value;setDirty()});[profile,model,instructions].forEach(x=>x.addEventListener('input',()=>{if(x===instructions)$('charCount').textContent=x.value.length+' / 16 384';setDirty()}));$('form').addEventListener('submit',e=>{e.preventDefault();status.textContent='Applying configuration…';save.disabled=true;vscode.postMessage({type:'save',profile:profile.value,instructions:instructions.value,model:model.value,skillSources:skills})});window.addEventListener('message',e=>{const m=e.data;if(m.type==='config'){agents=m.agents;selected=m.selected;render()}if(m.type==='error'){status.textContent=m.message;status.className='status error';save.disabled=false}if(m.type==='saveFailed'){status.textContent='The configuration was not saved.';status.className='status error';save.disabled=false}});window.addEventListener('beforeunload',e=>{if(dirty)e.preventDefault()});vscode.postMessage({type:'ready'});
</script></body></html>`;
}
