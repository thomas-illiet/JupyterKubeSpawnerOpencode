declare function acquireVsCodeApi(): { postMessage(message: unknown): void };

type WebviewSkill = { name: string; description?: string; tags?: string[]; required: boolean };
type WebviewPlugin = { name: string; description?: string; tags?: string[] };
type WebviewRegistry = { name: string; skills: WebviewSkill[]; plugins: WebviewPlugin[] };
type WebviewSkillSource = { name: string; repository: string; ref?: string; path?: string; names?: string[] };
type WebviewAgent = { id: string; resourceName: string; profile: string; instructions: string; model: string; allowedModels: string[]; customSkillSources?: WebviewSkillSource[]; selectedSkills?: string[]; selectedPlugins?: string[] };
type AssetKind = "skill" | "plugin";
type AssetModalState = { type: "assets"; kind: AssetKind; selection: Set<string>; query: string; filter: "all" | "selected"; trigger: HTMLElement; list: HTMLElement; search: HTMLInputElement; allFilter: HTMLButtonElement; selectedFilter: HTMLButtonElement; resultStatus: HTMLElement };
type CreateModalState = { type: "create"; trigger: HTMLElement; input: HTMLInputElement; error: HTMLElement };
type ConfirmModalState = { type: "confirm"; trigger: HTMLElement | null; pendingLabel: string; onConfirm: () => void };
type ModalState = AssetModalState | CreateModalState | ConfirmModalState;
type SkillSourceField = "name" | "repository" | "ref" | "path" | "names";

const vscode = acquireVsCodeApi();
const byId = <T extends HTMLElement>(id: string): T => {
  const element = document.getElementById(id);
  if (!element) throw new Error(`Missing element #${id}`);
  return element as T;
};
const esc = (value: unknown): string => String(value ?? "").replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" })[character] ?? character);
const sameNames = (left: string[], right: string[]): boolean => left.length === right.length && left.every((name) => right.includes(name));

let agents: WebviewAgent[] = [];
let registry: WebviewRegistry = { name: "", skills: [], plugins: [] };
let selected = "default";
let selectedSkills: string[] = [];
let selectedPlugins: string[] = [];
let customSkills: WebviewSkillSource[] = [];
let dirty = false;
let modalState: ModalState | null = null;
let modalPending = false;
let modalConfirmLabel = "Apply";
let pagePendingTrigger: HTMLElement | null = null;

const appShell = byId<HTMLElement>("appShell");
const agent = byId<HTMLSelectElement>("agent");
const profile = byId<HTMLInputElement>("profile");
const model = byId<HTMLSelectElement>("model");
const instructions = byId<HTMLTextAreaElement>("instructions");
const customList = byId<HTMLElement>("customList");
const statusEl = byId<HTMLElement>("status");
const save = byId<HTMLButtonElement>("save");
const terminal = byId<HTMLButtonElement>("terminal");
const terminalLabel = byId<HTMLElement>("terminalLabel");
const terminalFeedback = byId<HTMLElement>("terminalFeedback");
const tabs = [...document.querySelectorAll<HTMLButtonElement>(".tab")];
const modal = byId<HTMLDialogElement>("modal");
const modalTitle = byId<HTMLElement>("modalTitle");
const modalDescription = byId<HTMLElement>("modalDescription");
const modalMeta = byId<HTMLElement>("modalMeta");
const modalNotice = byId<HTMLElement>("modalNotice");
const modalContent = byId<HTMLElement>("modalContent");
const modalClose = byId<HTMLButtonElement>("modalClose");
const modalCancel = byId<HTMLButtonElement>("modalCancel");
const modalConfirm = byId<HTMLButtonElement>("modalConfirm");
const busyLayer = byId<HTMLElement>("busyLayer");
const busyLabel = byId<HTMLElement>("busyLabel");

function setDirty(value = true): void {
  dirty = value;
  save.textContent = "Save changes";
  save.disabled = !value;
  vscode.postMessage({ type: "dirty", value });
  if (value) {
    statusEl.textContent = "Unsaved changes";
    statusEl.className = "status unsaved";
  } else if (statusEl.classList.contains("unsaved")) {
    statusEl.textContent = "";
    statusEl.className = "status";
  }
}

function selectedCard(item: WebviewSkill | WebviewPlugin, required: boolean): string {
  return '<div class="selected-card"><span class="selected-icon" aria-hidden="true">✓</span><div class="selected-copy"><strong>' + esc(item.name) + '</strong><span class="selected-description">' + esc(item.description || "Platform-approved item") + '</span></div>' + (required ? '<span class="required-pill">Required</span>' : "") + "</div>";
}

