# comis-dev-crew

[![ci](https://img.shields.io/github/actions/workflow/status/comisai/comis-dev-crew/ci.yml?branch=main&style=flat-square&label=ci)](https://github.com/comisai/comis-dev-crew/actions/workflows/ci.yml)
[![security](https://img.shields.io/github/actions/workflow/status/comisai/comis-dev-crew/security.yml?branch=main&style=flat-square&label=security)](https://github.com/comisai/comis-dev-crew/actions/workflows/security.yml)
[![platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-blue?style=flat-square)](#installation)
[![license](https://img.shields.io/badge/license-Apache--2.0-blue?style=flat-square)](LICENSE)

**Durable, fail-closed supervision for coding workers in isolated Git worktrees.**

`comis-dev-crew` is the Go companion service for
[Comis](https://github.com/comisai/comis). Comis owns identity, conversations,
policy, capabilities, approvals, and terminal confinement. This project owns
development tasks, worktrees, worker adapters, evidence, validation, delivery
safety, and cleanup.

> **Pre-release:** the project is under active E0 development. There is no
> supported production deployment or stability guarantee. Review the
> [implementation status](docs/implementation-status.md) before using it with
> important repositories, hosts, or credentials.

## Key properties

- **Isolated work:** every task runs in a dedicated Git worktree.
- **Durable state:** SQLite records task transitions through a single service
  writer.
- **Replay-safe operations:** stable operation IDs prevent duplicate logical
  effects across retries and restarts.
- **Evidence-based transitions:** incomplete or contradictory evidence becomes
  `unknown`; it never becomes success or cleanup authority.
- **Narrow interfaces:** the CLI and MCP adapter use the same owner-only local
  JSON-RPC API instead of writing state directly.

## Installation

Supported release targets are macOS and Linux on AMD64 and ARM64.

### Release installer

Review the [installer script](docs/install.sh), then install the latest published
release:

```sh
curl -fsSL https://raw.githubusercontent.com/comisai/comis-dev-crew/main/docs/install.sh | sh
```

The installer verifies the archive against the release `checksums.txt`, installs
all four executables in `~/.comis-dev-crew/bin`, and links them into a directory
on `PATH`. To install a specific release or change the destination:

```sh
curl -fsSL https://raw.githubusercontent.com/comisai/comis-dev-crew/main/docs/install.sh \
  | DEVCREW_VERSION=v0.1.0 \
    DEVCREW_INSTALL_DIR="$HOME/.comis-dev-crew/bin" \
    DEVCREW_LINK_DIR="$HOME/.local/bin" sh
```

Replace `v0.1.0` with the required tag. The installer exits without changing the
system if it cannot find a release or verify its checksum.

The current published pre-release is `v0.1.0`. Recovery work on branches newer
than that tag is available only from source until a subsequent pre-release is
published.

### Build from source

Use the exact Go toolchain declared in [go.mod](go.mod):

```sh
git clone https://github.com/comisai/comis-dev-crew.git
cd comis-dev-crew
go build -trimpath -o bin/ ./cmd/...
```

This creates the four executables in `bin/`. Alternatively, `go install
./cmd/...` installs them into `GOBIN`.

## Quick start

Start the service with private, canonical state and socket paths:

```sh
devcrew-service \
  --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

In another terminal, inspect service and task state:

```sh
devcrew --socket /absolute/private/run/devcrew.sock service status
devcrew --socket /absolute/private/run/devcrew.sock tasks list --format json
devcrew --socket /absolute/private/run/devcrew.sock task explain TASK --format json
```

Without explicit path flags, the service and CLI derive matching locations from
the operating system's user configuration directory. For the full Comis and
coding-worker configuration, see [Running comis-dev-crew](docs/running.md).

## Executables

| Command | Purpose |
| --- | --- |
| `devcrew-service` | Long-lived service and sole production writer of durable state |
| `devcrew` | Human and script-friendly operator CLI |
| `devcrew-mcp` | Stateless MCP adapter over the canonical local API |
| `devcrew-report` | Restricted, task-scoped worker reporter |

Every command supports `--help` and `--version`.

## Architecture

Production dependencies point inward:

```text
cmd/*  ->  adapters  ->  internal/application  ->  internal/domain
```

The domain contains pure types and invariants. Application packages define
commands, queries, reducers, and consumer-owned interfaces. Adapters provide
SQLite, local API, Git, forge, worker, reporter, and Comis integration. The four
commands are composition roots only.

The complete engineering and security contract is in [AGENTS.md](AGENTS.md).

## Development

Read [AGENTS.md](AGENTS.md) and [CONTRIBUTING.md](CONTRIBUTING.md), then enable
the repository hooks:

```sh
git config core.hooksPath .githooks
```

Run the required verification gate before handing off a change:

```sh
make verify
```

Use `make verify-full` before a push or production-readiness claim. The protected
`make test-live` campaign is separate and requires explicitly configured external
prerequisites.

Additional references:

- [Implementation status](docs/implementation-status.md)
- [Operations guide](docs/running.md)
- [Dependency policy](DEPENDENCIES.md)
- [Security policy](SECURITY.md)

## Security

Report suspected vulnerabilities privately as described in
[SECURITY.md](SECURITY.md). Do not disclose them in a public issue.

## License

Licensed under Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
