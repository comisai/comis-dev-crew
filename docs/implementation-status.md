# Implementation status

`comis-dev-crew` is pre-release E0 foundation work. This page records, subsystem
by subsystem, what is actually implemented and what is deliberately not claimed.
It is maintained alongside the behavior it describes.

## Summary

The service owns durable SQLite state and a strict owner-only local API. The
operator CLI provides service, fleet, task, operation, and worker-profile views
alongside the task lifecycle commands: prepare, reconcile, handback, cleanup, and
the intervention set — pause, resume, cancel, verify, promote, replace, steer,
and the acknowledged operator-only discard. The
protocol foundation pins the 30-artifact Comis capability-service contract at
source commit `46bea003df4f28422dcf54a7a42a81e107d2b3c5` and bundle digest
`86f5f5eb3d8147ccf85200adb475ccfecdbe28f6acdeb5446b8b8a71edfa9b33`, and generates
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
Before those recovery probes, it runs the repository-shipped Comis and DevCrew
installers into a new private prefix, verifies all five installed artifacts by
the manifest hashes and versions, installs the five previous artifacts into a
second prefix using the separately pinned DevCrew release tag, upgrades that
prefix to the candidate versions, and re-verifies
all five bytes and versions. Every DevCrew install must retain the installer's
successful release-checksum proof.
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
handback mutations, diagnostic reads, and six precondition-classified cleanup
refusals. Those refusals must retain the distinct open-decision, open-hold,
active-execution, unknown-execution, dirty-worktree, and stale-forge-truth safe
messages in the bounded Comis failure evidence.

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

The pinned bundle also carries `managedRuns.heartbeat` and `managedRuns.cancel`.
A supervised liveness reporter now drives the first: it sweeps durable task
state and beats for every run that is both bound and unsettled, so a host that
requires liveness from this definition can tell quiet work from a departed
service. Each beat carries its own deadline, because the control connection
waits for an authenticated session and a beat delayed past the host's staleness
bound proves nothing.

Inbound `managedRuns.cancel` is dispatched to the durable task record: it stops
an activated run, preserves its artifacts, and reports an already-settled run
rather than refusing, so a second operator cancelling the same run is safe.

## Scout review attestation

A scout's worktree holds the only copy of its investigation, so removing it
before anybody has inventoried the report's open questions is how a buried
question disappears with the tree that held it. Cleanup therefore refuses a
scout until a recorded inventory states that no human decision remains open.

The inventory is a recorded semantic judgement, never a derived one: only a
model can read open questions out of prose. The finding is an explicit
discriminator rather than an inference from an empty key list, so neither
`open_decisions` nor `no_open_decisions` can be reached by omission, and a
request that states no finding is refused. Absence of a record and a record
finding nothing are kept distinct throughout — the first says nobody looked.

Promotion is gated the same way. Minting a ship task from a scout is the
concrete act of treating that investigation as a finished review, so an
investigation nobody inventoried — or one whose inventory still names open
decisions — cannot carry its authority forward without its unanswered parts.

One row exists per scout and a later inventory replaces an earlier one, so a
stale look never outvotes a fresher inspection of the same surface. Recording an
attestation moves no task: inventorying open questions observes the work rather
than finishing, delivering, or retiring it. Ship tasks are outside the gate;
their open decisions remain governed by the ordinary decision blocker.

## Open decision re-surfacing

An open decision wakes the liaison once as soon as it exists, and keeps coming
back until it is resolved or cancelled. Bounded describes the rate, not the end:
a question asked once and then dropped stops existing as far as the system is
concerned while the work it blocks waits indefinitely.

The first airing is the delivery of the worker's own decision report, so a report
still waiting in the durable outbox is not yet owed a repeat and the delivered
ones start their cadence from the moment the host acknowledged them. Counting only
the repeats would ask the liaison twice for a brand-new question, once through the
outbox and once immediately through the cadence.

The interval after each raising doubles from a configured initial wait up to a
configured maximum, so an unanswered question stops competing with fresh work
without ever becoming effectively silent. The default cadence is thirty minutes
growing to four hours; `--decision-resurface-initial` and
`--decision-resurface-maximum` configure it, and a cadence that could never
re-surface sensibly is refused rather than rounded into the default so a
deployment never runs at a rate it did not ask for. Filtering happens against the
decision rather than in the caller, so running the loop more often does not repeat
anything more often.

Each raising is recorded durably, keyed by the decision and keeping its first
sighting, so a restart replays at worst one repeat. An in-memory count would
make every open decision due again on every boot and wake the liaison with the
whole backlog. Open is decided by the same predicate cleanup uses — a decision
report with no resolution carrying its key — so the two surfaces can never
disagree about which questions are still live.

