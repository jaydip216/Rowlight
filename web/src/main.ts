import "./styles.css";
import { api } from "./api";
import { createEditor, selectedQuery, setQuery } from "./editor";
import { ResultGrid } from "./grid";
import type { ColumnItem, Connection, ConnectionInput, ConnectionProfile, QueryEvent, QueryHistoryEntry, TableItem, TLSMode } from "./types";

const app = document.querySelector<HTMLDivElement>("#app")!;
app.innerHTML = `
  <header class="app-header">
    <div class="brand"><span class="brand-mark">d0</span><span>db0</span></div>
    <div class="connection-state"><span class="status-dot"></span><span id="connection-label">Not connected</span></div>
    <button id="disconnect" class="button ghost hidden">Disconnect</button>
  </header>
  <main>
    <section id="connect-view" class="connect-view">
      <form id="connection-form" class="connection-card">
        <div class="eyebrow">NEW CONNECTION</div>
        <h1>Connect to MySQL</h1>
        <p class="muted">Connections are read-only by default. Credentials stay in backend memory.</p>
        <div class="profile-picker">
          <select id="saved-profile" aria-label="Saved connection profile"><option value="">New connection</option></select>
          <button id="delete-profile" type="button" class="button secondary" disabled>Delete</button>
        </div>
        <div class="form-grid">
          <label class="wide">Host<input name="host" value="127.0.0.1" required autocomplete="off"></label>
          <label>Port<input name="port" type="number" value="3306" min="1" max="65535" required></label>
          <label>Database<input name="database" placeholder="Optional" autocomplete="off"></label>
          <label>Username<input name="user" required autocomplete="username"></label>
          <label>Password<input name="password" type="password" autocomplete="current-password"></label>
          <label class="wide">TLS
            <select name="tlsMode">
              <option value="system" selected>Verify with system certificates</option>
              <option value="custom">Verify with custom CA</option>
              <option value="mutual">Mutual TLS</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
          <div id="tls-fields" class="wide tls-fields hidden">
            <label>Server name<input name="serverName" placeholder="db.example.com"></label>
            <label class="wide">CA certificate (PEM)<textarea name="caPem" rows="3" spellcheck="false"></textarea></label>
            <label class="mutual-field hidden">Client certificate (PEM)<textarea name="clientCertPem" rows="3" spellcheck="false"></textarea></label>
            <label class="mutual-field hidden">Client key (PEM)<textarea name="clientKeyPem" rows="3" spellcheck="false"></textarea></label>
          </div>
        </div>
        <label class="profile-save"><input name="saveProfile" type="checkbox"><span>Save nonsecret settings as</span><input name="profileName" placeholder="Profile name"></label>
        <div id="connection-error" class="alert error hidden" role="alert"></div>
        <div class="form-actions">
          <span id="test-result" class="muted"></span>
          <button id="test-connection" type="button" class="button secondary">Test connection</button>
          <button id="connect" class="button primary">Connect</button>
        </div>
      </form>
    </section>
    <section id="workspace" class="workspace hidden">
      <aside class="sidebar">
        <div class="sidebar-header"><span>DATABASES</span><button id="refresh-schema" class="icon-button" title="Refresh schema">↻</button></div>
        <input id="schema-filter" class="schema-filter" placeholder="Filter objects…" aria-label="Filter database objects">
        <div id="schema-tree" class="schema-tree"></div>
      </aside>
      <section class="workbench">
        <div class="query-toolbar">
          <div class="toolbar-context"><span><strong id="active-database">No database selected</strong><span class="readonly-pill">READ ONLY</span></span><select id="query-history" class="history-select" aria-label="Recent queries"><option value="">Recent queries</option></select><button id="clear-history" class="history-clear" title="Clear query history">Clear</button></div>
          <div class="toolbar-actions">
            <span class="shortcut">⌘↵</span>
            <button id="cancel-query" class="button danger hidden">Cancel</button>
            <button id="run-query" class="button primary">▶ Run</button>
          </div>
        </div>
        <div id="editor" class="editor"></div>
        <div class="result-bar">
          <div class="result-tabs"><button class="result-tab active">Results</button></div>
          <div class="result-meta">
            <button id="copy-results" class="result-action" disabled>Copy TSV</button>
            <button id="export-results" class="result-action" disabled>Export CSV</button>
            <div id="query-status" class="query-status">Ready</div>
          </div>
        </div>
        <div id="query-error" class="query-error hidden" role="alert"></div>
        <div id="results" class="results" role="grid"></div>
      </section>
    </section>
  </main>`;

