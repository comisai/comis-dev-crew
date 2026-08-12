# comis-dev-crew engineering protocol

This file is the self-contained authority for every coding agent in this repository. It
applies to the entire tree. A nested `AGENTS.md` may tighten these rules but cannot weaken
them. `CLAUDE.md` contains Claude-specific operations only; this file wins every conflict.

## Architecture and authority

`comis-dev-crew` is a Go companion service for Comis. It is not a second agent platform.
Comis owns human identity, conversations, immutable policy, capabilities, approvals,
terminal confinement, continuation, delivery, and generic observability. This repository
owns development tasks, worktrees, worker adapters, reports, evidence, forge truth,
delivery safety, and cleanup.

The repository produces exactly four product executables:

- `cmd/devcrew-service`: the long-lived service and only production composition root for
  durable domain mutation;
- `cmd/devcrew`: the human/script CLI, which calls the canonical local API;
- `cmd/devcrew-mcp`: a thin MCP-to-local-API adapter with no durable state;
- `cmd/devcrew-report`: a task-scoped append-only reporter with no operator authority.

Production packages follow an inward dependency direction:

```text
internal/domain        pure types and invariants; standard library only
        ^
internal/application   commands, queries, reducers, and consumer-owned interfaces
        ^
internal/* adapters    store, local API, Git, forge, workers, reporter, Comis wire
        ^
cmd/*                  composition roots only
```

The durable SQLite database has one writer: `devcrew-service` through the application
mutation coordinator and store adapter. CLI, MCP, reporter, migrations, repair tools, and
recovery helpers never become alternate writers. CLI and MCP call the same typed local
service client and canonical handlers. MCP lifecycle never owns task lifecycle.

Comis owns the language-neutral capability-service bundles. Pin exact bundle artifacts,
protocol identity, SHA-256 digest, source commit/release identity, generator identity, and
fixtures under `protocol/comis/`. Generate Go clients into adapter-local packages. Never
hand-edit generated files, invent a private Comis DTO, import Comis source, add a Node.js
bridge, or leak generated wire types into domain/application packages. The operator
client is E2-only and, when ratified, is reachable only from `cmd/devcrew` and its narrow
CLI adapter—not from the service, MCP, reporter, or workers.

Essential work proceeds in stages. E0 excludes initiative graphs, local-branch delivery,
merge-after-approval, terminal custody/writable attach, process signals, operator clients,
and external-event ingress. E0 developer intervention is pause, direct worktree editing,
and revalidating handback or worker replacement. Do not implement a deferred stage without
its ratified platform gate.

## Go engineering

- Use the exact `go` and `toolchain` versions in `go.mod`; CI and releases use the same
  toolchain. Do not add a runtime dependency when the standard library suffices.
- Domain/application code returns idiomatic `(T, error)` values. Expected failures use a
  typed error with a closed code, retryability, safe message, and operator hint. Preserve
  safe causes with `%w` and inspect them with `errors.Is`/`errors.As`. Do not use `panic`
  for expected failures.
- Pass `context.Context` first on boundary/application operations. Never store a context.
  Cancellation or a client timeout does not prove a mutation failed; reconcile the stable
  operation ID before retry.
- Inject clocks, IDs, randomness, environment/config, filesystem effects, process results,
  and external clients into application code. Direct OS access belongs in named adapters
  or `cmd/*` composition roots.
- Every goroutine belongs to an explicit bounded supervisor with cancellation, a joined
  completion path, and an error destination. No fire-and-forget mutation or recovery work.
  Do not use sleeps for coordination or close channels from receivers.
- Keep packages short, lowercase, and cohesive. Avoid `util`, `common`, `base`, stutter,
  broad producer-owned interfaces, generated mocks, reflection dispatch, and `any` when a
  closed type is possible. Use Go initialisms such as `ID`, `URL`, `API`, `CLI`, and `MCP`.