function renderAssets(): void {
  const activeSkills = registry.skills.filter((skill) => selectedSkills.includes(skill.name) || skill.required);
  const activePlugins = registry.plugins.filter((plugin) => selectedPlugins.includes(plugin.name));
  byId<HTMLElement>("skillSummary").innerHTML = activeSkills.length ? activeSkills.map((skill) => selectedCard(skill, skill.required)).join("") : '<div class="empty">No skills enabled.</div>';
  byId<HTMLElement>("pluginSummary").innerHTML = activePlugins.length ? activePlugins.map((plugin) => selectedCard(plugin, false)).join("") : '<div class="empty">No optional plugins enabled.</div>';
  byId<HTMLElement>("skillCount").textContent = `${activeSkills.length} enabled`;
  byId<HTMLElement>("pluginCount").textContent = `${activePlugins.length} enabled`;
}

function renderCustom(): void {
  customList.innerHTML = customSkills.length ? customSkills.map((source, index) => '<div class="custom-card"><div class="custom-head"><span class="custom-title">Repository ' + (index + 1) + '</span><button class="remove" type="button" data-remove="' + index + '" aria-label="Remove repository ' + (index + 1) + '">Remove</button></div><div class="custom-fields"><label class="custom-field"><span>Source name</span><input data-custom="name" data-index="' + index + '" maxlength="30" placeholder="team-skills" value="' + esc(source.name) + '"></label><label class="custom-field"><span>Git repository</span><input data-custom="repository" data-index="' + index + '" type="url" maxlength="2048" placeholder="https://github.com/org/skills.git" value="' + esc(source.repository) + '"></label><details class="advanced"><summary>Advanced options</summary><div class="advanced-fields"><label class="custom-field"><span>Git ref</span><input data-custom="ref" data-index="' + index + '" maxlength="255" placeholder="main, tag, or commit" value="' + esc(source.ref || "") + '"></label><label class="custom-field"><span>Repository path</span><input data-custom="path" data-index="' + index + '" maxlength="512" placeholder="skills/" value="' + esc(source.path || "") + '"></label><label class="custom-field"><span>Skill names</span><input data-custom="names" data-index="' + index + '" placeholder="review, release" value="' + esc((source.names || []).join(", ")) + '"></label></div></details></div></div>').join("") : '<div class="empty">No custom repositories configured.</div>';
  byId<HTMLElement>("customCount").textContent = `${customSkills.length} configured`;
  byId<HTMLButtonElement>("addCustom").disabled = customSkills.length >= 5;
}

function closeModal(restoreFocus = true): void {
  const trigger = modalState?.trigger ?? null;
  const wasOpen = modal.open;
  modalPending = false;
  modal.removeAttribute("aria-busy");
  modalClose.disabled = false;
  modalCancel.disabled = false;
  modalConfirm.disabled = false;
  modalState = null;
  if (modal.open) modal.close();
  modalContent.replaceChildren();
  modal.className = "modal";
  if (wasOpen) vscode.postMessage({ type: "modal", value: false });
  if (restoreFocus) trigger?.focus();
}

function setModalPending(label: string): void {
  modalPending = true;
  modal.setAttribute("aria-busy", "true");
  modalClose.disabled = true;
  modalCancel.disabled = true;
  modalConfirm.disabled = true;
  modalConfirm.textContent = label;
  modalContent.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement>("input,button,select,textarea").forEach((element) => { element.disabled = true; });
  modal.focus();
}

function clearModalPending(message: string): void {
  modalPending = false;
  modal.removeAttribute("aria-busy");
  modalClose.disabled = false;
  modalCancel.disabled = false;
  modalConfirm.textContent = modalConfirmLabel;
  modalConfirm.disabled = false;
  modalContent.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement>("input,button,select,textarea").forEach((element) => { element.disabled = false; });
  modalNotice.textContent = message;
  modalNotice.hidden = false;
  if (modalState?.type === "create") {
    validateAgentId(modalState);
    modalState.input.focus();
  } else {
    modalCancel.focus();
  }
}

function setPagePending(value: boolean, label = "Working…"): void {
  const wasPending = !busyLayer.hidden;
  if (value && !wasPending) {
    const active = document.activeElement;
    pagePendingTrigger = active instanceof HTMLElement && appShell.contains(active) ? active : null;
  }
  appShell.inert = value;
  appShell.setAttribute("aria-busy", String(value));
  busyLabel.textContent = label;
  busyLayer.hidden = !value;
  if (value) {
    busyLayer.focus();
  } else if (wasPending) {
    const trigger = pagePendingTrigger;
    pagePendingTrigger = null;
    if (trigger?.isConnected && !(trigger instanceof HTMLButtonElement && trigger.disabled)) trigger.focus();
    else agent.focus();
  }
}