const $ = <T extends HTMLElement>(selector: string): T => document.querySelector<T>(selector)!;
const connectView = $("#connect-view");
const workspace = $("#workspace");
const form = $("#connection-form") as HTMLFormElement;
const connectionError = $("#connection-error");
const testResult = $("#test-result");
const tree = $("#schema-tree");
const queryError = $("#query-error");
const status = $("#query-status");
const runButton = $("#run-query") as HTMLButtonElement;
const cancelButton = $("#cancel-query") as HTMLButtonElement;
const copyResultsButton = $("#copy-results") as HTMLButtonElement;
const exportResultsButton = $("#export-results") as HTMLButtonElement;
const profileSelect = $("#saved-profile") as HTMLSelectElement;
const deleteProfileButton = $("#delete-profile") as HTMLButtonElement;
const historySelect = $("#query-history") as HTMLSelectElement;
const grid = new ResultGrid($("#results"));

let connection: Connection | null = null;
let activeDatabase = "";
let queryId: string | undefined;
let queryController: AbortController | undefined;
const schemaNames = new Set<string>();
const tableCache = new Map<string, TableItem[]>();
const columnCache = new Map<string, ColumnItem[]>();
let profiles: ConnectionProfile[] = [];
let historyEntries: QueryHistoryEntry[] = [];

const editor = createEditor($("#editor"), () => void runQuery(), () => [...schemaNames]);

function connectionInput(): ConnectionInput {
  const data = new FormData(form);
  const tlsMode = String(data.get("tlsMode")) as TLSMode;
  return {
    host: String(data.get("host")), port: Number(data.get("port")), user: String(data.get("user")),
    password: String(data.get("password")), database: String(data.get("database")),
    tls: {
      mode: tlsMode, serverName: String(data.get("serverName") || "") || undefined,
      caPem: String(data.get("caPem") || "") || undefined,
      clientCertPem: String(data.get("clientCertPem") || "") || undefined,
      clientKeyPem: String(data.get("clientKeyPem") || "") || undefined,
    },
  };
}

async function loadProfiles(selectedID = ""): Promise<void> {
  const result = await api.profiles();
  profiles = result.profiles;
  profileSelect.replaceChildren(new Option("New connection", ""), ...profiles.map((profile) => new Option(profile.name, profile.id)));
  profileSelect.value = selectedID;
  deleteProfileButton.disabled = !profileSelect.value;
}

function applyProfile(profile: ConnectionProfile): void {
  setFormValue("host", profile.host);
  setFormValue("port", String(profile.port));
  setFormValue("user", profile.user);
  setFormValue("password", "");
  setFormValue("database", profile.database);
  setFormValue("tlsMode", profile.tls.mode);
  setFormValue("serverName", profile.tls.serverName ?? "");
  setFormValue("caPem", profile.tls.caPem ?? "");
  setFormValue("clientCertPem", profile.tls.clientCertPem ?? "");
  setFormValue("clientKeyPem", "");
  setFormValue("profileName", profile.name);
  (form.elements.namedItem("saveProfile") as HTMLInputElement).checked = false;
  $("select[name=tlsMode]").dispatchEvent(new Event("change"));
}

function setFormValue(name: string, value: string): void {
  const field = form.elements.namedItem(name) as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null;
  if (field) field.value = value;
}

