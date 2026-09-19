# Rowlight browser frontend

The frontend is a framework-free TypeScript/Vite application. It switches CodeMirror 6 between MySQL and PostgreSQL syntax and uses a fixed-row virtual result grid, so DOM size remains bounded as rows arrive.

## Run locally

```sh
npm install
npm run dev
```

Vite proxies `/api` to `http://127.0.0.1:7070`. `npm run build` writes static assets to `dist/` for the Go backend to embed.

The backend launches the page with `#token=<random-token>`. The frontend reads the token once, immediately removes the fragment from browser history, and sends it as `Authorization: Bearer <token>` on every API request. The token never appears in an HTTP request URL.

## Backend API contract

All endpoints return JSON errors with an `error` string. Recoverable failures also include stable `code` and `retryable` fields, for example `{ "error": "…", "code": "connection_unavailable", "retryable": true }`. The frontend also accepts `message` for compatibility. Database identifiers are URL-encoded path segments. Secrets are accepted only in request bodies and must never be logged.

### Connections

- `POST /api/connections` accepts a connection input and returns `{ "id", "engine", "serverVersion", "database" }`.
- `GET /api/connections/{id}` returns the same connection identity so a browser refresh can restore the active engine and session.
- `GET /api/connections/{id}/health` pings the database and returns `{ "status": "healthy", "latencyMs": 2 }`.
- `POST /api/connections/{id}/reconnect` asks the existing pool to establish a usable connection and returns the connection identity.
- `DELETE /api/connections/{id}` closes the connection and may return `204 No Content`.

Metadata and table-browse operations retry exactly once in the backend after a recognized transport failure. Arbitrary SQL is never replayed. The UI preserves the editor and exposes Reconnect so the user can decide whether to run it again.

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
  "engine": "postgres",
  "host": "127.0.0.1",
  "port": 5432,
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

Engine is `mysql` or `postgres`; a missing engine is treated as `mysql` for profiles created by older builds. Default ports are 3306 and 5432 respectively. TLS mode is one of `disabled`, `system`, `custom`, or `mutual`. Custom mode requires `caPem`; mutual mode also requires the client certificate and key.

### Lazy schema

- `GET /api/connections/{id}/schemas` → `{ "schemas": ["inventory"] }`
- `GET /api/connections/{id}/schemas/{schema}/tables` → `{ "tables": [{ "name": "products", "type": "table" }] }`
- `GET /api/connections/{id}/schemas/{schema}/tables/{table}/columns` → `{ "columns": [{ "name": "id", "dataType": "bigint", "columnType": "bigint unsigned", "nullable": false, "key": "PRI", "default": null, "extra": "" }] }`

For MySQL/MariaDB, the schema list represents databases. For PostgreSQL, it represents non-system schemas inside the connected database.

### Table browsing

`POST /api/connections/{id}/browse` returns one bounded page from a table or view. Identifiers are accepted only after they match columns loaded from `information_schema`; values remain query parameters.

```json
{
  "schema": "inventory",
  "table": "products",
  "offset": 0,
  "pageSize": 100,
  "sort": { "column": "id", "direction": "asc" },
  "filters": [{ "column": "name", "operator": "contains", "value": "desk" }]
}
```

Supported filter operators are `equals`, `contains`, `startsWith`, `isNull`, and `isNotNull`. The response contains `{ "columns", "rows", "offset", "pageSize", "hasMore" }`. Page size defaults to 100 and cannot exceed 200. Offset paging without an explicit sort has database-defined row order.

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
- `{ "type": "error", "error": "…", "code"?: "connection_unavailable", "retryable"?: true }`

Rows are positional arrays. `BIGINT` and `DECIMAL` values must be encoded as strings, and SQL `NULL` as JSON `null`. The backend limits delivered rows/bytes and reports `truncated: true` when it stops at the limit.

`DELETE /api/queries/{queryId}` requests cancellation and may return `204 No Content`. Closing the streaming request must also cancel backend work.

## Current Phase 1–2 boundary

Implemented: PostgreSQL/MySQL/MariaDB connection selection with TLS inputs, persistent nonsecret profiles, connection test, database/schema/table/column browsing, paged table previews with one-column filtering and sorting, schema filtering, engine-aware SQL editing and completion, run selection or full editor with `Cmd/Ctrl+Enter`, streaming results, bounded DOM rendering, cancellation, capped/optional query history, loaded-result TSV copy/CSV export, and visible errors/status.

Deferred: result editing, full cell inspector, compound filter UI, keyset pagination, Keychain password storage, and write sessions.