A bounded supervisor consumes the due-set on its own tick, raising each decision
through the same generic attention path it took the first time and recording the
raising only once it succeeded — a decision recorded before it was actually
raised would sit silent for a whole interval while the work it blocks waits. The
tick decides only how often the ledger is consulted; the cadence belongs to each
decision, so inspecting more often asks nobody anything more often. The tick is
the configured initial wait, capped at one minute so a long cadence still runs a
live loop. The supervisor is composed alongside the report forwarder, the evidence
forwarder and the liveness reporter whenever an authenticated host connection
exists; it is bounded by its context and joins on cancellation with them.

The raising itself is an ordinary attention report on the authenticated control
lane, carrying the original question and the run it belongs to. Its operation and
report identities are derived from the decision plus the number of airings already
recorded, so an uncertain send is retried under the exact same identity — the host
recognizes the repeat instead of asking twice — while the next airing is a new
report. Only an acknowledgement naming the same run and report counts as raised.

The open decisions are readable from the operator console as an inventory and as
one keyed decision, each stating which side is being waited on and when the
question will next be raised. The return schedule is derived once, next to the
cadence the supervisor runs, so no adapter can publish a schedule the service
does not keep. Both reads are operator-only in the canonical handler rather than
only in the model facade's tool list, because an open question is private task
detail and a facade that later grew a tool must not thereby gain the authority to
read it. Neither read can submit or close an answer: that stays on the generic
Comis attention path.

## Service event stream

Task state changes and decision openings and closings are recorded as a durable,
append-only event log, and the log is readable from a cursor.

Each event is written in the same transaction as the change it describes. That
placement is the guarantee: an event appended after the commit can be lost by a
crash, and one appended before it can describe a transition that rolled back, so
an operator watching the stream would see a state the service never reached or
miss one it did. Because task state has exactly one durable writer, recording the
event there makes divergence structurally impossible rather than a discipline.

The stream is content-free by construction. Every column is an identity, a closed
discriminator, a version or a time, so there is no column a question, objective,
path or branch could occupy. It is consequently reachable from the model facade
as well as the operator console, unlike the private task reads.

Reading is a bounded page plus the cursor to resume from, returned even when the
page is empty. Following is therefore a caller-side loop rather than a held
connection, which keeps the request/response transport and its bounded reads
unchanged, survives a restart, and lets a dropped follower resume exactly where it
stopped.

## Reconciliation survey

Which unknown tasks a reconcile would accept is readable from the operator
console. Each task is classified against the same evidence the reconcile command
requires — durable authority, terminal settlement, worktree verification,
cleanliness, and whether a commit exists ahead of the pinned base — using the
read-only half of the reconciliation inspector.

The survey reports and never acts, because choosing an action from evidence is
the authority the explicit per-task command holds and a survey that reconciled on
its own would be a second writer of that transition. Only the unknown state is
surveyed, since listing anything else would offer an action the service would
refuse. Every way the evidence falls short is its own closed posture rather than
an error, so one unreadable task never hides the rest of the fleet.

## Task change summaries

What a task changed is readable from the operator console as two bounded
summaries: committed work and work still in the tree. The split is the point —
committed work is what the worker stands behind, while uncommitted work is what a
handback would land in a developer's editor.

The base revision and the worktree come from durable state, never from the
request, so a read cannot be aimed at a tree the task does not own or measured
from a revision nobody pinned, and the same exact worktree identity checks as
candidate validation run first. Only counts and paths leave the adapter: a patch
body is unbounded worker-authored content and no surface asks for one. A binary
change is marked rather than counted as zero, a rename keeps both paths, a change
set larger than the read bounds reports its listing as truncated, and a path
carrying control characters or invalid encoding is refused rather than escaped.

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
already-unknown operations are preserved. A reconciled `candidate_complete` task is
also preserved when its accepted sealed evidence and exact pending publications are
consistent, so host delivery resumes after restart. A repeated restart is
idempotent.

## Unknown-task candidate recovery

An owner-only `ReconcileTask` mutation recovers the narrow case where an exact
worker terminal exited without a candidate report but the registered operation-
bound worktree contains a clean non-base descendant of the pinned base. Its closed
action is `validate-clean-candidate`; caller input contains no repository, path,
branch, head, run, lease, terminal, or attachment authority.

The service combines durable preparation and terminal bindings with a fresh Git
inspection. When Comis confinement produced a clean commit in lease-private Git
administration, read-only diagnostics recognize that exact candidate and the
reconciliation mutation explicitly hands it into the prepared host branch. The
service then atomically records reconciliation evidence and advances
`unknown` through `reconciling` into `validating`. It neither synthesizes a worker
report nor increments the report cursor. Exact operation replay remains bound to
the original result reference and may project only a monotonically newer state of
that task; altered reuse conflicts, and dirty, missing, base-equal, divergent,
ambiguous, active, or mismatched authority leaves the task unchanged.
The normal candidate supervisor binds its pre-validation Git read, validation
receipts, evidence commit, and forge request to the branch and head stored by the
completed reconciliation, while the Git pusher re-verifies that exact snapshot
before transfer. Cleanup later accepts exactly one candidate origin: a delivered
worker candidate report or one exact completed reconciliation record matching the
sealed head.
After successful recovery validation, the two exact server-owned evidence
publications drive the task to `delivered`; the service does not create a worker
report merely to close the state machine. Incomplete recovery history remains
unresolved after restart and refuses a second reconciliation record.

