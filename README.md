# comis-dev-crew

Pre-release. Nothing here works yet.

`comis-dev-crew` is a companion product for the [Comis](https://github.com/comisai/comis)
agent platform: a long-lived Go service (`devcrew-service`), an independent operator CLI
(`devcrew`), a thin MCP facade (`devcrew-mcp`), and a restricted task reporter
(`devcrew-report`) for supervising multiple coding-CLI workers in isolated git worktrees.

## Status: maintainer bootstrap

This repository currently contains only a maintainer-created bootstrap (module root,
license, this file). It is the intended starting state for the Go implementation lane:
the coding agent adopting this repository verifies this marker, then produces the real
scaffold — authoritative `AGENTS.md`, subordinate `CLAUDE.md`, their repository-policy
test, the `Makefile` verification contract, CI workflows, and the four `cmd/*`
entrypoints — as its first committed work, before any substantive production code.

The design authority lives in the private `comisai/planning` repository
(`comis-companion-ecosystem/`), including the implementation design, the common
companion-service architecture contract, the parallel-development process, and the
binding ratification record.

## License

Apache-2.0 — see [LICENSE](LICENSE).
