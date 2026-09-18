# Lightweight PostgreSQL/MySQL/MariaDB browser client

Status: active implementation; the Phase 1–3 workflow is runnable and PostgreSQL/MySQL/MariaDB support is implemented.

## Agreed direction

Build a free, focused SQL client, initially distributed for macOS. A CLI command starts a local Go process and opens the existing browser. PostgreSQL, MySQL, and MariaDB are the first database targets. Connections go directly to the database and support TLS. The initial workflow is connect → browse schema → run a query → inspect/copy/export results, with read-only connections by default.

Backend resident memory is the primary memory metric, as requested. Browser memory is excluded from that budget. Browser responsiveness still matters. The backend will stay running while the CLI runs; Ctrl-C closes database sessions and shuts it down. Product name: Rowlight.

## Options and recommendation

| Decision | Recommended | Alternative and tradeoff |
|---|---|---|
| Backend | Go standard library HTTP server, database/sql, go-sql-driver/mysql | Rust is viable, but there is no measured benefit here that justifies changing the agreed Go direction |
| UI | TypeScript, CSS, CodeMirror 6, a small headless grid virtualizer | Preact can simplify growing UI state; choose it if the initial UI spike shows plain TypeScript becoming difficult to maintain |
| Delivery | One executable containing built web assets | Separate assets complicate installation without a clear MVP benefit |
| Result transport | HTTP streaming with bounded NDJSON batches | WebSockets add connection lifecycle work that this workflow does not currently need |
| Saved state | Small versioned local files; secrets behind a credential-store interface | Embedded database can be added if history or persistence becomes complex |