function render(): void {
  const value = agents.find((item) => item.id === selected) ?? agents[0];
  if (!value) return;
  const knownSkills = new Set(registry.skills.map((item) => item.name));
  const knownPlugins = new Set(registry.plugins.map((item) => item.name));
  const rawSkills = value.selectedSkills ?? [];
  const rawPlugins = value.selectedPlugins ?? [];
  const rawSkillSet = new Set(rawSkills);
  const rawPluginSet = new Set(rawPlugins);
  const required = registry.skills.filter((item) => item.required).map((item) => item.name);
  selectedSkills = [...new Set([...required, ...rawSkills.filter((name) => knownSkills.has(name))])];
  selectedPlugins = [...new Set(rawPlugins.filter((name) => knownPlugins.has(name)))];
  const catalogChanged = selectedSkills.length !== rawSkillSet.size || selectedSkills.some((name) => !rawSkillSet.has(name)) || selectedPlugins.length !== rawPluginSet.size || selectedPlugins.some((name) => !rawPluginSet.has(name));
  agent.replaceChildren(...agents.map((item) => {
    const option = document.createElement("option");
    option.value = item.id;
    option.textContent = `${item.profile} · ${item.id}`;
    option.selected = item.id === selected;
    return option;
  }));
  profile.value = value.profile;
  instructions.value = value.instructions;
  model.replaceChildren(...value.allowedModels.map((name) => {
    const option = document.createElement("option");
    option.value = name;
    option.textContent = name;
    option.selected = name === value.model;
    return option;
  }));
  customSkills = structuredClone(value.customSkillSources ?? []);
  byId<HTMLElement>("agentName").textContent = value.profile;
  byId<HTMLElement>("resource").textContent = value.resourceName;
  byId<HTMLElement>("countBadge").textContent = `${agents.length} of 5`;
  byId<HTMLButtonElement>("delete").disabled = value.id === "default";
  byId<HTMLButtonElement>("create").disabled = agents.length >= 5;
  byId<HTMLElement>("charCount").textContent = `${instructions.value.length} / 16 384`;
  renderAssets();
  renderCustom();
  setDirty(catalogChanged);
  if (catalogChanged) {
    statusEl.textContent = "Catalogue changed · review and save";
    statusEl.className = "status unsaved";
  } else {
    statusEl.textContent = "";
    statusEl.className = "status";
  }
}

function activateTab(tab: HTMLButtonElement, focus = false): void {
  tabs.forEach((item) => {
    const active = item === tab;
    item.classList.toggle("active", active);
    item.setAttribute("aria-selected", String(active));
    item.tabIndex = active ? 0 : -1;
    const panel = byId<HTMLElement>(item.dataset.tab ?? "");
    panel.classList.toggle("active", active);
    panel.hidden = !active;
  });
  if (focus) tab.focus();
}

function setModalShell(options: { title: string; description: string; meta?: string; confirm: string; cancel?: string; danger?: boolean; variant: "catalog" | "compact" }): void {
  modal.className = `modal modal-${options.variant}`;
  modalTitle.textContent = options.title;
  modalDescription.textContent = options.description;
  modalMeta.textContent = options.meta ?? "";
  modalMeta.hidden = !options.meta;
  modalNotice.textContent = "";
  modalNotice.hidden = true;
  modalCancel.textContent = options.cancel ?? "Cancel";
  modalConfirm.textContent = options.confirm;
  modalConfirmLabel = options.confirm;
  modalConfirm.classList.toggle("danger-button", Boolean(options.danger));
  modalConfirm.disabled = false;
}

function showModal(initialFocus?: HTMLElement): void {
  if (!modal.open) { modal.showModal(); vscode.postMessage({ type: "modal", value: true }); }
  requestAnimationFrame(() => (initialFocus ?? modalClose).focus());
}

function modalTrigger(fallback: HTMLElement): HTMLElement {
  const active = document.activeElement;
  return active instanceof HTMLElement && active !== document.body && active !== document.documentElement ? active : fallback;
}

function openConfirm(options: { title: string; description: string; message: string; confirm: string; pendingLabel: string; danger?: boolean; trigger?: HTMLElement | null; onConfirm: () => void }): void {
  closeModal(false);
  const body = document.createElement("div");
  body.className = options.danger ? "confirm-callout danger-callout" : "confirm-callout";
  const icon = document.createElement("span");
  icon.className = "confirm-icon";
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = options.danger ? "!" : "↻";
  const message = document.createElement("p");
  message.textContent = options.message;
  body.append(icon, message);
  modalContent.replaceChildren(body);
  setModalShell({ title: options.title, description: options.description, confirm: options.confirm, danger: options.danger, variant: "compact" });
  modalState = { type: "confirm", trigger: options.trigger ?? null, pendingLabel: options.pendingLabel, onConfirm: options.onConfirm };
  showModal(modalCancel);
}

