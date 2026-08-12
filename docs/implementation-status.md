# Implementation status

`comis-dev-crew` is pre-release E0 foundation work. This page records, subsystem
by subsystem, what is actually implemented and what is deliberately not claimed.
It is maintained alongside the behavior it describes.

## Summary

The service owns durable SQLite state and a strict owner-only local API. The
operator CLI provides read-only service, fleet, task, and operation views. The
protocol foundation pins the 24-artifact Comis capability-service revision at
source commit `b84577fe790829bf1f043af6c2626f6b27ef7b89` and bundle digest
`94ec7bd173cd20f0de2cb4e9ab719d392f240236ac80d56e3a7ea1abe4e20cb8`, and generates
a closed Go adapter.

Real worker execution, public mutation transport, and end-to-end production Comis
runtime wiring are not implemented yet.

## Foundation

The maintainer-created bootstrap was adopted without reinitializing its history.
The repository has its engineering protocol, verification contract, CI foundation,
pure E0 domain records, a pure-Go SQLite store, canonical read application
handlers, a bounded newline-delimited local protocol over an owner-only Unix
socket, the first read-only operator CLI, and an authenticated Comis protocol pin
with generated DTO, validation, and Unix control client support.

The protocol-foundation join gate is implemented for protocol
`comis.capability-service/1`, including workspace-lease, terminal-event, and
execution-attachment control scopes. End-to-end production worker execution and
public mutation transport are not claimed.

## Comis adapter

The adapter contains the supervised persistent bidirectional connection used by
the next service composition step. It authenticates the exact pinned handshake,
dispatches only `managedRuns.activate` and `managedRuns.abandon`, carries
`managedRuns.report` on the same socket, and reconnects with bounded backoff.
Wrong credentials, altered operation envelopes, unknown fields, excess
concurrency, and forged run references fail before handler authority. The adapter
does not retry an uncertain report itself.

A separate stateless forwarder polls the durable outbox, maps the closed worker
vocabulary to the pinned Comis report vocabulary, and retries uncertain outcomes
with the same operation and service-report identities until it can durably record
an exact host acknowledgement. It is implemented as an independently supervised
adapter and participates in the installed service lifecycle.

The service lifecycle supervises exactly one supplied control connection and that
forwarder alongside both local endpoints, cancelling and joining all of them if
any component fails.

Authenticated inbound activation is backed by the same durable mutation
coordinator: the stored external reference, registration nonce, service instance,
UTC expiry, and workspace-request invariant are checked in the transaction that
commits the exact managed run and workspace lease. Inbound abandonment durably
closes the private preparation; `preserve` retains the prepared task with its
closed reason, while `reap_safe` enters the unbound reversible
cancellation and cleanup path. Exact operation replay returns the original wire
acknowledgement, and altered reuse is rejected with a content-free durable
conflict audit.

## Task records

Task records persist bounded acceptance criteria and constraints with the exact
brief revision hash. Any changed contract field invalidates the pin, and
incomplete lifecycle reconciliation can become only `unknown` until verified
evidence selects a known E0 state.

## Startup reconciliation

Before opening its local socket or advertising readiness, the service reconciles
durable startup state. Prepared and ready tasks remain known because no work-start
evidence exists. Tasks whose runtime may have been active become `unknown`, as do
operations left merely `accepted`. Stable terminal task evidence and completed or
already-unknown operations are preserved, and a repeated restart is idempotent.

## Mutation boundary

The first mutation boundary prepares a service-minted task and later activates it
with the exact host-managed run and workspace lease. Each change commits the task
and its completed operation at one global state version. Identical operation
replays recover the original task across a service restart; altered subjects fail
closed, and concurrent identical preparation creates one logical task.

Preparation also atomically stores the private external reference, registration
nonce, bounded UTC expiry, requested workspace root, and open or abandoned posture
needed for the Comis two-phase join. Exact replay returns that same private
registration instead of minting another.

In the installed production composition, the authenticated terminal `created`
acknowledgement is cross-bound to the exact managed run and workspace lease, the
reviewed launch descriptor is rebuilt and verified, and a stable service-owned
start operation records `ready` to `launching` before the event is acknowledged.
Exact event replays are idempotent and altered reuse fails closed. A `running`
event remains insufficient by itself: only its durable join with the protected
wrapper's task, canonical working directory, run and lease, and brief-hash
acknowledgement advances `launching` to `working`. Terminal exit or missing
evidence never means success.

Threat posture: the production supervisor reconciles the stable terminal operation
before any start side effect, serializes inbound lifecycle coordination, and
refuses ambiguous run or lease bindings and inconsistent reviewed descriptors
without acknowledging the event. Unverified evidence cannot select a worker,
redirect the protected attachment, or advance task state.

## Local client and MCP adapter

The typed local client and strict handler expose `PrepareTask` as the first
canonical mutation. Its public payload contains only task-contract fields: the
stable operation ID comes from the request envelope and the configured service
instance comes from endpoint composition. The result classifies the operation as
`mutate` and carries the private durable registration for the MCP adapter.

The service composition can bind this same coordinator to a dedicated owner-only
MCP endpoint while retaining a separate operator endpoint, and both servers cancel
and join together.

