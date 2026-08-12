# Contributing

Thanks for looking at `comis-dev-crew`. Please read this page before opening an
issue or a pull request.

## What this repository accepts right now

This is pre-release E0 foundation work with a narrow, explicitly staged scope. It
is not looking for feature contributions yet, and several capabilities are
deliberately deferred behind ratified platform gates rather than left undone. A
pull request that implements a deferred stage will be declined regardless of its
quality.

Useful contributions today:

- Bug reports with a reproduction against a specific commit.
- Security reports, via [SECURITY.md](SECURITY.md) rather than a public issue.
- Corrections where the documentation and the implementation disagree.
- Failing tests that demonstrate a stated invariant does not hold.

If you want to propose something larger, open an issue describing the problem
first. Please do not send a large unsolicited pull request.

## The engineering protocol is binding

[AGENTS.md](AGENTS.md) is the authoritative protocol for this repository and
applies to human and automated contributors alike. It wins every conflict with
any other file, including this one. Read it before changing code. It is not
style advice; it encodes the architecture, security, and evidence rules that the
automated gates enforce.

Points that most often surprise new contributors:

- Dependencies are exact-pinned and admitted only for a concrete caller. New
  runtime dependencies need a documented justification in
  [DEPENDENCIES.md](DEPENDENCIES.md) and must pass the checked-in license policy.
- There is exactly one durable writer to the SQLite store. The CLI, MCP adapter,
  reporter, and tooling never become alternate writers.
- Unknown outcomes stay unknown. Missing, stale, or contradictory evidence never
  becomes success, idle, or cleanup safety.
- Backward-compatibility shims are not accepted. Schemas, fixtures, generated
  clients, and callers change together.
- Generated files are never hand-edited.

## Development setup

You need the exact Go toolchain named in [go.mod](go.mod). Then enable the
repository hook so the gates run before a push:

```text
git config core.hooksPath .githooks
```

Confirm it with `git config --get core.hooksPath`; it must print `.githooks`.

## Verification gates

Run focused tests while developing, then:

```text
make verify       # required before any handoff
make verify-full  # required before pushing or claiming readiness
```

Never weaken, skip, relabel, or silently degrade a gate to make it pass. If a
gate is wrong, fix the gate in its own commit with a stated rationale.

`make test-live` is a separate protected campaign that requires real external
prerequisites. It is expected to fail rather than skip when those are absent,
and it is not part of `verify-full`.

## Tests come first

Every production behavior change starts with a demonstrably failing behavior
test, then the smallest implementation that passes it. A test that already
passed before your change is not evidence. Documentation, comments, formatting,
and build-only configuration are exempt.

When something breaks across layers, repair the authoritative layer. Do not add
a parallel guard that hides the disagreement.

## Commits and pull requests

- Work on a branch. Never commit directly to `main`.
- Use [Conventional Commits](https://www.conventionalcommits.org/).
- One concern per commit. Preserve unrelated changes.
- Do not add a `Co-Authored-By:` trailer.
- Never commit secrets, credentials, databases, sockets, runtime directories,
  task worktrees, or coverage artifacts.
- Keep documentation and command help current in the same commit as the behavior
  they describe.

In the pull request, state what changed and why, the risk class you assigned
(low, medium, or high, per AGENTS.md), your RED and GREEN evidence, and which
gates you ran. High-risk changes additionally need a threat note and negative
boundary tests.

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

By contributing you agree that your contributions are licensed under the
Apache License 2.0, as recorded in [LICENSE](LICENSE).
