# comis-dev-crew

Pre-release scaffold. The four commands build and expose version/help output; domain,
service, storage, worker, and Comis integration behavior is not implemented yet.

`comis-dev-crew` is a companion product for the [Comis](https://github.com/comisai/comis)
agent platform: a long-lived Go service (`devcrew-service`), an independent operator CLI
(`devcrew`), a thin MCP facade (`devcrew-mcp`), and a restricted task reporter
(`devcrew-report`) for supervising multiple coding-CLI workers in isolated git worktrees.

## Status: implementation scaffold

The maintainer-created bootstrap has been adopted without reinitializing its history.
The repository now has its engineering protocol, verification contract, CI foundation,
and the four required command entrypoints. Development is proceeding toward the E0
protocol-foundation gate. No production capability is claimed by this scaffold.

The design authority lives in the private `comisai/planning` repository
(`comis-companion-ecosystem/`), including the implementation design, the common
companion-service architecture contract, the parallel-development process, and the
binding ratification record.

## Commands

- `devcrew-service` — long-lived service composition root (scaffold only)
- `devcrew` — operator CLI composition root (scaffold only)
- `devcrew-mcp` — thin MCP facade composition root (scaffold only)
- `devcrew-report` — restricted worker reporter composition root (scaffold only)

Each command currently supports `--help` and `--version`. Build all four with
`make build`.

## Development

Read [AGENTS.md](AGENTS.md) before changing the repository. Install the repository hook
with `git config core.hooksPath .githooks`, then use:

```text
make verify       # handoff gate
make verify-full  # push/readiness gate, excluding the protected live campaign
```

The protected E0 live campaign is intentionally separate as `make test-live` and is not
yet implemented. See [CLAUDE.md](CLAUDE.md) only for Claude-specific operational notes.

## License

Apache-2.0 — see [LICENSE](LICENSE).