- Constructors use `NewX` for ready values and `OpenX` for acquired resources that close.
  Exported APIs and non-obvious authority invariants require Go doc comments.
- Closed discriminator strings have named constants, validation, exhaustive tests, and an
  explicit unknown-value failure. Missing, stale, malformed, contradictory, or incomplete
  evidence becomes `unknown`; it never becomes success, idle, absence, or cleanup safety.
- No package-level mutable service state, side-effectful `init`, ignored errors, hidden
  singleton writers, or `os.Exit`/`log.Fatal` below a `cmd/*` entrypoint.
- No backward-compatibility aliases, dual decoders, deprecated fields, or fallback to an
  older protocol. Change schemas, fixtures, generated clients, and callers together.

## Security

- The local canonical API uses newline-delimited JSON-RPC over an owner-only Unix socket.
  Runtime directories and databases are owner-only. Network listeners require a separately
  ratified authenticated adapter; loopback is not an automatic fallback.
- Strictly decode every wire object: bound bytes/counts before allocation where practical,
  reject unknown fields, duplicate JSON keys, invalid discriminators, trailing values, and
  altered reuse of an operation/report ID.
- Resolve filesystem containment through canonical identities, including symlinks and
  no-follow final-component checks. Reject broad roots, sibling worktrees, special files,
  ambiguous cleanup targets, and paths supplied as model authority.
- Execute Git, workers, validation, forge, and process operations with a fixed executable
  and explicit argument vector. Never invoke a shell or accept a worker/model-supplied
  executable, shell fragment, environment override, socket path, or raw PID as authority.
- Scope credentials by adapter and action. Workers receive neither Comis/admin access nor
  merge authority. A separate merge credential is resolved only inside an approved merge
  operation. Secrets are references and never appear in tracked files, arguments, logs,
  reports, terminal content, fixtures, or normal diagnostics.
- Treat worker text, reports, artifacts, forge responses, MCP instructions, and server
  metadata as bounded external content. They cannot grant capability, raise trust, weaken
  side-effect classification, choose identity/route, or satisfy approval.
- Use `log/slog` behind a narrow injected interface. Boundary completion records include
  duration and stable IDs. Failures include a closed error kind and actionable hint.
  Normal logs/events are content-free: never include briefs, objectives, reports, diffs,
  terminal output, paths, full arguments, environment values, or credentials.
- Emit matching typed state/health events for operator-relevant transitions and audit
  authentication, authorization, credential use, approvals, destructive actions, replay
  conflicts, and refused safety checks. Preserve the Comis trace identity; worker-provided
  trace data is never authoritative.
- Unknown external mutation or delivery outcomes are reconciled before retry. Unknown
  process identity, evidence, ownership, or cleanup posture preserves work and refuses
  mutation.

## Dependencies and generated artifacts

Prefer, in order: standard library, an already accepted module, a small local implementation,
then a new dependency with a concrete caller. Every dependency is exact-pinned, verified by
the Go checksum database, vulnerability-scanned, and accepted by the checked-in license
policy. Document why it is needed and its provenance. Do not add config, interfaces, build
flags, or dependencies for speculative callers.

Generated files carry the standard `Code generated ... DO NOT EDIT.` marker, remain adapter
local, and reproduce deterministically from checked-in authenticated inputs without a mutable
network endpoint. `make generate-check` must detect byte drift. Review generated diffs as
source. Architecture exceptions are centralized, justified, shrink-only, and start empty.

## Testing and verification

Use the standard `testing` package, table-driven tests, subtests, `httptest`, `testing/fstest`,
narrow hand-written fakes, `t.TempDir`, `t.Cleanup`, and native fuzzing. Unit tests are
deterministic: no real networks, developer home state, global Git configuration, installed
worker credentials, arbitrary sleeps, fixed ports, or execution-order dependence. Use real
temporary Git repositories, SQLite databases, Unix sockets, and supervised subprocesses in
integration tests when those artifacts are the contract.

