# Implementation status

`comis-dev-crew` is pre-release E0 foundation work. This page records, subsystem
by subsystem, what is actually implemented and what is deliberately not claimed.
It is maintained alongside the behavior it describes.

## Summary

The service owns durable SQLite state and a strict owner-only local API. The
operator CLI provides service, fleet, task, operation, reconciliation, handback, and cleanup
views and commands. The
protocol foundation pins the 30-artifact Comis capability-service contract at
source commit `b0b8065c0b43a29840dcd21fcc15ef37e4b905d4` and bundle digest
`fff96cf5105d9cda9da5dfd2fbc7e9f15242754f63d7f8155cde4ef874d5c52b`, and generates
a closed Go adapter.

Installed composition supervises the Comis control lane, Codex and Claude Code
launch descriptors, candidate validation, forge truth, delivery, unknown-task
reconciliation, handback, and safe cleanup. Merge authority and unattended worker
settling are not claimed.
Tagged release builds inject the exact tag into all four executables, while
untagged source builds identify themselves as `dev`.

The protected live gate is implemented for a dedicated Linux runner and a real
human Telegram sender. It observes two working lanes, restarts only manifest-bound
isolated MCP, DevCrew, and Comis units, waits for post-restart Telegram evidence,
then verifies cleaned task, operation, Comis, Git, GitHub, and count-only secret
residency truth. This is an executable gate, not a claim that the external campaign
has run; release readiness still requires a passing protected invocation.
The gate observes the sibling still working immediately after the human-approved
handback; initial worker overlap alone does not satisfy that continuation claim.
The owner-private manifest records exact Comis and DevCrew commits, requires the
compiled protocol identity, and pins the Comis CLI plus all four DevCrew product
artifacts by canonical path, SHA-256, and version. Runtime validation re-hashes
those installed files and checks their fixed `--version` output before any
protected action. It requires both canonical source checkouts to resolve `HEAD`
to their corresponding recorded commits. It also pins the complete `systemctl cat` output for each of
the three isolated units, including active drop-ins, and binds exact hashed Codex
and Claude Code installations to the two campaign task profiles.
The manifest binds the canonical DevCrew database and worktree root. The live
support package can capture content-free start and finish metrics for the three
isolated service processes, including `/proc` RSS, file descriptors, and
descendant bubblewrap jails; both pinned database sizes; Comis data; active
DevCrew terminal bindings; task worktrees; and both durable delivery backlogs.
It rejects a sample pair that spans less than one hour, exceeds bounded growth,
or leaves a terminal, jail, worktree, or delivery behind.
The campaign runner makes that pair mandatory: it captures after simultaneous
worker overlap, waits out the one-hour floor, samples again after cleanup, and
writes the observation into the hashed evidence directory. Evidence-only
closeout likewise requires a non-overwriting owner-private baseline bound to the
same campaign and exact source commits; a manifest duration cannot substitute
for two live samples.
The protected recovery library also implements non-overwriting, hash-inventoried
backup and isolated restore of Comis data, DevCrew SQLite state, candidate
configuration, and repository-bearing unit definitions. It excludes plaintext
`.env`, runs the count-only secret-residency oracle over the full retained tree,
checks every restored SQLite database and the restored Comis configuration, and
probes exact previous binaries only against separately copied synthetic state.
The full campaign runner and evidence-only closeout both require the resulting
strict owner-private recovery artifact before a passing verdict can be written.
These are executable acceptance mechanisms; the external protected run remains
required before their evidence can be credited.
Comis system-health and session-explanation artifacts are structurally validated
before they can contribute a passing verdict: source coverage, campaign activity,
hard-degraded posture, agent identity, and the exact origin Telegram conversation
must be present rather than inferred from an arbitrary JSON object.
The explanation set must also account for the closed eight-tool campaign catalog,
including two successful task preparations and cleanups, the reconciliation and
handback mutations, diagnostic reads, and two precondition-classified cleanup
refusals. Those refusals must retain the distinct unresolved-decision and
dirty-worktree safe messages in the bounded Comis failure evidence.

## Foundation

The maintainer-created bootstrap was adopted without reinitializing its history.
The repository has its engineering protocol, verification contract, CI foundation,
pure E0 domain records, a pure-Go SQLite store, canonical read application
handlers, a bounded newline-delimited local protocol over an owner-only Unix
socket, the first read-only operator CLI, and an authenticated Comis protocol pin
with generated DTO, validation, and Unix control client support.

The protocol join gate is implemented for protocol
`comis.capability-service/1`, including attention-response, workspace-lease,
terminal-event, and execution-attachment control scopes. Public operator mutation transport is not
claimed.

## Comis adapter

The adapter contains the supervised persistent bidirectional connection used by
the next service composition step. It authenticates the exact pinned handshake,
dispatches only `managedRuns.activate`, `managedRuns.abandon`, and terminal events; carries
reports, evidence, attention-response receives, and workspace release on the same socket; and reconnects with bounded backoff.
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

## Unknown-task candidate recovery

An owner-only `ReconcileTask` mutation recovers the narrow case where an exact
worker terminal exited without a candidate report but the registered operation-
bound worktree contains a clean non-base descendant of the pinned base. Its closed
action is `validate-clean-candidate`; caller input contains no repository, path,
branch, head, run, lease, terminal, or attachment authority.

