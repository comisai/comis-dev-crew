# Claude operational notes

Read and follow `AGENTS.md` before making changes. `AGENTS.md` is the authoritative repository protocol and wins every conflict. If this file is stale, follow `AGENTS.md` and update this file in the same change.

## Commands

- Run focused Go tests while developing, then `make verify` before a handoff.
- Run `make verify-full` before any authorized push or readiness claim.
- Confirm the active hook with `git config --get core.hooksPath`; it must print `.githooks`.
- Build the four scaffold binaries with `make build`.

## Service operations

`devcrew-service` owns the installed lifecycle, worker launch supervision,
candidate validation, delivery, handback, and cleanup. Use only the canonical
local API and operator CLI; never mutate its database directly.

## Live prerequisites

`make test-live` is protected. A live campaign requires an explicitly configured
Comis instance, Telegram captain thread, disposable GitHub fixture repository,
and supported authenticated worker profiles. A missing prerequisite is a failure,
not a skip.

## Troubleshooting read order

Use the repository checks first: focused test, `make test-architecture`, `make verify`, then
`make verify-full`. Once product diagnostics exist, prefer fleet status, task explanation,
Comis managed-run explanation, and Comis system health before raw logs.
