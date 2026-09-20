# Rowlight

A lightweight, read-only SQL client for PostgreSQL, MySQL, and MariaDB. Rowlight runs as a single Go process, opens its interface in your existing browser, and keeps database connections in the local backend.

> **Project status:** Alpha. The core browse-and-query workflow is usable, but packaging and broader compatibility testing are still in progress.

## Why Rowlight?

Many database clients bundle administration, modeling, migration, and collaboration features into a large desktop application. Rowlight focuses on a smaller workflow:

1. Connect directly to a database.
2. Browse schemas, tables, views, and columns.
3. Run read-only SQL.
4. Inspect, copy, or export bounded results.

The release binary is currently about 11 MB and the backend measured approximately 13 MiB idle RSS on the development Mac. These are development measurements, not compatibility guarantees across systems.

## Features

- PostgreSQL, MySQL, and MariaDB connections
- System certificate, custom CA, mutual TLS, and explicitly disabled TLS modes
- Read-only transactions and a conservative, engine-aware SQL policy
- Persistent nonsecret connection profiles
- Lazy schema, table, view, and column browsing
- Server-side table filtering, sorting, and pagination
- MySQL- and PostgreSQL-aware CodeMirror editor
- Streaming, bounded query results with a virtualized grid
- Query cancellation
- Connection health checks, stale-session detection, and explicit reconnect
- One automatic retry for safe metadata and table-browse reads after a transport failure
- Optional, capped local query history
- Copy loaded rows as TSV or export them as CSV
- Exact string transport for large integers and decimals
- Explicit NULL, binary, and truncated-value representations
- Loopback-only HTTP server with a random per-launch bearer token

## Quick start

Install the latest macOS release:

```sh
curl -fsSL https://raw.githubusercontent.com/jaydip216/Rowlight/master/install.sh -o /tmp/rowlight-install.sh
sh /tmp/rowlight-install.sh
```

The installer detects Apple Silicon or Intel, verifies the release SHA-256 checksum, and installs `rowlight` into `/usr/local/bin`. It asks for `sudo` only when that directory is not writable.

Then launch Rowlight:

```sh
rowlight
```

To install a specific release or choose another destination:

```sh
ROWLIGHT_VERSION=v0.1.0-alpha ROWLIGHT_INSTALL_DIR="$HOME/.local/bin" sh /tmp/rowlight-install.sh
```

### Build from source

Development builds require:

- Go 1.24 or newer
- Node.js 20 or newer
- macOS, Linux, or Windows

```sh
git clone https://github.com/jaydip216/Rowlight.git
cd Rowlight
make build
./bin/rowlight
```

Rowlight listens on a random `127.0.0.1` port and opens an authenticated URL in the default browser.

Useful launch options:

```sh
# Print the URL without opening a browser
./bin/rowlight -no-open

# Use a fixed loopback port
./bin/rowlight -port 7070
```

The frontend is embedded in the executable. End users do not need Node.js after the binary has been built.

## Supported databases

| Engine | Default port | Browse model |
|---|---:|---|
| PostgreSQL | 5432 | Schemas inside the connected database |
| MySQL | 3306 | Databases |
| MariaDB | 3306 | Databases |

The current integration fixtures cover PostgreSQL 17 and MariaDB. A broader version matrix and certificate-enabled database fixtures are planned.

## Read-only behavior

Rowlight accepts a deliberately narrow SQL subset. It supports `SELECT`, `SHOW`, and `EXPLAIN`; MySQL and MariaDB also support `DESCRIBE` and `DESC`. Multiple statements, writes, locking reads, file output, session control, and other potentially mutating constructs are rejected.

Queries also execute inside database read-only transactions. These checks reduce accidents, but they are not a security boundary for hostile SQL or privileged database functions. Use a database account with only the permissions you intend to grant.

## Security and local data

- The HTTP server binds only to loopback.
- API calls require a random token generated on each launch.
- Host and Origin headers are validated.
- Passwords and client private keys remain in backend memory and are not saved in profiles.
- TLS never silently downgrades when a verified mode is selected.
- MySQL multi-statement execution is disabled.

On macOS, nonsecret profiles and query history are stored with owner-only permissions at:

