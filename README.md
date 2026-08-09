# comis-dev-crew

Pre-release E0 foundation. The service now owns durable SQLite state and a strict owner-only
local API; the operator CLI provides read-only service, fleet, task, and operation views. The
protocol foundation pins an exact Comis capability-service bundle and generates a closed Go
adapter. The durable-work wave now includes the closed E0 task transition authority and one
canonical task contract whose deterministic worker brief is pinned by a stored SHA-256 revision
hash. The application now commits replay-safe task preparation and exact host binding atomically
with their durable operation outcomes. Worker execution, public mutation transport, and
end-to-end Comis runtime wiring are not implemented yet.

`comis-dev-crew` is a companion product for the [Comis](https://github.com/comisai/comis)
agent platform: a long-lived Go service (`devcrew-service`), an independent operator CLI
(`devcrew`), a thin MCP facade (`devcrew-mcp`), and a restricted task reporter
(`devcrew-report`) for supervising multiple coding-CLI workers in isolated git worktrees.

## Status: independent E0 foundation

The maintainer-created bootstrap was adopted without reinitializing its history. The
repository now has its engineering protocol, verification contract, CI foundation, pure
E0 domain records, a pure-Go SQLite store, canonical read application handlers, a bounded
newline-delimited local protocol over an owner-only Unix socket, the first read-only operator
CLI, and an authenticated Comis protocol pin with generated DTO, validation, and Unix control
client support. The protocol-foundation join gate is implemented. End-to-end Comis host
integration, public mutation transport, and real worker capability are not claimed yet.

Task records persist bounded acceptance criteria and constraints with the exact brief revision
hash. Any changed contract field invalidates the pin, and incomplete lifecycle reconciliation
can become only `unknown` until verified evidence selects a known E0 state.

The first mutation boundary prepares a service-minted task and later acknowledges the exact
host-managed run and workspace lease. Each change commits the task and its completed operation
at one global state version. Identical operation replays recover the original task across a
service restart; altered subjects fail closed, and concurrent identical preparation creates one
logical task.

The repository registry resolves only operator-configured opaque IDs. It validates canonical
primary checkouts and dedicated worktree roots beneath approved roots, pins the primary Git
common-directory filesystem identity, and accepts a task worktree only when a real Git query
proves that exact identity. It does not create worktrees or launch workers yet.

The in-process reporter seam is append-only and task-scoped: its endpoint stores only a digest
of the protected credential, derives the task identity instead of accepting it from worker
content, requires the exact pinned brief revision/hash, bounds the sparse closed report payload,
and rejects a mismatched sink receipt. Unix-socket reporter transport is still pending.

The deterministic fixture worker runs synchronously from a verified brief, emits authenticated
progress, requests exactly one keyed decision, records its resolution, and supports explicit
fault stops before or after each durable-report boundary. It launches no subprocess and exists
to drive restart/replay tests before the first real coding-worker adapter.

The design authority lives in the private `comisai/planning` repository
(`comis-companion-ecosystem/`), including the implementation design, the common
companion-service architecture contract, the parallel-development process, and the
binding ratification record.

## Commands

- `devcrew-service` — long-lived store and local read-API authority
- `devcrew` — read-only operator control console
- `devcrew-mcp` — thin MCP facade composition root (scaffold only)
- `devcrew-report` — restricted worker reporter composition root (scaffold only)

All four commands support `--help` and `--version`. Build them with `make build`.

Start the current service with explicit canonical paths:

```text
devcrew-service --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

The service creates its final state/runtime directories as owner-only, stores the database
as owner-only, and refuses relative, non-canonical, symlinked, broad-root, non-regular, live,
or identity-ambiguous targets. Without explicit flags, both binaries derive the same paths
under the operating system's user configuration directory.

The current CLI surface is read-only:

```text
devcrew [--socket PATH] service status
devcrew [--socket PATH] doctor [--format table|json]
devcrew [--socket PATH] status [--format table|json]
devcrew [--socket PATH] tasks list [--format table|json]
devcrew [--socket PATH] task show TASK [--format yaml|json]
devcrew [--socket PATH] task explain TASK [--format text|json]
devcrew [--socket PATH] task operation OPERATION [--format text|json]
```

JSON outputs are stable versioned projections. Human and YAML views are presentation only
and carry no authority. Host integration remains explicitly unavailable until the adapter is
wired to a runnable Comis capability-service host; the pinned source exposes only a test fixture
host. The CLI never opens SQLite as a normal-operation fallback.

## Development

Read [AGENTS.md](AGENTS.md) before changing the repository. Install the repository hook
with `git config core.hooksPath .githooks`, then use:

```text
make verify       # handoff gate
make verify-full  # push/readiness gate, excluding the protected live campaign
```

The protected E0 live campaign remains intentionally separate as `make test-live` and is
not yet implemented. See [DEPENDENCIES.md](DEPENDENCIES.md) for accepted runtime dependency
provenance and [CLAUDE.md](CLAUDE.md) only for Claude-specific operational notes.

## License

Apache-2.0 — see [LICENSE](LICENSE).
