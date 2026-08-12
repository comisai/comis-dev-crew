# comis-dev-crew

[![ci](https://img.shields.io/github/actions/workflow/status/comisai/comis-dev-crew/ci.yml?branch=main&style=flat-square&label=ci)](https://github.com/comisai/comis-dev-crew/actions/workflows/ci.yml)
[![security](https://img.shields.io/github/actions/workflow/status/comisai/comis-dev-crew/security.yml?branch=main&style=flat-square&label=security)](https://github.com/comisai/comis-dev-crew/actions/workflows/security.yml)
[![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-blue?style=flat-square)](#install)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

**Supervise coding-CLI workers in isolated git worktrees, with durable custody of
every task transition.**

A Go companion service for the [Comis](https://github.com/comisai/comis) agent
platform. Comis owns human identity, conversations, immutable policy,
capabilities, approvals, terminal confinement, continuation, delivery, and generic
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
task preparation and activation, an append-only worker reporter seam, and a
five-tool MCP facade on the official Go SDK.

What is not implemented: real unattended worker execution, public mutation
transport, end-to-end production Comis runtime wiring, and the protected live
campaign. Several further capabilities are deliberately deferred behind ratified
platform gates rather than merely unfinished.

[docs/implementation-status.md](docs/implementation-status.md) records the
subsystem-by-subsystem detail, including what each component explicitly refuses
to claim.

## How it works

```text
    a task, prepared through the local API or the MCP facade
                        │
                        ▼
  ┌───────────────────────────────────────────────────────────┐
  │  its own git worktree — your checkout is never touched    │
  │  prepare → activate → launch → work → report → validate   │
  └───────────────────────────────────────────────────────────┘
                        │  every transition needs verified evidence
                        ▼
       durable, replay-safe task state — or `unknown`
```

Each transition either proves itself with authenticated evidence or refuses to
advance. A worker exiting is not success, a completed terminal turn is not
settled work, and an interrupted runtime becomes `unknown` rather than a guess.
Identical operations replay to one logical effect across a service restart;
altered reuse of an operation ID fails closed with a content-free audit record.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/comisai/comis-dev-crew/main/docs/install.sh | sh
```

The installer resolves the latest release for your platform, **verifies the
archive digest against the release `checksums.txt`, and refuses to install on a
mismatch**. It places the four executables in `~/.comis-dev-crew/bin` and
symlinks them onto your `PATH`. Override with `DEVCREW_INSTALL_DIR`,
`DEVCREW_LINK_DIR`, or `DEVCREW_VERSION`.

> **No release is published yet.** Until one is tagged, the installer exits with
> an explanation and installs nothing. Build from source in the meantime.

Build from source with the exact Go toolchain pinned in [go.mod](go.mod):

```sh
git clone https://github.com/comisai/comis-dev-crew.git
cd comis-dev-crew
go build -trimpath -o bin/ ./cmd/...
```

That writes all four executables into the ignored `bin/` directory. `make build`
compiles the same packages as a check without emitting them, and `go install
./cmd/...` places them on your `GOBIN` path instead.

Every build reports `dev` for `--version`. Version provenance is a deferred
design decision, so released binaries will report `dev` too until it is ratified.

Supported targets are `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and
`linux/arm64`. The SQLite adapter is pure Go, so all four cross-compile without
CGO or a platform C toolchain. Anything beyond the read-only service also needs a
real Comis instance and a reviewed worker CLI profile.

## Executables

| Command | Role |
| --- | --- |
| `devcrew-service` | Long-lived service; the only production composition root for durable domain mutation |
| `devcrew` | Operator CLI over the typed local client |
| `devcrew-mcp` | Stateless five-tool MCP facade on the official SDK stdio transport |
| `devcrew-report` | Restricted task-scoped worker brief reader and sparse reporter |

All four support `--help` and `--version`.

## Quickstart

The read-only service needs two explicit canonical paths:

```sh
devcrew-service --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

Then query it:

```sh
devcrew --socket /absolute/private/run/devcrew.sock status
devcrew --socket /absolute/private/run/devcrew.sock tasks list --format json
```

The service creates its state and runtime directories as owner-only and refuses
relative, non-canonical, symlinked, broad-root, non-regular, live, or
identity-ambiguous targets. Without explicit flags, both binaries derive the same
paths under the operating system's user configuration directory.

[docs/running.md](docs/running.md) covers the full Comis and Codex lane, the
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

```sh
git config core.hooksPath .githooks
```

```sh
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

Maintainers: [docs/repository-hardening.md](docs/repository-hardening.md) records
the server-side protection baseline and the script that applies it.

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
