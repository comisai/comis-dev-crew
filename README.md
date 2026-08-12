# comis-dev-crew

A Go companion service for the [Comis](https://github.com/comisai/comis) agent
platform. It supervises multiple coding-CLI workers in isolated Git worktrees and
keeps durable, replay-safe custody of the resulting task state.

Comis owns human identity, conversations, immutable policy, capabilities,
approvals, terminal confinement, continuation, delivery, and generic
observability. This repository owns development tasks, worktrees, worker adapters,
reports, evidence, forge truth, delivery safety, and cleanup. It is not a second
agent platform.

## Status: pre-release

**This is unreleased E0 foundation work. There is no tagged version, no supported
deployment, and no stability or security guarantee.** Do not point it at
repositories, credentials, or hosts you are not prepared to lose access to.

What works today: durable SQLite state with a single writer, a strict owner-only
local JSON-RPC API, a read-only operator CLI, an authenticated and digest-pinned
Comis protocol adapter, the first canonical mutation boundary with replay-safe
task preparation and activation, an append-only worker reporter seam, fixed
exact-version Codex and Claude Code worker adapters, installed supervision,
candidate validation and delivery, and a seven-tool MCP facade on the official
Go SDK.

What is not claimed: a supported production deployment, public operator mutation
transport, merge authority, external-event ingress, or trustworthy unattended
settling from either worker CLI. Further capabilities remain behind explicit
platform gates.

[docs/implementation-status.md](docs/implementation-status.md) records the
subsystem-by-subsystem detail, including what each component explicitly refuses
to claim.

## Executables

| Command | Role |
| --- | --- |
| `devcrew-service` | Long-lived service; the only production composition root for durable domain mutation |
| `devcrew` | Operator CLI over the typed local client |
| `devcrew-mcp` | Stateless seven-tool MCP facade on the official SDK stdio transport |
| `devcrew-report` | Restricted task-scoped worker brief reader and sparse reporter |

All four support `--help` and `--version`.

## Requirements

- The exact Go toolchain pinned in [go.mod](go.mod).
- Linux or macOS. The SQLite adapter is pure Go, so `darwin/amd64`,
  `darwin/arm64`, `linux/amd64`, and `linux/arm64` cross-compile without CGO.
- A real Comis instance and exact reviewed Codex or Claude Code CLI profiles for
  anything beyond the read-only service.

## Build

```text
git clone https://github.com/comisai/comis-dev-crew.git
cd comis-dev-crew
go build -trimpath -o bin/ ./cmd/...
```

That writes all four executables into the ignored `bin/` directory. `make build`
compiles the same packages as a check without emitting them, and `go install
./cmd/...` places them on your `GOBIN` path instead. There is no tagged version,
so every build reports `dev`.

## Quickstart

The read-only service needs two explicit canonical paths:

```text
bin/devcrew-service --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

Then query it:

```text
bin/devcrew --socket /absolute/private/run/devcrew.sock status
bin/devcrew --socket /absolute/private/run/devcrew.sock tasks list --format json
```

The service creates its state and runtime directories as owner-only and refuses
relative, non-canonical, symlinked, broad-root, non-regular, live, or
identity-ambiguous targets. Without explicit flags, both binaries derive the same
paths under the operating system's user configuration directory.

[docs/running.md](docs/running.md) covers the full Comis and coding-worker lane, the
complete flag set, the MCP facade, the operator CLI surface, and the worker
reporter contract.

## Design principles

These are enforced by the test suite, not just documented:

- **One durable writer.** Only `devcrew-service` mutates the SQLite store,
  through the application mutation coordinator. The CLI, MCP adapter, reporter,
  and tooling never become alternate writers.
- **Unknown stays unknown.** Missing, stale, malformed, contradictory, or
  incomplete evidence becomes `unknown`. It never becomes success, idle, absence,
  or cleanup safety.
- **External content is never authority.** Worker text, reports, artifacts, forge
  responses, and MCP metadata cannot grant capability, raise trust, choose
  identity, or satisfy approval.
- **Fail closed on ambiguity.** Dirty, divergent, symlink-escaped, or
  cleanup-ambiguous postures refuse the operation rather than overwrite or remove
  work.
- **Content-free diagnostics.** Normal logs and events carry no briefs,
  objectives, reports, diffs, terminal output, paths, full arguments, environment
  values, or credentials.

The full protocol is [AGENTS.md](AGENTS.md), which is authoritative for this tree.

## Development

Read [AGENTS.md](AGENTS.md) before changing anything, then install the repository
hook:

```text
git config core.hooksPath .githooks
```

```text
make verify       # handoff gate
make verify-full  # push and readiness gate
```

`make test-live` is a separate protected campaign requiring real external
prerequisites; it fails rather than skips when they are absent, and it is not part
of `verify-full`.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening an issue or pull request,
and [DEPENDENCIES.md](DEPENDENCIES.md) for accepted dependency provenance.
[CLAUDE.md](CLAUDE.md) contains Claude-specific operational notes only.

## Security

Report vulnerabilities privately. See [SECURITY.md](SECURITY.md). Do not open a
public issue for a suspected vulnerability.

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