async function saveSelectedProfile(input: ConnectionInput): Promise<void> {
  const shouldSave = (form.elements.namedItem("saveProfile") as HTMLInputElement).checked;
  if (!shouldSave) return;
  const name = String(new FormData(form).get("profileName") ?? "").trim();
  if (!name) throw new Error("Enter a profile name or turn off Save profile.");
  const existing = profiles.find((profile) => profile.id === profileSelect.value);
  const saved = await api.saveProfile({
    id: existing?.id,
    name,
    host: input.host,
    port: input.port,
    user: input.user,
    database: input.database,
    tls: {
      mode: input.tls.mode,
      serverName: input.tls.serverName,
      caPem: input.tls.caPem,
      clientCertPem: input.tls.clientCertPem,
    },
  });
  await loadProfiles(saved.id);
}

async function loadHistory(): Promise<void> {
  const result = await api.history();
  historyEntries = result.history;
  historySelect.replaceChildren(new Option("Recent queries", ""), ...historyEntries.map((entry) => {
    const summary = entry.sql.replace(/\s+/g, " ").trim();
    const label = `${new Date(entry.executedAt).toLocaleTimeString()} · ${summary.slice(0, 70)}`;
    return new Option(label, entry.id);
  }));
}

function setBusy(button: HTMLButtonElement, busy: boolean, busyText: string): void {
  button.disabled = busy;
  button.dataset.label ??= button.textContent ?? "";
  button.textContent = busy ? busyText : button.dataset.label;
}

function showError(element: HTMLElement, error: unknown): void {
  element.textContent = error instanceof Error ? error.message : "Unexpected error";
  element.classList.remove("hidden");
}

async function connect(): Promise<void> {
  const button = $("#connect") as HTMLButtonElement;
  connectionError.classList.add("hidden");
  setBusy(button, true, "Connecting…");
  try {
    const input = connectionInput();
    connection = await api.connect(input);
    try { await saveSelectedProfile(input); }
    catch (error) { showError(connectionError, error); }
    activeDatabase = connection.database;
    $("#connection-label").textContent = `${connection.serverVersion} · read only`;
    $(".status-dot").classList.add("online");
    $("#disconnect").classList.remove("hidden");
    connectView.classList.add("hidden");
    workspace.classList.remove("hidden");
    updateActiveDatabase();
    await loadDatabases();
    await loadHistory();
  } catch (error) { showError(connectionError, error); }
  finally { setBusy(button, false, ""); }
}

async function loadDatabases(): Promise<void> {
  if (!connection) return;
  tree.innerHTML = `<div class="tree-loading">Loading schema…</div>`;
  try {
    const { items } = await api.databases(connection.id);
    schemaNames.clear();
    items.forEach(({ name }) => schemaNames.add(name));
    tree.replaceChildren(...items.map(({ name }) => databaseNode(name)));
    if (!activeDatabase && items[0]) {
      activeDatabase = items[0].name;
      updateActiveDatabase();
    }
  } catch (error) { tree.innerHTML = `<div class="tree-error"></div>`; showError(tree.firstElementChild as HTMLElement, error); }
}

function databaseNode(database: string): HTMLElement {
  const details = document.createElement("details");
  details.className = "tree-database";
  details.dataset.search = database.toLowerCase();
  const summary = document.createElement("summary");
  summary.innerHTML = `<span class="db-icon">◫</span><span></span>`;
  summary.lastElementChild!.textContent = database;
  summary.addEventListener("click", () => { activeDatabase = database; updateActiveDatabase(); });
  details.append(summary);
  details.addEventListener("toggle", () => { if (details.open) void loadTables(details, database); });
  return details;
}

async function loadTables(container: HTMLDetailsElement, database: string): Promise<void> {
  if (!connection || container.dataset.loaded) return;
  const loading = document.createElement("div"); loading.className = "tree-loading"; loading.textContent = "Loading…";
  container.append(loading);
  try {
    const { items } = await api.tables(connection.id, database);
    tableCache.set(database, items);
    items.forEach(({ name }) => schemaNames.add(`${database}.${name}`));
    loading.remove();
    container.append(...items.map((item) => tableNode(database, item)));
    container.dataset.loaded = "true";
  } catch (error) { showError(loading, error); }
}