Use Node tooling during development to bundle the frontend; end users need neither Node nor Go. Go supports embedding frontend assets at compile time. CodeMirror supports modular bundling and removing unused code. TanStack Virtual offers framework-independent virtualization on both axes; evaluate its core package before building custom scroll machinery. [Go embed](https://pkg.go.dev/embed), [CodeMirror bundling](https://codemirror.net/examples/bundle/), [TanStack Virtual](https://tanstack.com/virtual/latest/docs/introduction).

## First-release scope

- Connection profiles: host, port, user, default database, TLS settings, connection test, and useful authentication errors.
- Save nonsecret profile details locally. Passwords remain in memory by default; offer optional macOS Keychain storage after the packaging spike validates the integration. Never silently fall back to plaintext passwords.
- Sidebar: databases, tables/views, columns, indexes, and searchable names. Load details on expansion and refresh explicitly.
- SQL editor: highlighting, basic completion from loaded schema, run selection or one statement, keyboard shortcuts, and a visible connection/database indicator.
- Results: column types, resizable columns, keyboard navigation, copy cells/rows, explicit NULL presentation, text/JSON inspection, elapsed time, fetched-row count, and clear truncation status.
- Table browsing: bounded pages, basic filters and sorting executed in the database. Prefer keyset pagination with a suitable unique key; use bounded offset pagination otherwise and document its cost and instability under concurrent changes.
- Cancellation, timeouts, connection errors, and reconnect behavior.
- CSV export of the currently fetched result, clearly labeled with its limit. Full-result streaming export is a later feature.
- Small capped local query history, with disable and clear controls. Store query text and metadata, not result contents; explain that SQL text can contain sensitive values.

Proposed v0.1 boundary: no write-mode toggle, grid editing, schema migrations, SSH tunnel manager, cloud sync, AI features, dashboards, plugins, or other database engines. Existing external SSH tunnels can still present a local port. A later release can add explicit writable sessions without changing the read-only default.

## Architecture and execution contract

```text
Browser: connection form | schema tree | SQL editor | results grid
                 authenticated local HTTP / streamed results
Go: local server → query coordinator → database/sql + MySQL/PostgreSQL drivers
          ├─ connection profiles / credential store
          ├─ bounded schema cache
          └─ cancellation, result encoding and resource limits
                                      ↓ direct TCP / TLS
                       PostgreSQL, MySQL, or MariaDB
```

The browser never connects directly to the database. The backend owns credentials and database sessions. Use a small lazy connection pool per connected profile, a global cap, one active query per editor, and a separate control path for cancellation. Unused profiles must not open sockets. Metadata discovery must not scan entire tables or automatically run expensive exact row counts.

Each query gets an ID, context deadline, and pinned database connection/transaction. The stream contains metadata, bounded row batches, and one terminal event: complete, truncated, cancelled, or failed. Close rows, roll back the read transaction, and release or discard the connection on every path. Clean up on browser disconnect as well as explicit cancellation. Persistent user-controlled SQL sessions are outside v0.1. Do not keep completed results in a backend cache.

Do not append LIMIT to arbitrary SQL: that can change meaning or produce invalid SQL. Limit application delivery by rows and bytes, and test how the selected driver behaves when results are abandoned. Closing rows can involve draining protocol data; do not assume it instantly stops server work. Set database-aware execution limits where supported and verify termination separately.

Go driver cancellation closes the underlying connection when cancellation happens before results finish. Reconnect transparently for later queries, with read-only setup reapplied. Immediate server-side termination is not guaranteed by that client behavior; the spike must inspect the server and decide whether a separate same-user KILL QUERY control connection is necessary and feasible. Synchronize query completion and cancellation so a late cancellation never kills a subsequent query using a reused connection. [Driver documentation](https://github.com/go-sql-driver/mysql).

Preserve BIGINT and DECIMAL as exact strings with type metadata. Keep SQL NULL distinct from empty strings. Preserve raw date/time representations, zero dates, binary values, and duplicate column names; encode rows as positional arrays. Truncate cell previews visibly rather than silently changing copied/exported values. A single huge wire-protocol value can exceed normal batch budgets before display truncation; test it and document the supported limits.

## Read-only behavior and local access

Read-only transactions are a useful layer, not a complete sandbox for arbitrary SQL. MySQL permits some temporary-table changes; implicit commits and session-control statements require care across all supported engines. A restricted database account is the authoritative protection against unauthorized writes. [MySQL transaction behavior](https://dev.mysql.com/doc/refman/8.4/en/commit.html), [MariaDB transactions](https://mariadb.com/docs/server/reference/sql-statements/transactions/start-transaction).

The initial policy accepts one understood read statement at a time and uses a read-only transaction on the same pinned connection. MySQL driver multi-statements remain disabled. The policy applies PostgreSQL- or MySQL-specific quoting and comment rules and rejects unknown statements, transaction/session control, locking reads, file output, procedure calls, and executable-comment bypasses. A first-keyword check is insufficient; even SELECT can have side effects through functions. Unsupported safety setup fails closed, with an actionable explanation. Do not promise that parsing makes a privileged account safe for arbitrary hostile SQL.

Bind the HTTP server to loopback only. Require a random per-launch credential, validate Host and Origin, protect state-changing requests, and disable permissive CORS. Design and test the browser bootstrap so credentials are not placed in query strings or request logs. Escape database content as text, use a restrictive content security policy, and load all runtime assets locally.

TLS configuration must verify the server certificate and hostname. Support system trust, custom CA, and optional client certificate/key where needed. Never silently downgrade a requested TLS connection. Disable local-infile loading and avoid exposing arbitrary driver options through profile import. Redact credentials from logs and errors. These are part of connecting a browser to database credentials, not optional release polish.

## Performance budgets to validate

These are proposed engineering targets, not measured claims or release promises. Record hardware, OS, browser, server version, build, fixture, and repetitions for every benchmark.

| Measurement | Initial target |
|---|---|
| Go RSS, idle without database connection | ≤30 MB; 20 MB is a stretch goal |
| Go RSS, one idle direct TLS connection | ≤50 MB |
| Backend ready after CLI launch | ≤150 ms on the reference Mac |
| UI usable in an already-open browser | ≤500 ms, excluding database connection establishment |
| Initial query delivery | ≤1,000 rows or 8 MiB encoded output, whichever occurs first |
| Application row batch | ≤256 rows plus a byte cap; oversized cells handled separately |
| Concurrency | Start with 2 active user queries globally, then benchmark |
| Idle behavior | No recurring schema scans; no persistent CPU activity from polling |

Use release builds and report median and p95 startup timings plus peak RSS. Measure a large generated result without allowing retained memory to scale with its total row count. Include wide rows, large cells, slow readers, cancellation, and repeated connect/query/disconnect cycles. Go may retain freed heap pages, so report allocation/heap trends separately from RSS. Browser RAM is outside the target, but frontend row retention and frame responsiveness remain bounded.

## Build milestones and completion checks

| Milestone | Deliverable | Completion check |
|---|---|---|
| 1. Feasibility | Tiny Go server, embedded editor/grid experiment, direct TLS connection, parser/read-only and cancellation experiments | Measured baseline; verified server cancellation behavior; explicit supported SQL subset; chosen frontend and parser |
| 2. First usable workflow | CLI opens UI, connect, list tables, run a read query, stream results, cancel | End-to-end workflow works on PostgreSQL, MySQL, and MariaDB; errors leave the app usable |
| 3. Daily-use features | Paging/filtering, schema completion, cell inspection, copy, CSV, profiles/history | Exact datatype fixtures survive display/copy/export; result limits are visible and honored |
| 4. Reliability | Local authentication, TLS failures, read-only policy, cleanup, bounded concurrency, keyboard access | Integration and browser tests pass; repeated operations do not accumulate resources |
| 5. Release | macOS arm64 and amd64 builds, checksums, install/uninstall instructions, supported-server matrix, benchmark report | Clean-machine smoke tests; no runtime dependency installation; documented limits match observed behavior |

Establish a narrow, explicitly tested server matrix, starting with PostgreSQL 17, MySQL 8.4, and MariaDB 11.4 as proposed baselines. Add other versions only after integration tests. Test Safari and Chromium on macOS. Packaging should assess Gatekeeper/signing requirements early; signing/notarization depends on available developer credentials. The Keychain integration may require platform bindings, so validate build requirements before claiming a completely static executable. Review dependency licenses and include required notices; choose the project's open-source license before publishing.

Do not set a calendar estimate until milestone 1 resolves parser coverage, cancellation, and packaging. Each milestone should leave a runnable build; feature work follows the verified memory and execution design.

## Verification and future agent split

Meaningful automated checks cover dangerous statement rejection (including comments/CTEs/multiple statements), read-only enforcement with restricted and privileged test accounts, TLS rejection, stale-session cleanup, cancellation and follow-up queries, slow consumers, quoted identifiers, exact data values, and CSV escaping. Run integration checks against both real database families. Test browser bootstrap against cross-origin requests and database cell content against HTML injection. Ensure cancellation tests verify server state, not just a changed button label.

During implementation, parallel work can be split into backend execution, frontend interaction, and independent integration/performance review after API contracts are agreed. One owner integrates changes and prevents conflicting edits. Two independent reviews informed this plan: architecture/performance and database semantics. No artifact-generation skill is necessary for this Markdown engineering plan; use relevant skills if later work involves hosted sites, design files, or other specialized artifacts.

The immediate next implementation step is milestone 1, not scaffolding every future feature. Success means evidence that the small Go process, bounded results, and read-only query workflow work together on both engines.