The service combines durable preparation and terminal bindings with a fresh Git
inspection, then atomically records reconciliation evidence and advances
`unknown` through `reconciling` into `validating`. It neither synthesizes a worker
report nor increments the report cursor. Exact operation replay returns the
original outcome, altered reuse conflicts, and dirty, missing, base-equal,
divergent, ambiguous, active, or mismatched authority leaves the task unchanged.
The normal candidate supervisor re-reads the exact head around fixed validation.
Cleanup later accepts exactly one candidate origin: a delivered worker candidate
report or one exact completed reconciliation record matching the sealed head.
After successful recovery validation, the two exact server-owned evidence
publications drive the task to `delivered`; the service does not create a worker
report merely to close the state machine.

`ExplainTask` combines durable terminal posture, current host connectivity, and
fresh registered-worktree inspection. Its closed recovery reasons distinguish a
settled terminal without candidate evidence, unresolved restart evidence,
unavailable host integration, an unrecoverable workspace, and reconciliation in
progress, with reachable reason-specific next actions.

Canonical task detail and explanation projections include content-free evidence
references for candidate, report activity, decision posture, validation, forge
delivery, cleanup, and opaque host authority. A reconciled candidate retains its
reconciliation operation reference after judgment, so final closeout can prove
candidate origin without a direct database read. Fleet rows consume the same durable
postures for head, activity, validation, blocking, and attention. Custody and
process observations remain explicitly `unknown` until a process-evidence
contract exists.

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

The typed local client and strict handler expose `PrepareTask`, `ReconcileTask`,
`HandbackTask`, and `CleanupTask` as canonical mutations. Preparation contains
only task-contract fields: the
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
`read` classification. The official-SDK facade exposes eight tools;
`reconcile_task` is non-read-only, idempotent, closed-world, and uses the same
stable result and side-effect semantics as the CLI and typed local client.

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
overwriting or removing work. A task held by one refused cleanup can be resumed by
a fresh same-task caller operation after the safety condition is corrected; the
original cleanup record remains the sole release/removal authority and the retry
is stored as an alias of the one completed effect.

Threat posture: resumption cannot select a task, worktree, lease, head, or delivery
record from the fresh caller operation. The unique existing task cleanup record
remains authoritative, an operation ID already owned by another command is rejected,
and the service re-proves the clean exact worktree and current forge truth before
host release and again before removal authorization. Open cleanup holds remain a
fail-closed pre-release blocker; the operator surface identifies only that closed
category and never exposes the hold's free-text reason. Unresolved decisions have
their own closed retryable precondition and operator hint, without exposing
decision content.

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

After a decision report is locally accepted, the reporter blocks on that same
protected socket until Comis returns the exact keyed owner response. The service
derives managed-run authority from the activation binding, uses a fresh operation
identity for every pending poll over its authenticated persistent connection,
and returns the private body only to the waiting reporter. Pending or temporarily
unavailable delivery remains content-free, while unbound sockets, response
identity drift, invalid states, and malformed bodies fail closed.

## Candidate validation and forge delivery

Candidate supervision re-reads the exact clean head around fixed no-shell local
checks, seals bounded validation and forge evidence, and holds delivery until the
configured required checks are green in fresh forge truth. Ship delivery performs
one non-force exact-branch push, resolves or creates one pull request, and re-reads
its head, base, state, URL, and check conclusions. Scout delivery reads only the
reviewed bounded artifact. Both use durable outbox identities for exactly-once
host delivery across restart.

Forge API and pull-request truth and branch push use distinct credentials. HTTPS
token push is supported, and an SSH route allows a repository-scoped deploy key
to be the push identity. The latter decodes the owner-private key only into a
transient `0600` file, invokes the canonical OpenSSH executable through fixed
service-owned argv, pins host keys, accepts only the configured Git receive or
upload command, and removes the key before returning. There is no merge operation.

## Worker harnesses

The Codex harness adapter builds a fixed no-shell `codex exec --json`
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

The Claude Code harness is independently exact-version pinned. It uses fixed
print-mode stream-json argv, disables session persistence, project settings,
hooks, plugins, slash commands, browser integration, and unreviewed MCP servers,
and permits non-interactive tool execution only inside its operator-selected
Comis jail. Its owner-private config directory is a fixed environment binding;
task, run, lease, brief, and attachment authority never enter argv or stdin.
Fresh `system`, `assistant`, and tool-result `user` events prove activity, while a
`result` without a task report remains `unknown`.

The canonical `GetLaunchPlan` read accepts `ready` and recovery-reread `launching`
tasks, then invokes that configured adapter with the durable task, workspace,
brief, and activation binding. It projects the profile ID, terminal allow-entry
ID, opaque managed-run and workspace-lease handles required by the Comis terminal
API, brief revision hash, and attachment target with durable source, confidence,
and freshness metadata. Executable paths, argv, shell text, environment bindings,
workspace paths, and attachment IDs are not part of the local, CLI, or MCP result.

Production composition accepts these profiles only from operator startup
configuration, requires canonical regular non-symlink executables that are not
shell launchers, and proves each exact pinned version before opening service
endpoints. The fixed adapters own argv and protected mount bindings; task or model
content cannot redirect either, and lifecycle settling remains degraded.

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
report lifecycle. Fixture composition requires explicit command flags and is used
only with deterministic reviewed inputs. Candidate
completion advances only to `validating`; it never claims validation, delivery, or
terminal success.

## Design record

The detailed design and ratification record is maintained privately by the
maintainer and is not part of this repository. The binding constraints a
contributor needs are reproduced in [AGENTS.md](../AGENTS.md), which is
self-contained and authoritative for this tree.