function tableNode(database: string, table: TableItem): HTMLElement {
  const details = document.createElement("details");
  details.className = "tree-table"; details.dataset.search = `${database} ${table.name}`.toLowerCase();
  const summary = document.createElement("summary");
  summary.innerHTML = `<span class="table-icon">▦</span><span></span><small></small><button type="button" class="tree-browse" title="Browse the first 200 rows">Browse</button>`;
  summary.children[1]!.textContent = table.name; summary.children[2]!.textContent = table.type;
  const browse = summary.querySelector<HTMLButtonElement>(".tree-browse")!;
  browse.addEventListener("click", (event) => {
    event.preventDefault();
    event.stopPropagation();
    activeDatabase = database;
    updateActiveDatabase();
    setQuery(editor, `SELECT *\nFROM ${quoteIdentifier(database)}.${quoteIdentifier(table.name)}\nLIMIT 200;`);
    void runQuery();
  });
  details.append(summary);
  details.addEventListener("toggle", () => { if (details.open) void loadColumns(details, database, table.name); });
  return details;
}

async function loadColumns(container: HTMLDetailsElement, database: string, table: string): Promise<void> {
  if (!connection || container.dataset.loaded) return;
  const loading = document.createElement("div"); loading.className = "tree-loading"; loading.textContent = "Loading…";
  container.append(loading);
  try {
    const { items } = await api.columns(connection.id, database, table);
    columnCache.set(`${database}.${table}`, items);
    items.forEach(({ name }) => schemaNames.add(name));
    loading.remove();
    for (const column of items) {
      const row = document.createElement("div"); row.className = "tree-column";
      row.innerHTML = `<span>⌁</span><span></span><small></small>`;
      row.children[1]!.textContent = column.name; row.children[2]!.textContent = column.dataType;
      container.append(row);
    }
    container.dataset.loaded = "true";
  } catch (error) { showError(loading, error); }
}

function updateActiveDatabase(): void {
  $("#active-database").textContent = activeDatabase || "No database selected";
}

function quoteIdentifier(value: string): string {
  return `\`${value.replaceAll("`", "``")}\``;
}

async function runQuery(): Promise<void> {
  if (!connection || queryController) return;
  const sql = selectedQuery(editor);
  if (!sql) { showError(queryError, new Error("Enter a query to run.")); return; }
  queryError.classList.add("hidden"); grid.reset(); updateResultActions();
  queryController = new AbortController(); queryId = undefined;
  runButton.classList.add("hidden"); cancelButton.classList.remove("hidden"); status.textContent = "Running…";
  const started = performance.now();
  const onEvent = (event: QueryEvent) => {
    if (event.type === "started") queryId = event.queryId;
    if (event.type === "meta") { queryId = event.queryId; grid.setColumns(event.columns); }
    if (event.type === "rows") { grid.append(event.rows); updateResultActions(); }
    if (event.type === "complete") { status.textContent = `${event.rowCount.toLocaleString()} rows · ${event.elapsedMs} ms${event.truncated ? " · LIMIT REACHED" : ""}`; window.setTimeout(() => void loadHistory(), 0); }
    if (event.type === "cancelled") status.textContent = `Cancelled · ${event.rowCount.toLocaleString()} rows · ${event.elapsedMs} ms`;
    if (event.type === "error") { showError(queryError, new Error(event.error)); status.textContent = "Query failed"; }
  };
  try {
    const headerQueryId = await api.query(connection.id, sql, queryController.signal, onEvent);
    queryId ??= headerQueryId;
    if (status.textContent === "Running…") status.textContent = `${grid.rowCount.toLocaleString()} rows · ${Math.round(performance.now() - started)} ms`;
  } catch (error) {
    if ((error as DOMException).name !== "AbortError") { showError(queryError, error); status.textContent = "Query failed"; }
  } finally {
    queryController = undefined; queryId = undefined;
    runButton.classList.remove("hidden"); cancelButton.classList.add("hidden");
  }
}