The stateless MCP adapter uses the exact-pinned official Go SDK. The facade
package is implemented and tested over its official in-memory transport before the
command root uses the production stdio transport. See
[running.md](running.md) for the tool surface and call-context rules.

## Adapter parity

The adapter parity fixture drives one real service and database through the typed
client, CLI JSON, and official-SDK MCP transport. Preparation produces the same
normalized task, operation, state version, and `mutate` classification across all
three paths. Repeated calls create one task, and an altered stable operation
remains the same non-retryable `conflict`. List, get, explain, and launch plan
return identical versioned projections through all adapters and retain their
`read` classification.

A tagged integration test builds and kills the real stdio `devcrew-mcp` process,
replaces it, and proves the prepared task, completed operation, exact private
extension, and one logical replay remain intact; a forged managed-run metadata hint
changes no task authority. A second tagged test builds and launches both installed
processes, performs the authenticated pinned handshake, prepares through the
official-SDK MCP facade, activates through the persistent control socket, verifies
the safe reviewed Codex launch plan, and drives the real task from `ready` through
`launching` to `working` using authenticated terminal events plus the exact runtime
wrapper acknowledgement.

The generated authenticated client is verified against Comis's standalone
test-only capability-service host over a real owner-only Unix socket, including
exact protocol and digest agreement plus altered-digest and wrong-credential
rejection.

## Repository registry

The registry resolves only operator-configured opaque IDs. It validates canonical
primary checkouts and dedicated worktree roots beneath approved roots, pins the
primary Git common-directory filesystem identity, and creates or exactly adopts an
operation-bound task worktree only when real Git queries prove the exact
repository, pinned base, branch, and path identity.

Deterministic bounded branch naming is collision-safe; retries return the same
worktree. Dirty, divergent, unpushed, symlink-escaped, primary-checkout,
live-sibling, untracked-target, and cleanup-ambiguous postures fail closed without
overwriting or removing work.

## Reporter seam

The seam is append-only and task-scoped. Its endpoint stores only a digest of the
protected credential, derives the task identity instead of accepting it from
worker content, requires the exact pinned brief revision and hash, bounds the
sparse closed report payload, and rejects a mismatched sink receipt.

Its application sink atomically persists the exact authenticated report, advances
the task cursor and closed E0 lifecycle, and enqueues a stable Comis delivery
identity at the same global state version. Pending deliveries survive restart and
remain eligible for exact-identity resend until the store records the host's
matching sequence and retention acknowledgement. Identical task and report IDs
replay the original receipt across restart; altered payloads, acknowledgements, and
ambiguous decision keys fail closed.

A real worker uses an owner-only per-task Unix socket to read its brief,
acknowledge its exact run, lease, canonical working directory, and brief binding,
and append reports without a task or authority selector. Preparation creates that
socket under the configured private runtime root and declares it as
`requestedAttachment`; activation then supplies the execution-attachment ID and
target name that bind the same listener. The listener and any durable activation
binding are reconstructed after a service restart.

## Worker harness

The first real harness adapter builds a fixed no-shell `codex exec --json`
descriptor from an exact-version static profile. The descriptor validates the
activation-returned attachment ID and target name, references no host socket
source, and binds only the exact protected mounted path and its matching target
name to the two fixed reporter environment keys. Task, run, and lease identity,
the execution-attachment ID, and brief authority remain only in the protected
attachment, never in argv or the generic bootstrap prompt.

Structured activity is classified only while fresh; a completed turn without a
task report is `unknown`. Because the reviewed Codex CLI does not expose a
trustworthy settle signal, the current profile is explicitly degraded and cannot
run unattended. The repository does not infer settled or successful work from a
completed Codex turn, and does not invent a private attachment protocol or a
second terminal backend.

The canonical `GetLaunchPlan` read accepts `ready` and recovery-reread `launching`
tasks, then invokes that configured adapter with the durable task, workspace,
brief, and activation binding, but projects only the profile ID, terminal
allow-entry ID, brief revision hash, and attachment target with durable source,
confidence, and freshness metadata. Executable paths, argv, shell text,
environment bindings, workspace paths, and attachment IDs are not part of the
local, CLI, or MCP result.

Production composition accepts this profile only from operator startup
configuration, requires a canonical regular non-symlink executable that is not a
shell launcher, and proves the exact pinned version before opening service
endpoints. The fixed adapter owns argv and the protected mount binding; task or
model content cannot redirect either, and lifecycle settling remains degraded.

## Deterministic fixture worker

The fixture worker runs synchronously from a verified brief, emits authenticated
progress, requests exactly one keyed decision, records its resolution, and ends by
reporting only a validation candidate. It supports explicit fault stops before or
after each durable-report boundary.

The restart matrix independently cancels the service and requesting context before
and after prepare, binding acknowledgement, and report acceptance. Stable
identities replay one logical effect; an interrupted runtime becomes `unknown`
without relaunch or false success.

The fixture launches no subprocess and exists to prove these properties around the
report lifecycle. Fixture composition is available only as a programmatic test
seam; the production service command exposes no fixture-worker flags. Candidate
completion advances only to `validating`; it never claims validation, delivery, or
terminal success.

## Design record

The detailed design and ratification record is maintained privately by the
maintainer and is not part of this repository. The binding constraints a
contributor needs are reproduced in [AGENTS.md](../AGENTS.md), which is
self-contained and authoritative for this tree.
