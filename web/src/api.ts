import type {
  ColumnItem,
  Connection,
  ConnectionInput,
  ConnectionProfile,
  DatabaseItem,
  QueryEvent,
  QueryHistoryEntry,
  TableItem,
} from "./types";

const launchToken = (() => {
  const hash = new URLSearchParams(location.hash.replace(/^#/, ""));
  const tokenFromHash = hash.get("token");
  if (tokenFromHash) sessionStorage.setItem("db0-launch-token", tokenFromHash);
  const token = tokenFromHash ?? sessionStorage.getItem("db0-launch-token") ?? "";
  if (tokenFromHash) history.replaceState(null, document.title, `${location.pathname}${location.search}`);
  return token;
})();

function apiHeaders(headers?: HeadersInit): Headers {
  const result = new Headers(headers);
  result.set("Content-Type", "application/json");
  if (launchToken) result.set("Authorization", `Bearer ${launchToken}`);
  return result;
}

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    ...init,
    headers: apiHeaders(init?.headers),
  });
  if (!response.ok) throw await responseError(response);
  if (response.status === 204 || response.headers.get("Content-Length") === "0") return undefined as T;
  return response.json() as Promise<T>;
}

async function responseError(response: Response): Promise<ApiError> {
  let message = `${response.status} ${response.statusText}`;
  try {
    const value = (await response.json()) as { error?: string; message?: string };
    if (value.error || value.message) message = value.error ?? value.message ?? message;
  } catch {
    // Keep the HTTP status when the response is not JSON.
  }
  return new ApiError(message, response.status);
}

const segment = (value: string) => encodeURIComponent(value);

export const api = {
  profiles(): Promise<{ profiles: ConnectionProfile[] }> {
    return request("/api/profiles");
  },

  saveProfile(input: Omit<ConnectionProfile, "id" | "updatedAt"> & { id?: string }): Promise<ConnectionProfile> {
    return request("/api/profiles", { method: "POST", body: JSON.stringify(input) });
  },

  deleteProfile(id: string): Promise<void> {
    return request(`/api/profiles/${segment(id)}`, { method: "DELETE" });
  },

  history(): Promise<{ history: QueryHistoryEntry[] }> {
    return request("/api/history");
  },

  clearHistory(): Promise<void> {
    return request("/api/history", { method: "DELETE" });
  },

  async testConnection(input: ConnectionInput): Promise<{ serverVersion: string }> {
    const connection = await request<Connection>("/api/connections", { method: "POST", body: JSON.stringify(input) });
    try { return { serverVersion: connection.serverVersion }; }
    finally { await this.disconnect(connection.id); }
  },

  connect(input: ConnectionInput): Promise<Connection> {
    return request("/api/connections", { method: "POST", body: JSON.stringify(input) });
  },

  disconnect(id: string): Promise<void> {
    return request(`/api/connections/${segment(id)}`, { method: "DELETE" });
  },

  databases(id: string): Promise<{ items: DatabaseItem[] }> {
    return request<{ schemas: string[] }>(`/api/connections/${segment(id)}/schemas`)
      .then(({ schemas }) => ({ items: schemas.map((name) => ({ name })) }));
  },

  tables(id: string, database: string): Promise<{ items: TableItem[] }> {
    return request<{ tables: TableItem[] }>(`/api/connections/${segment(id)}/schemas/${segment(database)}/tables`)
      .then(({ tables }) => ({ items: tables }));
  },

  columns(id: string, database: string, table: string): Promise<{ items: ColumnItem[] }> {
    return request<{ columns: ColumnItem[] }>(
      `/api/connections/${segment(id)}/schemas/${segment(database)}/tables/${segment(table)}/columns`,
    ).then(({ columns }) => ({ items: columns }));
  },

  async query(
    connectionId: string,
    sql: string,
    signal: AbortSignal,
    onEvent: (event: QueryEvent) => void,
  ): Promise<string | undefined> {
    const response = await fetch(`/api/connections/${segment(connectionId)}/queries`, {
      method: "POST",
      headers: apiHeaders({ Accept: "application/x-ndjson" }),
      body: JSON.stringify({ sql }),
      signal,
    });
    if (!response.ok) throw await responseError(response);
    if (!response.body) throw new ApiError("The server returned an empty query stream.", 502);

    const queryId = response.headers.get("X-DB0-Query-ID") ?? undefined;
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffered = "";
    while (true) {
      const { value, done } = await reader.read();
      buffered += value ?? "";
      const lines = buffered.split("\n");
      buffered = lines.pop() ?? "";
      for (const line of lines) {
        if (line.trim()) onEvent(JSON.parse(line) as QueryEvent);
      }
      if (done) break;
    }
    if (buffered.trim()) onEvent(JSON.parse(buffered) as QueryEvent);
    return queryId;
  },

  cancel(queryId: string): Promise<void> {
    return request(`/api/queries/${segment(queryId)}`, { method: "DELETE" });
  },
};