function validateAgentId(state: CreateModalState): string | null {
  const value = state.input.value.trim();
  let error = "";
  if (!value) error = "Enter an Agent identifier.";
  else if (value.length > 30 || !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(value)) error = "Use lowercase letters, numbers, and hyphens (30 characters maximum).";
  else if (agents.some((item) => item.id === value)) error = "An Agent with this identifier already exists.";
  state.error.textContent = error;
  state.input.setAttribute("aria-invalid", String(Boolean(error)));
  modalConfirm.disabled = Boolean(error);
  return error ? null : value;
}

function openCreateModal(): void {
  const trigger = modalTrigger(byId<HTMLButtonElement>("create"));
  closeModal(false);
  const field = document.createElement("label");
  field.className = "modal-field";
  const label = document.createElement("span");
  label.textContent = "Agent identifier";
  const input = document.createElement("input");
  input.type = "text";
  input.maxLength = 30;
  input.placeholder = "backend-go";
  input.autocomplete = "off";
  input.spellcheck = false;
  input.setAttribute("aria-describedby", "agentIdHelp agentIdError");
  const help = document.createElement("span");
  help.id = "agentIdHelp";
  help.className = "modal-help";
  help.textContent = "Lowercase letters, numbers, and hyphens. The identifier cannot be changed later.";
  const error = document.createElement("span");
  error.id = "agentIdError";
  error.className = "modal-error";
  error.setAttribute("aria-live", "polite");
  field.append(label, input, help, error);
  const children: HTMLElement[] = [field];
  if (dirty) {
    const warning = document.createElement("p");
    warning.className = "inline-warning";
    warning.textContent = "Creating an Agent will discard the unsaved changes on the current Agent.";
    children.push(warning);
  }
  modalContent.replaceChildren(...children);
  setModalShell({ title: "Create Agent", description: "Add another isolated Agent to this workspace.", confirm: dirty ? "Discard & create" : "Create Agent", variant: "compact" });
  const state: CreateModalState = { type: "create", trigger, input, error };
  modalState = state;
  input.addEventListener("input", () => validateAgentId(state));
  validateAgentId(state);
  showModal(input);
}

function assetCollection(kind: AssetKind): Array<WebviewSkill | WebviewPlugin> {
  return kind === "skill" ? registry.skills : registry.plugins;
}

function updateAssetModalMeta(state: AssetModalState): void {
  const assets = assetCollection(state.kind);
  const requiredCount = state.kind === "skill" ? registry.skills.filter((item) => item.required).length : 0;
  const selectedCount = assets.filter((item) => state.selection.has(item.name) || ("required" in item && item.required)).length;
  modalMeta.hidden = false;
  modalMeta.textContent = requiredCount ? `${selectedCount} selected · ${requiredCount} required` : `${selectedCount} selected`;
  modalConfirm.textContent = `Apply (${selectedCount})`;
  const current = state.kind === "skill" ? selectedSkills : selectedPlugins;
  const next = assets.filter((item) => state.selection.has(item.name) || ("required" in item && item.required)).map((item) => item.name);
  modalConfirm.disabled = sameNames(current, next);
}

function assetOption(item: WebviewSkill | WebviewPlugin, state: AssetModalState): HTMLElement {
  const required = "required" in item && item.required;
  const option = document.createElement("label");
  option.className = `asset-option${required ? " required-option" : ""}`;
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.checked = required || state.selection.has(item.name);
  checkbox.disabled = required;
  checkbox.addEventListener("change", () => {
    if (checkbox.checked) state.selection.add(item.name); else state.selection.delete(item.name);
    updateAssetModalMeta(state);
    if (state.filter === "selected") { renderAssetModalList(state); state.selectedFilter.focus(); }
  });
  const copy = document.createElement("span");
  copy.className = "asset-option-copy";
  const titleRow = document.createElement("span");
  titleRow.className = "asset-option-title";
  const name = document.createElement("strong");
  name.textContent = item.name;
  titleRow.append(name);
  if (required) {
    const badge = document.createElement("span");
    badge.className = "required-pill";
    badge.textContent = "Required";
    titleRow.append(badge);
  }
  const description = document.createElement("span");
  description.className = "asset-option-description";
  description.textContent = item.description || "Platform-approved item";
  copy.append(titleRow, description);
  if (item.tags?.length) {
    const tags = document.createElement("span");
    tags.className = "asset-tags";
    item.tags.forEach((tag) => {
      const token = document.createElement("span");
      token.textContent = tag;
      tags.append(token);
    });
    copy.append(tags);
  }
  option.append(checkbox, copy);
  return option;
}