function updateResultActions(): void {
  const empty = grid.rowCount === 0;
  copyResultsButton.disabled = empty;
  exportResultsButton.disabled = empty;
}

async function copyResults(): Promise<void> {
  try {
    await navigator.clipboard.writeText(grid.toTSV());
    status.textContent = `${grid.rowCount.toLocaleString()} loaded rows copied as TSV`;
  } catch (error) {
    showError(queryError, error);
  }
}

function exportResults(): void {
  const blob = new Blob([grid.toCSV()], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  const suffix = new Date().toISOString().replaceAll(":", "-").replace("T", "_").slice(0, 19);
  link.href = url;
  link.download = `db0-results_${suffix}.csv`;
  document.body.append(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
  status.textContent = `${grid.rowCount.toLocaleString()} loaded rows exported`;
}

async function cancelQuery(): Promise<void> {
  cancelButton.disabled = true; status.textContent = "Cancelling…";
  try { if (queryId) await api.cancel(queryId); queryController?.abort(); }
  catch (error) { showError(queryError, error); }
  finally { cancelButton.disabled = false; }
}

form.addEventListener("submit", (event) => { event.preventDefault(); void connect(); });
profileSelect.addEventListener("change", () => {
  const profile = profiles.find((item) => item.id === profileSelect.value);
  deleteProfileButton.disabled = !profile;
  if (profile) applyProfile(profile);
});
deleteProfileButton.addEventListener("click", async () => {
  if (!profileSelect.value) return;
  try { await api.deleteProfile(profileSelect.value); await loadProfiles(); }
  catch (error) { showError(connectionError, error); }
});
$("#test-connection").addEventListener("click", async () => {
  const button = $("#test-connection") as HTMLButtonElement; connectionError.classList.add("hidden"); testResult.textContent = "";
  setBusy(button, true, "Testing…");
  try { const value = await api.testConnection(connectionInput()); testResult.textContent = `Connected · ${value.serverVersion}`; }
  catch (error) { showError(connectionError, error); }
  finally { setBusy(button, false, ""); }
});
$("select[name=tlsMode]").addEventListener("change", (event) => {
  const mode = (event.target as HTMLSelectElement).value;
  $("#tls-fields").classList.toggle("hidden", mode !== "custom" && mode !== "mutual");
  document.querySelectorAll(".mutual-field").forEach((item) => item.classList.toggle("hidden", mode !== "mutual"));
});
$("#disconnect").addEventListener("click", async () => {
  if (connection) await api.disconnect(connection.id).catch(() => undefined);
  connection = null; tableCache.clear(); columnCache.clear(); schemaNames.clear();
  workspace.classList.add("hidden"); connectView.classList.remove("hidden"); $("#disconnect").classList.add("hidden");
  $("#connection-label").textContent = "Not connected"; $(".status-dot").classList.remove("online");
});
$("#refresh-schema").addEventListener("click", () => { tableCache.clear(); columnCache.clear(); void loadDatabases(); });
$("#schema-filter").addEventListener("input", (event) => {
  const value = (event.target as HTMLInputElement).value.trim().toLowerCase();
  tree.querySelectorAll<HTMLElement>("[data-search]").forEach((node) => node.classList.toggle("filtered", !node.dataset.search?.includes(value)));
});
runButton.addEventListener("click", () => void runQuery());
cancelButton.addEventListener("click", () => void cancelQuery());
copyResultsButton.addEventListener("click", () => void copyResults());
exportResultsButton.addEventListener("click", exportResults);
historySelect.addEventListener("change", () => {
  const entry = historyEntries.find((item) => item.id === historySelect.value);
  if (entry) setQuery(editor, entry.sql);
  historySelect.value = "";
});
$("#clear-history").addEventListener("click", async () => {
  try { await api.clearHistory(); await loadHistory(); }
  catch (error) { showError(queryError, error); }
});

void loadProfiles().catch((error) => showError(connectionError, error));