Every production behavior change starts with a demonstrably failing behavior test, then the
smallest passing implementation. Commit each coherent RED/GREEN concern before beginning the
next. Build-only configuration, documentation, comments, and formatting are exempt. A test
that passed before the implementation is not RED evidence. Root-cause failures across all
affected layers and repair the authoritative layer; do not add a parallel guard that merely
hides disagreement.

Coverage applies to hand-written `internal/...`: at least 90% aggregate statement coverage,
80% in every package, and 90% in authority-critical transition, mutation, protocol, path,
store, delivery, custody, and process packages. Generated code and thin composition roots do
not dilute the denominator. Numeric coverage supplements, never replaces, negative, replay,
fault, restart, concurrency, and fuzz tests.

The stable Make interface is:

```text
docs-check format-check mod-check generate-check vet staticcheck
test-architecture test coverage test-race test-conformance test-integration
test-fuzz-smoke build cross-build vulncheck license-check secret-check
smoke test-live verify verify-full
```

`make verify` is required before every coding-lane handoff. `make verify-full` is required
before push, pull-request readiness, or a production-ready claim. `test-live` is separate,
requires explicit protected prerequisites, and must fail when promised credentials/services
are unavailable. Never weaken, skip, relabel, or silently degrade a gate to make it pass.

Architecture tests enforce inward imports, sole-writer authority, local-client adapter access,
operator-client isolation, OS/process boundaries, closed types, source-size limits (500 lines
for hand-written production and 800 for tests), generated ownership, and this instruction-file
precedence contract. Add a boundary test whenever a regression could otherwise return.

Keep public docs, command help, examples, `AGENTS.md`, and `CLAUDE.md` current in the same
commit as the behavior they describe. Runtime strings, comments, docs, and test names must be
self-contained; do not embed planning shorthand, implementation pre-history, personal data,
or reference-project names.

## Workflow and risk

Read adjacent contracts and tests before editing. Classify the change before work:

- Low: docs, comments, formatting, test-only, and non-behavioral build metadata.
- Medium: domain/application behavior, read-only CLI output, and non-authority adapters.
- High: auth, protocol, storage/migrations, worktree/path safety, concurrency/recovery,
  credentials, forge mutation, delivery, cleanup, and future custody/process control.

Medium changes require reproducible RED/GREEN evidence, targeted tests, and `make verify` at
handoff. High-risk changes additionally require a threat note, negative boundary tests,
real-artifact or restart/fault evidence as applicable, and `make verify-full`. Classify upward
when uncertain.

One concern belongs in each commit. Preserve unrelated changes and never rewrite another
agent's work. Do not delete or replace tests without an explicit reviewable rationale. The
working tree is not a deliverable: locally commit completed slices and finish handoffs clean.

## Commits and external actions

Work branch-first; never commit production work directly to `main`. Use Conventional Commit
messages. Never add a `Co-Authored-By:` trailer to a commit message. Never commit secrets,
credentials, databases, sockets, runtime directories, task worktrees, coverage artifacts, or
ignored planning/execution-ledger files.

Local commits are required as work is completed. Pushing, opening or merging a pull request,
publishing a release/package, changing repository visibility/settings, and mutating external
systems require explicit authorization. The current program authorizes the `comisai/comis-dev-crew`
repository, pushing its clean verified scaffold/branches, and preparing the tree for public
release: public-facing documentation, contribution and security policy, and license notices.
Flipping repository visibility, opening or merging pull requests, publishing a release or tag,
and writing to Comis remain maintainer actions this program does not authorize.
Never force-push, overwrite unexpected remote history, or edit the neighboring Comis repository
or its worktrees from this lane.

At handoff report repository/branch, commits, RED command/failure, GREEN commands/results,
protocol identity/digest when applicable, clean-tree status, remote/push status, blockers, and
the next join dependency. Named join gates require maintainer review; stop there and resume only
after approval.