function renderAssetModalList(state: AssetModalState): void {
  const query = state.query.trim().toLocaleLowerCase();
  const assets = assetCollection(state.kind).filter((item) => {
    const selectedItem = state.selection.has(item.name) || ("required" in item && item.required);
    if (state.filter === "selected" && !selectedItem) return false;
    return !query || [item.name, item.description ?? "", ...(item.tags ?? [])].join(" ").toLocaleLowerCase().includes(query);
  });
  state.list.replaceChildren();
  if (!assets.length) {
    const empty = document.createElement("div");
    empty.className = "modal-empty";
    const title = document.createElement("strong");
    title.textContent = state.filter === "selected" ? `No selected ${state.kind}s match.` : `No ${state.kind}s found.`;
    const detail = document.createElement("span");
    detail.textContent = state.query ? "Try a different search." : "The platform catalogue has no matching entries.";
    empty.append(title, detail);
    if (state.query) {
      const clear = document.createElement("button");
      clear.type = "button";
      clear.className = "link-button";
      clear.textContent = "Clear search";
      clear.addEventListener("click", () => { state.query = ""; state.search.value = ""; renderAssetModalList(state); state.search.focus(); });
      empty.append(clear);
    }
    state.list.append(empty);
  } else {
    state.list.append(...assets.map((item) => assetOption(item, state)));
  }
  const label = state.kind === "skill" ? "skills" : "plugins";
  state.list.setAttribute("aria-label", `${assets.length} ${label} shown`);
  state.resultStatus.textContent = `${assets.length} shown`;
  state.allFilter.setAttribute("aria-pressed", String(state.filter === "all"));
  state.selectedFilter.setAttribute("aria-pressed", String(state.filter === "selected"));
  updateAssetModalMeta(state);
}

function openAssetModal(kind: AssetKind): void {
  const trigger = modalTrigger(byId<HTMLButtonElement>(kind === "skill" ? "manageSkills" : "managePlugins"));
  closeModal(false);
  const assets = assetCollection(kind);
  const current = kind === "skill" ? selectedSkills : selectedPlugins;
  const selection = new Set(current);
  if (kind === "skill") registry.skills.filter((item) => item.required).forEach((item) => selection.add(item.name));
  const controls = document.createElement("div");
  controls.className = "catalog-controls";
  const searchWrap = document.createElement("label");
  searchWrap.className = "catalog-search";
  const searchLabel = document.createElement("span");
  searchLabel.className = "sr-only";
  searchLabel.textContent = `Search ${kind}s`;
  const search = document.createElement("input");
  search.type = "search";
  search.placeholder = `Search ${kind}s…`;
  search.autocomplete = "off";
  searchWrap.append(searchLabel, search);
  const filters = document.createElement("div");
  filters.className = "catalog-filters";
  filters.setAttribute("role", "group");
  filters.setAttribute("aria-label", "Filter catalogue");
  const allFilter = document.createElement("button");
  allFilter.type = "button";
  allFilter.textContent = "All";
  const selectedFilter = document.createElement("button");
  selectedFilter.type = "button";
  selectedFilter.textContent = "Selected";
  filters.append(allFilter, selectedFilter);
  const resultStatus = document.createElement("div");
  resultStatus.className = "catalog-result";
  resultStatus.setAttribute("role", "status");
  resultStatus.setAttribute("aria-live", "polite");
  controls.append(searchWrap, filters, resultStatus);
  const list = document.createElement("div");
  list.className = "asset-options";
  list.setAttribute("role", "group");
  modalContent.replaceChildren(controls, list);
  const title = kind === "skill" ? "Manage skills" : "Manage plugins";
  const description = kind === "skill" ? "Choose platform skills for this Agent. Required skills stay locked." : "Choose platform plugins for this Agent.";
  setModalShell({ title, description, confirm: `Apply (${selection.size})`, variant: "catalog" });
  const state: AssetModalState = { type: "assets", kind, selection, query: "", filter: "all", trigger, list, search, allFilter, selectedFilter, resultStatus };
  modalState = state;
  search.addEventListener("input", () => { state.query = search.value; renderAssetModalList(state); });
  allFilter.addEventListener("click", () => { state.filter = "all"; renderAssetModalList(state); });
  selectedFilter.addEventListener("click", () => { state.filter = "selected"; renderAssetModalList(state); });
  renderAssetModalList(state);
  showModal(search);
  if (!assets.length) modalConfirm.disabled = true;
}

