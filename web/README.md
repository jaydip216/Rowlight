# db0 browser frontend

The Phase 1–2 frontend is a framework-free TypeScript/Vite application. It uses CodeMirror 6 for MySQL syntax and a fixed-row virtual result grid, so DOM size remains bounded as rows arrive.

## Run locally

```sh
npm install
npm run dev
```

Vite proxies `/api` to `http://127.0.0.1:7070`. `npm run build` writes static assets to `dist/` for the Go backend to embed.

The backend launches the page with `#token=<random-token>`. The frontend reads the token once, immediately removes the fragment from browser history, and sends it as `Authorization: Bearer <token>` on every API request. The token never appears in an HTTP request URL.

## Backend API contract

All endpoints return JSON errors as `{ "error": "…" }` (the frontend also accepts `message` for compatibility). Database identifiers are URL-encoded path segments. Secrets are accepted only in request bodies and must never be logged.

### Connections

- `POST /api/connections` accepts a connection input and returns `{ "id", "serverVersion", "database" }`.
- `DELETE /api/connections/{id}` closes the connection and may return `204 No Content`.

The Test connection action uses the same create endpoint and immediately deletes the returned connection, so the backend needs no separate test route.

### Profiles and history

- `GET /api/profiles` lists nonsecret saved connection profiles.
- `POST /api/profiles` creates or updates a profile. Password and client-key fields are rejected.
- `DELETE /api/profiles/{id}` removes a profile.
- `GET /api/history` lists at most 100 recent query executions, newest first.
- `DELETE /api/history` clears query history.

Connection input:

```json
{
  "host": "127.0.0.1",
  "port": 3306,
  "user": "reader",
  "password": "secret",
  "database": "inventory",
  "tls": {
    "mode": "system",
    "serverName": "db.example.com",
    "caPem": "optional PEM",
    "clientCertPem": "optional PEM",
    "clientKeyPem": "optional PEM"
  }
}
```

TLS mode is one of `disabled`, `system`, `custom`, or `mutual`. Custom mode requires `caPem`; mutual mode also requires the client certificate and key.

### Lazy schema

- `GET /api/connections/{id}/schemas` → `{ "schemas": ["inventory"] }`
- `GET /api/connections/{id}/schemas/{schema}/tables` → `{ "tables": [{ "name": "products", "type": "table" }] }`
- `GET /api/connections/{id}/schemas/{schema}/tables/{table}/columns` → `{ "columns": [{ "name": "id", "dataType": "bigint", "columnType": "bigint unsigned", "nullable": false, "key": "PRI", "default": null, "extra": "" }] }`

### Query stream and cancellation

`POST /api/connections/{id}/queries` accepts `{ "sql", "maxRows"?, "recordHistory"? }` and responds with `Content-Type: application/x-ndjson`. Each line is one event. The first event is flushed immediately so cancellation works while database execution is still pending:

```jsonl
{"type":"started","queryId":"q_123"}
{"type":"meta","queryId":"q_123","columns":[{"name":"id","databaseType":"BIGINT","nullable":false}]}
{"type":"rows","rows":[["9223372036854775807"],[null]]}
{"type":"complete","rowCount":2,"elapsedMs":12,"truncated":false}
```

The terminal event is exactly one of:

- `{ "type": "complete", "rowCount", "elapsedMs", "truncated" }`
- `{ "type": "cancelled", "rowCount", "elapsedMs" }`
- `{ "type": "error", "error": "…" }`

Rows are positional arrays. `BIGINT` and `DECIMAL` values must be encoded as strings, and SQL `NULL` as JSON `null`. The backend limits delivered rows/bytes and reports `truncated: true` when it stops at the limit.

`DELETE /api/queries/{queryId}` requests cancellation and may return `204 No Content`. Closing the streaming request must also cancel backend work.

## Current Phase 1–2 boundary

Implemented: direct connection form with TLS inputs, persistent nonsecret profiles, connection test, database/table/column browsing, one-click bounded table previews, schema filtering, SQL editing and completion from loaded names, run selection or full editor with `Cmd/Ctrl+Enter`, streaming results, bounded DOM rendering, cancellation, capped/optional query history, loaded-result TSV copy/CSV export, and visible errors/status.

Deferred: result editing, full cell inspector, server-side filtering/paging, Keychain password storage, and write sessions.