```text
~/Library/Application Support/Rowlight/state.json
```

Query text can contain sensitive literals. History can be disabled or cleared from the query toolbar.

## Development

Run the complete unit suite and production frontend build:

```sh
make test
```

Build the release-style executable:

```sh
make build
```

Run the backend on port 7070:

```sh
make dev-backend
```

Create unsigned macOS archives for Apple Silicon and Intel, plus SHA-256 checksums:

```sh
make release-darwin VERSION=0.1.0-alpha
```

Measure backend startup, idle RSS, and large-result encoding on the current machine:

```sh
make benchmark
```

For Vite hot reload, run this in another terminal:

```sh
cd web
npm install
npm run dev
```

The Vite development server proxies `/api` to `http://127.0.0.1:7070`.

### Browser smoke test

Rowlight includes a dependency-free WebDriver smoke test for Safari or Chromium. Start a WebDriver server, launch Rowlight with a fixed port, and pass the authenticated URL printed by Rowlight:

```sh
# Safari: enable Develop → Allow Remote Automation, then run:
safaridriver -p 4444
./bin/rowlight -no-open -port 7070
ROWLIGHT_URL='http://127.0.0.1:7070/#token=…' BROWSER_NAME=safari make browser-smoke
```

For Chromium, point `WEBDRIVER_URL` at a running ChromeDriver and set `BROWSER_NAME=chrome`. The check verifies UI bootstrap, removal of the token from the URL, authenticated API access after refresh, and clean session shutdown.

### Integration tests

MySQL or MariaDB:

```sh
ROWLIGHT_INTEGRATION=1 \
ROWLIGHT_TEST_HOST=127.0.0.1 \
ROWLIGHT_TEST_PORT=3306 \
ROWLIGHT_TEST_USER=reader \
ROWLIGHT_TEST_PASSWORD=secret \
ROWLIGHT_TEST_DATABASE=app \
go test -run TestMariaDBIntegration -v ./internal/store
```

For a certificate-enabled fixture, set `ROWLIGHT_TEST_TLS=custom` and `ROWLIGHT_TEST_CA_FILE` to its PEM CA certificate. `ROWLIGHT_TEST_SERVER_NAME` can override hostname verification when the certificate uses a different DNS name.

PostgreSQL:

```sh
ROWLIGHT_POSTGRES_INTEGRATION=1 \
ROWLIGHT_POSTGRES_HOST=127.0.0.1 \
ROWLIGHT_POSTGRES_PORT=5432 \
ROWLIGHT_POSTGRES_USER=rowlight \
ROWLIGHT_POSTGRES_PASSWORD=secret \
ROWLIGHT_POSTGRES_DATABASE=rowlight_test \
go test -run TestPostgresIntegration -v ./internal/store
```

The PostgreSQL integration test creates and replaces its fixture table and view in the configured database. Use a disposable test database.

## Architecture

```text
Browser UI
    │ authenticated loopback HTTP + streamed NDJSON
    ▼
Rowlight Go process
    ├── connection and query coordinator
    ├── bounded result encoding
    ├── local profiles and history
    └── database/sql
          ├── pgx
          └── go-sql-driver/mysql
```

Frontend assets are compiled by Vite and embedded into the Go executable. The browser never connects directly to the database.

## Current limits

- Query previews stop at 1,000 rows or approximately 8 MiB.
- Individual cell previews stop at 1 MiB and are marked as truncated.
- Table browsing uses offset pages of 100 rows by default and caps pages at 200 rows.
- Unsorted offset pages have database-defined ordering.
- At most two user queries execute concurrently across the process.
- User SQL is never replayed after a connection failure. Reconnect explicitly, review the preserved SQL, and run it again.
- CTEs, writable sessions, grid editing, SSH tunnel management, and Keychain storage are not implemented yet.
- macOS archives are unsigned and not notarized. Safari/Chromium automation and a broader database/TLS matrix are still pending.

See [PLAN.md](./PLAN.md) for the roadmap and validation goals. API and frontend development notes are in [web/README.md](./web/README.md).

## Contributing

Issues and focused pull requests are welcome. Please run `make test` before submitting changes and include a real-database integration test when changing engine-specific connection, metadata, or query behavior.