function applyModal(): void {
  const state = modalState;
  if (!state) return;
  if (state.type === "confirm") {
    setModalPending(state.pendingLabel);
    state.onConfirm();
    return;
  }
  if (state.type === "create") {
    const id = validateAgentId(state);
    if (!id) { state.input.focus(); return; }
    setModalPending("Creating…");
    vscode.postMessage({ type: "create", id, sourceId: selected });
    return;
  }
  const assets = assetCollection(state.kind);
  const next = assets.filter((item) => state.selection.has(item.name) || ("required" in item && item.required)).map((item) => item.name);
  const current = state.kind === "skill" ? selectedSkills : selectedPlugins;
  const changed = !sameNames(current, next);
  if (state.kind === "skill") selectedSkills = next; else selectedPlugins = next;
  closeModal(false);
  renderAssets();
  if (changed) setDirty();
  state.trigger.focus();
}

tabs.forEach((tab, index) => {
  tab.addEventListener("click", () => activateTab(tab));
  tab.addEventListener("keydown", (event) => {
    let next: number | undefined;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    if (event.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
    if (event.key === "Home") next = 0;
    if (event.key === "End") next = tabs.length - 1;
    if (next !== undefined) { event.preventDefault(); activateTab(tabs[next], true); }
  });
});

agent.addEventListener("change", () => {
  const next = agent.value;
  agent.value = selected;
  const switchAgent = (): void => { setDirty(false); selected = next; vscode.postMessage({ type: "select", id: selected }); };
  if (dirty) openConfirm({ title: "Discard unsaved changes?", description: "Switching Agents reloads its saved configuration.", message: "Your current changes have not been saved and will be lost.", confirm: "Discard & switch", pendingLabel: "Switching…", trigger: agent, onConfirm: switchAgent });
  else { setPagePending(true, "Switching Agent…"); switchAgent(); }
});
byId<HTMLButtonElement>("create").addEventListener("click", openCreateModal);
byId<HTMLButtonElement>("delete").addEventListener("click", () => {
  const current = agents.find((item) => item.id === selected);
  if (!current || current.id === "default") return;
  const unsaved = dirty ? " Unsaved changes on this Agent will also be discarded." : "";
  openConfirm({ title: "Delete Agent?", description: "This action cannot be undone.", message: `Delete “${current.profile}” (${current.id}) and its configuration.${unsaved}`, confirm: "Delete Agent", pendingLabel: "Deleting…", danger: true, trigger: byId<HTMLButtonElement>("delete"), onConfirm: () => vscode.postMessage({ type: "delete", id: selected }) });
});
let terminalRequestId: string | null = null;
let terminalWatchdog: number | undefined;
let terminalFeedbackTimer: number | undefined;

function resetTerminalButton(): void {
  if (terminalWatchdog !== undefined) window.clearTimeout(terminalWatchdog);
  if (terminalFeedbackTimer !== undefined) window.clearTimeout(terminalFeedbackTimer);
  terminalWatchdog = undefined;
  terminalFeedbackTimer = undefined;
  terminalRequestId = null;
  terminal.classList.remove("launching", "opened");
  terminal.removeAttribute("aria-busy");
  terminal.removeAttribute("aria-disabled");
  terminal.setAttribute("aria-label", "Open this Agent in a terminal");
  terminalLabel.textContent = "Open in terminal";
  terminalFeedback.textContent = "";
  terminalFeedback.className = "terminal-feedback sr-only";
}

function showTerminalFailure(message: string): void {
  if (terminalWatchdog !== undefined) window.clearTimeout(terminalWatchdog);
  terminalWatchdog = undefined;
  terminalRequestId = null;
  terminal.classList.remove("launching", "opened");
  terminal.removeAttribute("aria-busy");
  terminal.removeAttribute("aria-disabled");
  terminal.setAttribute("aria-label", "Try opening this Agent in a terminal again");
  terminalLabel.textContent = "Try again";
  terminalFeedback.textContent = message;
  terminalFeedback.className = "terminal-feedback error";
  terminalFeedbackTimer = window.setTimeout(() => {
    if (terminalRequestId === null) resetTerminalButton();
  }, 4_000);
}

terminal.addEventListener("click", () => {
  if (terminalRequestId) return;
  if (terminalFeedbackTimer !== undefined) window.clearTimeout(terminalFeedbackTimer);
  terminalFeedbackTimer = undefined;
  const requestId = `terminal-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  terminalRequestId = requestId;
  terminal.classList.remove("opened");
  terminal.classList.add("launching");
  terminal.setAttribute("aria-busy", "true");
  terminal.setAttribute("aria-disabled", "true");
  terminalLabel.textContent = dirty ? "Opening saved config…" : "Launching shell…";
  terminalFeedback.textContent = dirty ? "Opening the last saved Agent configuration in a terminal." : "Opening terminal.";
  terminalFeedback.className = "terminal-feedback sr-only";
  vscode.postMessage({ type: "terminal", id: selected, requestId });
  terminalWatchdog = window.setTimeout(() => {
    if (terminalRequestId === requestId) showTerminalFailure("The terminal did not open in time.");
  }, 8_000);
});
byId<HTMLButtonElement>("manageSkills").addEventListener("click", () => openAssetModal("skill"));
byId<HTMLButtonElement>("managePlugins").addEventListener("click", () => openAssetModal("plugin"));

byId<HTMLButtonElement>("addCustom").addEventListener("click", () => {
  if (customSkills.length >= 5) return;
  customSkills.push({ name: "", repository: "", ref: "", path: "", names: [] });
  byId<HTMLDetailsElement>("customRepositories").open = true;
  renderCustom();
  setDirty();
  customList.querySelector<HTMLInputElement>(`[data-index="${customSkills.length - 1}"][data-custom="name"]`)?.focus();
});
customList.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement) || target.dataset.remove === undefined) return;
  customSkills.splice(Number(target.dataset.remove), 1);
  renderCustom();
  setDirty();
  byId<HTMLButtonElement>("addCustom").focus();
});
customList.addEventListener("input", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement)) return;
  const index = Number(target.dataset.index);
  const field = target.dataset.custom as SkillSourceField | undefined;
  if (!Number.isInteger(index) || !field || !customSkills[index]) return;
  target.setCustomValidity("");
  if (field === "names") customSkills[index].names = target.value.split(",").map((value) => value.trim()).filter(Boolean);
  else customSkills[index][field] = target.value;
  setDirty();
});
[profile, model, instructions].forEach((input) => input.addEventListener("input", () => {
  if (input === instructions) byId<HTMLElement>("charCount").textContent = `${instructions.value.length} / 16 384`;
  setDirty();
}));

function invalid(input: HTMLInputElement, error: string): false {
  activateTab(byId<HTMLButtonElement>("tab-skills"));
  byId<HTMLDetailsElement>("customRepositories").open = true;
  input.setCustomValidity(error);
  input.focus();
  input.reportValidity();
  statusEl.textContent = error;
  statusEl.className = "status error";
  return false;
}

function validate(): boolean {
  if (!profile.value.trim()) {
    activateTab(byId<HTMLButtonElement>("tab-general"));
    profile.setCustomValidity("Enter a display name.");
    profile.focus();
    profile.reportValidity();
    return false;
  }
  profile.setCustomValidity("");
  const seen = new Set<string>();
  const namePattern = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
  const skillPattern = /^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$/;
  for (let index = 0; index < customSkills.length; index += 1) {
    const source = customSkills[index];
    const input = (field: SkillSourceField): HTMLInputElement => {
      const element = customList.querySelector<HTMLInputElement>(`[data-index="${index}"][data-custom="${field}"]`);
      if (!element) throw new Error(`Missing custom skill field ${field}`);
      return element;
    };
    source.name = source.name.trim();
    source.repository = source.repository.trim();
    source.ref = (source.ref || "").trim();
    source.path = (source.path || "").trim();
    if (!namePattern.test(source.name) || source.name.length > 30) return invalid(input("name"), "Use a unique lowercase name with letters, numbers, or hyphens.");
    if (seen.has(source.name)) return invalid(input("name"), "Source names must be unique.");
    seen.add(source.name);
    let url: URL;
    try { url = new URL(source.repository); } catch { return invalid(input("repository"), "Enter a valid public HTTPS Git URL."); }
    if (url.protocol !== "https:" || !url.host || url.username || url.password || url.search || url.hash) return invalid(input("repository"), "Use HTTPS without credentials, query parameters, or fragments.");
    if (source.path.includes("..")) return invalid(input("path"), "Repository path cannot contain “..”.");
    if ((source.names || []).length > 50) return invalid(input("names"), "Select no more than 50 skill names.");
    if ((source.names || []).some((name) => name.length > 253 || !skillPattern.test(name))) return invalid(input("names"), "Skill names may contain letters, numbers, dots, underscores, and hyphens.");
  }
  return true;
}

byId<HTMLFormElement>("form").addEventListener("submit", (event) => {
  event.preventDefault();
  if (!validate()) return;
  profile.value = profile.value.trim();
  statusEl.textContent = "Applying configuration…";
  statusEl.className = "status";
  save.textContent = "Saving…";
  save.disabled = true;
  setPagePending(true, "Saving changes…");
  vscode.postMessage({ type: "save", id: selected, profile: profile.value, instructions: instructions.value, model: model.value, customSkillSources: customSkills, selectedSkills, selectedPlugins });
});

modalClose.addEventListener("click", () => { if (!modalPending) closeModal(); });
modalCancel.addEventListener("click", () => { if (!modalPending) closeModal(); });
modalConfirm.addEventListener("click", applyModal);
modal.addEventListener("cancel", (event) => { event.preventDefault(); if (!modalPending) closeModal(); });
modal.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && modalState?.type === "create" && event.target === modalState.input && !modalConfirm.disabled) {
    event.preventDefault();
    applyModal();
  }
});

window.addEventListener("message", (event: MessageEvent) => {
  const value = event.data as Record<string, unknown>;
  if (value.type === "config") {
    const hadModal = modal.open;
    setPagePending(false);
    closeModal(false);
    agents = Array.isArray(value.agents) ? value.agents as WebviewAgent[] : [];
    registry = value.registry && typeof value.registry === "object" ? value.registry as WebviewRegistry : { name: "", skills: [], plugins: [] };
    selected = typeof value.selected === "string" ? value.selected : "default";
    render();
    if (hadModal) agent.focus();
  }
  if (value.type === "requestRefresh") {
    openConfirm({ title: "Discard unsaved changes?", description: "Refreshing reloads the configuration from the platform.", message: "Your current changes have not been saved and will be lost.", confirm: "Discard & refresh", pendingLabel: "Refreshing…", trigger: modalTrigger(byId<HTMLButtonElement>("save")), onConfirm: () => vscode.postMessage({ type: "refresh" }) });
  }
  if (value.type === "refreshBlocked" && modalState) {
    modalNotice.textContent = "Finish or cancel this dialog before refreshing.";
    modalNotice.hidden = false;
    modalClose.focus();
  }
  if (value.type === "busy") {
    const message = typeof value.message === "string" ? value.message : "The current Agent action is still in progress.";
    if (modalPending) {
      modalNotice.textContent = message;
      modalNotice.hidden = false;
      modal.focus();
    } else if (!busyLayer.hidden) {
      busyLabel.textContent = message;
    } else {
      statusEl.textContent = message;
      statusEl.className = "status";
    }
  }
  if (value.type === "error") {
    setPagePending(false);
    statusEl.textContent = typeof value.message === "string" ? value.message : "Could not load the Agent configuration.";
    statusEl.className = "status error";
    save.disabled = true;
  }
  if (value.type === "loading") setPagePending(true, typeof value.message === "string" ? value.message : "Refreshing…");
  if (value.type === "refreshFailed") {
    setPagePending(false);
    const message = typeof value.message === "string" ? value.message : "Could not refresh the Agent configuration.";
    if (modalPending) clearModalPending(message);
    save.disabled = !dirty;
    statusEl.textContent = message;
    statusEl.className = "status error";
  }
  if (value.type === "saveFailed") {
    setPagePending(false);
    setDirty(true);
    statusEl.textContent = typeof value.message === "string" ? value.message : "Could not save changes · try again";
    statusEl.className = "status error";
  }
  if (value.type === "actionFailed") {
    setPagePending(false);
    const message = typeof value.message === "string" ? value.message : "The action could not be completed.";
    if (modalPending) clearModalPending(message);
    statusEl.textContent = message;
    statusEl.className = "status error";
  }
  if (value.type === "terminalOpened" && value.requestId === terminalRequestId) {
    if (value.id !== selected) { resetTerminalButton(); return; }
    const requestId = terminalRequestId;
    if (terminalWatchdog !== undefined) window.clearTimeout(terminalWatchdog);
    terminalWatchdog = undefined;
    terminal.classList.remove("launching");
    terminal.classList.add("opened");
    terminal.removeAttribute("aria-busy");
    terminalLabel.textContent = "Terminal opened";
    terminalFeedback.textContent = "Terminal opened.";
    terminalFeedbackTimer = window.setTimeout(() => {
      if (terminalRequestId === requestId) resetTerminalButton();
    }, 1_400);
  }
  if (value.type === "terminalFailed" && value.requestId === terminalRequestId) {
    if (value.id !== undefined && value.id !== selected) { resetTerminalButton(); return; }
    showTerminalFailure(typeof value.message === "string" ? value.message : "Could not open the terminal.");
  }
});
window.addEventListener("beforeunload", (event) => { if (dirty) event.preventDefault(); });
setPagePending(true, "Loading Agents…");
vscode.postMessage({ type: "ready" });
