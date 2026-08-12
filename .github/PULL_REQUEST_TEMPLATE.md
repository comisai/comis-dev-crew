# Pull request

## What and why

<!-- The problem, and why this is the right layer to fix it. -->

## Risk class

<!-- Per AGENTS.md: low (docs, comments, formatting, test-only, build metadata),
     medium (domain/application behavior, read-only CLI output, non-authority
     adapters), or high (auth, protocol, storage, path safety, concurrency,
     credentials, forge mutation, delivery, cleanup). Classify upward when
     uncertain. -->

- [ ] Low
- [ ] Medium
- [ ] High

## Evidence

RED — the failing command and its failure, before the implementation:

```text

```

GREEN — the commands and results after:

```text

```

## Checklist

- [ ] One concern in this change; unrelated changes preserved.
- [ ] Conventional Commit messages, with no `Co-Authored-By:` trailer.
- [ ] `make verify` passes (required for every handoff).
- [ ] `make verify-full` passes (required before push or a readiness claim).
- [ ] No secrets, credentials, databases, sockets, runtime directories, task
      worktrees, or coverage artifacts are committed.
- [ ] Documentation and command help updated in this change, not a later one.
- [ ] No new runtime dependency, or DEPENDENCIES.md records its justification,
      provenance, and license review.
- [ ] Generated files regenerated rather than hand-edited; `make generate-check`
      detects no drift.

## High-risk changes only

- [ ] Threat note included below.
- [ ] Negative boundary tests added.
- [ ] Real-artifact, restart, or fault evidence attached as applicable.

<!-- Threat note: what authority this touches, what an attacker or a malicious
     worker could attempt, and which invariant refuses it. -->
