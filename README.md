# db0

db0 is an early, lightweight, read-only MySQL/MariaDB client. One Go process serves an embedded browser interface on loopback, opens a tab, and owns the database connections.

This repository currently contains the Phase 1–2 vertical slice. It is suitable for local testing, not yet a stable release.

## Current features

- Direct MySQL/MariaDB connections, with verified system, custom-CA, or mutual TLS.
- Lazy database, table, and column browsing.
- One-click browsing for the first 200 table or view rows.
- MySQL-aware CodeMirror editor with schema-name completion.
- One-statement read-only query policy and read-only database transactions.
- Streaming, bounded results with a virtualized result grid.
- Copy loaded results as TSV and export them as CSV.
- Query cancellation and automatic cleanup when the browser request closes.
- Loopback-only HTTP server with a random per-launch bearer token.

The SQL policy is intentionally conservative. v0.1 accepts `SELECT`, `SHOW`, `DESCRIBE`, `DESC`, and `EXPLAIN`, while rejecting multiple statements and potentially mutating constructs. CTEs are deferred. Use a database account with only the permissions you intend to grant: the UI policy is an accident-prevention layer, not an authorization boundary for hostile SQL.

## Build and run

Requirements for development are Go 1.24+ and Node.js 20+.

```sh
make build
./bin/db0
```

The command listens on a random `127.0.0.1` port and opens the authenticated URL in the default browser. Use `./bin/db0 -no-open` to print the URL without opening it, or `-port 7070` to request a fixed loopback port.

End users only need the built `db0` executable. Frontend assets are embedded into it.

## Development

```sh
make test
make dev-backend
```

For Vite hot reload, run `npm run dev` inside `web/` and run the backend on port 7070. The development server proxies `/api` to that port. Open the authenticated backend URL once to obtain the per-launch token; the production build stores it in browser session memory after removing it from the URL fragment.

## Limits in this build

- Query previews are capped at 1,000 rows and approximately 8 MiB.
- Individual cell previews are capped at 1 MiB and marked as truncated in transport.
- Up to two user queries execute concurrently across the process.
- Saved profiles, Keychain storage, writable sessions, SSH tunnel management, and additional database engines are deferred.
- Client cancellation is implemented. Whether a cancelled statement disappears immediately on the server still needs verification against the supported MySQL and MariaDB matrix.

See [PLAN.md](./PLAN.md) for the implementation roadmap and validation gates.