The private Git handoff is a high-risk boundary. Paths come only from the
registered worktree and its canonical Git administration, and the source record,
generated configuration, inert commit identity, copied worktree controls, branch, clean index, and base
ancestry must all match. Symlinks, executable Git configuration, alternate object
indirection, dirty content, or shared/private head drift are refused before any
host branch mutation. Promotion imports no tags or submodules, advances the exact
branch with an old-head compare-and-swap, and synchronizes only the worktree index;
it never replaces workspace files. Replay re-verifies the same clean head.

`ExplainTask` combines durable terminal posture, current host connectivity, and
fresh registered-worktree inspection. It exposes closed recovery reasons with
reachable reason-specific next actions; their operator-facing meanings are
documented in [running.md](running.md).

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

The typed local client and strict handler expose the canonical task mutations:
preparation, reconciliation, handback, and cleanup, alongside the on-request
lifecycle and intervention set — pause, resume, cancel, verify, promote, replace,
steer, and the operator-only discard. Each is idempotent under its stable
operation ID and reconciles rather than re-sends an uncertain outcome.
Preparation contains only task-contract fields: the
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
`read` classification. The official-SDK facade exposes the same task-control
surface as the CLI and typed local client; `reconcile_task` and the other
non-read-only tools are idempotent, closed-world, and use the same stable result
and side-effect semantics across all three paths.

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
host release, closes and removes the exact service-owned runtime attachment, and
then verifies again before removal authorization. Open cleanup holds remain a
fail-closed pre-release blocker; the operator surface identifies only that closed
category and never exposes the hold's free-text reason. Unresolved decisions have
their own closed retryable precondition and operator hint, without exposing
decision content. Active execution, unknown execution authority, dirty worktrees,
and stale pull-request truth likewise retain separate content-free messages and
condition-specific retry guidance.

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
binding are reconstructed after a service restart only when the recorded runtime
directory, socket, and relay identities still match. Ambiguous ownership preserves
the filesystem objects, moves an affected live task to `unknown`, and exposes a
closed recovery explanation instead of granting cleanup or relaunch authority.

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

Task explanation reads the latest durable candidate judgment while validation is
in progress as well as after failure. Operator-facing candidate diagnoses are
documented in [running.md](running.md).

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
source, and binds the exact protected mounted path, its matching target name,
and the public untrusted relay identity to three fixed reporter environment keys. Task, run, and lease identity,
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

Both families answer the same intervention contract, which is what makes the
adapter boundary a frozen contract rather than one harness's shape. Reaching a
running worker returns a reviewed plan the service performs through its scoped
host control operation; no adapter owns a terminal. An instruction is delivered
only into an affirmatively empty composer, and a pending or unknown composer
defers instead of injecting. The instruction is typed exactly once while the
submission keystroke alone may be retried, since a resent instruction is a
second instruction the worker cannot distinguish from the first. A slash, skill,
or mention invocation waits on a longer harness-specific pause, because
submitting into a picker that is still resolving selects an entry instead of
sending the line. An instruction carrying its own newline, a control sequence,
or more than 8192 bytes is refused rather than sanitized. Pause and stop carry
no operator text and use distinct keystrokes, and every unconfirmed submission
is reconciled rather than silently resent.

Each family also answers for its own readiness and process attribution. Profile
validation is delegated to the reviewed catalog rather than re-checked per
family, so dispatch and the adapter cannot disagree about which profiles exist
and which shapes each allows, and a refusal names whether the profile belongs to
another family or simply disallows the shape. A diagnosis reports the settle
signal separately from availability, because an installed, pinned, reachable
harness can still be unable to prove a turn ended and only the unattended
decision depends on that second fact. Process roles are assigned solely from
exact attribution: an unattributed observation, a missing task, source or
executable label, or a foreign profile is `unknown` with the reason named, since
a role pinned to the wrong process is what makes an unrelated program look like
task state.

Resuming a paused worker reuses the family's own reviewed launch descriptor and
replaces only its trailing bootstrap, so the executable, argument vector,
environment allowlist and attachment binding stay exactly what launch reviewed
and resuming cannot become a second, less-examined way to start a worker. The
resume bootstrap names the exact head the worker left and tells it the tree
already holds its own unfinished work. Resume is refused without that head:
E0 returns a worker through the worktree rather than a vendor session, so the
head is what proves the tree did not move under it.

Neither family reports a lifecycle integration it cannot prove. An unverified
settle signal yields no artifacts and a named reason rather than a best-effort
hook, because a hook that looks installed but emits nothing reads as evidence
that a turn ended. Usage is reported only when a producer emitted it: absence,
staleness, a half-counted turn and a negative count are all unknown with a
reason, never zero.

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
