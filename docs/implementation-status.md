# Implementation status

`comis-dev-crew` is pre-release work spanning its E0 foundation and staged
post-E0 capabilities. This page records, subsystem by subsystem, what is actually
implemented and what is deliberately not claimed. It is maintained alongside
the behavior it describes.

## Summary

The service owns durable SQLite state and a strict owner-only local API. The
operator CLI provides service, fleet, task, initiative, backlog, operation, and
worker-profile views alongside task lifecycle commands, initiative controls and
candidate integration, durable backlog intake and promotion, the operator half
of approval-bound merge, and the acknowledged operator-only discard. The
protocol foundation pins the 43-artifact Comis capability-service contract at
source commit `4deb33ed59b272d4a84046a20a7f51a615f06039` and bundle digest
`dea251a955a4d68faf402aa6977db1b4544737e43aa1f624f39dc359008f6414`, and generates
a closed Go adapter that can consume an exact one-shot approval receipt.

Installed composition supervises the Comis control lane, Codex and Claude Code
launch descriptors, candidate validation, forge truth, delivery, unknown-task
reconciliation, handback, safe cleanup, and approval-bound pull-request merge
authority. Unattended worker settling is not claimed.
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
That foundation established the engineering protocol, verification contract, CI
foundation, initial E0 domain records, pure-Go SQLite store, canonical read
application handlers, bounded newline-delimited local protocol over an owner-only
Unix socket, initial read-only operator CLI, and authenticated Comis protocol pin
with generated DTO, validation, and Unix control client support. Later staged
capabilities reuse those boundaries rather than creating alternate authorities.

The protocol join gate is implemented for protocol
`comis.capability-service/1`, including attention-response, workspace-lease,
terminal-event, and execution-attachment control scopes. No network-exposed
public operator transport is claimed; mutations remain on the owner-only local
API.

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

Operator cancellation can also settle an `unknown` task when durable evidence
proves there is nothing left to stop: the recorded terminal must belong to the
task's exact managed run and workspace lease, its latest trusted posture must be
`exited` or `released`, and no validation process may remain active. The task is
then reconciled to `cancelled` without releasing its worktree, artifacts, run,
lease, or execution attachment. Threat posture: missing or contradictory
terminal authority, a lost or active terminal, and active validation all refuse
the mutation and preserve `unknown`; cancellation never converts process
uncertainty into a claim of safe settlement, and discard remains a separate
explicitly acknowledged operation.

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
An uncertain attention send leaves the decision due and is retried on the next
supervisor tick without stopping the service. Durable ledger read or write
failures still stop supervision because continuing without authoritative state
could record or omit the wrong airing.

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

## Task history

One task's history is readable from three durable sources that are never blended:
the reports its worker authored, the task's slice of the service event log, and
the reviewed validation programs that ran. They stay apart because they carry
different authority — a worker entry is a claim, a service entry is a durable
fact, and a validation entry is what actually executed — and the precedence model
depends on an operator being able to tell them apart.

Nothing new is stored to serve this: each source is a projection of a record the
service already keeps, so the history cannot drift from the state it describes.
Worker text was bounded and rejected for control characters at report acceptance,
so it is safe to render, and validation entries expose only the sanitized
executable label. All three share one monotonic cursor, so following behaves
identically across sources.

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

## Boundary records

The three seams an outside actor reaches — the local API, a worker's reporter
endpoint, and the Comis control connection — each write one structured line per
crossing to standard error, at a level selected by `--log-level`.

The record is a closed struct, not a set of caller-supplied fields. That is the
design rather than an implementation detail: "never log a brief, objective,
report, diff, path, argument or credential" is a rule every future call site
would have to remember, while a closed record has no field those could occupy.
Failures reuse the same closed error kinds and operator hints callers receive,
so failures group identically across all three seams.

This is diagnostic output and not durable history. Transitions live in the event
stream and refusals in the audit trail; both survive a restart, and a lost log
line costs an operator context rather than a fact.

## Audit trail

A refused cleanup and a rejected reporter credential are recorded as a durable
append-only audit trail, separate from the transition log.

The separation is the point. The transition log records transitions, so a
refusal — which changes no state — is invisible there by design, and the
threat the reporter endpoint exists to stop would succeed or fail with equally
no trace. The refusal record is written outside the transaction it describes,
since that transaction rolls back; the credential rejection carries the task
addressed and nothing of the credential presented.

The trail is operator-only, unlike the event stream. Every column is an
identity, a closed discriminator, a sequence or a time, so the record stays
content-free while still naming the ground it was refused on.

## Reconciliation survey

Which unknown tasks a reconcile would accept is readable from the operator
console. Each task is classified against the same evidence the reconcile command
requires — durable authority, terminal settlement, absence of prior candidate
recovery history, worktree verification, cleanliness, and whether a commit exists
ahead of the pinned base — using the read-only half of the reconciliation
inspector. Existing candidate or reconciliation history is classified as
incomplete authority instead of offering an action the commit boundary will
refuse.

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
The candidate supervisor consumes this same port as machine evidence rather than
trusting a worker's prose description of which files changed.

## Comis adapter

The adapter contains the supervised persistent bidirectional connection used by
the installed service composition. It authenticates the exact pinned handshake;
dispatches managed-run and managed-run-group activation and abandonment,
managed-run cancellation, and terminal events; reads exact group host rollups;
and carries liveness, reports, evidence, attention-response receives, exact
approval-receipt consumption, and workspace release on the same socket. It
reconnects with bounded backoff.
Wrong credentials, altered operation envelopes, unknown fields, excess
concurrency, and forged run references fail before handler authority. The adapter
does not retry an uncertain report itself.

A separate stateless forwarder polls the durable outbox, maps the closed worker
vocabulary to the pinned Comis report vocabulary, and retries uncertain outcomes
with the same operation and service-report identities until it can durably record
an exact host acknowledgement. It is implemented as an independently supervised
adapter and participates in the installed service lifecycle.

The service lifecycle gives the one supplied control connection, its bounded
forwarders, and both local endpoints explicit cancellation and joined completion
paths.

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
already-unknown operations are preserved. A `candidate_complete` task is also
preserved when exactly one worker report or one completed reconciliation owns its
accepted sealed evidence and exact durable publications. The service can therefore
resume the remaining host evidence or report delivery after restart. A repeated
restart is idempotent.

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
report merely to close the state machine. Instead, a separate durable outbox emits
one service-owned `candidate_complete` projection after both evidence publications
are acknowledged, so Comis can reduce the same verified terminal outcome. Its
identities and acknowledgement are exact-replay safe across restart. Startup
backfill accepts only one completed reconciliation with accepted evidence and two
matching publications; incomplete or ambiguous authority stops startup rather than
claiming success. The terminal projection remains retryable after cleanup because
cleanup cannot revoke a previously accepted outcome. Incomplete recovery history
remains unresolved after restart and refuses a second reconciliation record.

The normal candidate supervisor uses the same server-owned handoff before it
validates a task that already has an accepted worker candidate report. That report
has already moved the task into `validating`, so no separate recovery mutation is
needed. Its handoff authority reads the exact durable task, preparation, and
preparation operation without borrowing the recovery reader's terminal-settlement
precondition; terminal evidence remains mandatory for unknown-task recovery and
cannot be weakened by normal validation. The supervisor derives every Git identity
from the durable preparation, requires the promoted snapshot to match a fresh host
inspection, and runs no validation or forge operation when those authorities differ.
It then compares the complete bounded base-to-head diff with the immutable exact
and prefix path rules in the resolved validation profile. Both sides of a rename
must be allowed. A disallowed path fails only that task before local commands,
artifact inspection, forge activity, or evidence publication; truncated or
internally inconsistent diff evidence remains unknown and cannot authorize delivery.
A task without an accepted candidate report still requires the explicit unknown-task
recovery flow.

The private Git handoff is a high-risk boundary. Paths come only from the
registered worktree and its canonical Git administration, and the source record,
generated configuration, inert commit identity, copied worktree controls, branch, clean index, and base
ancestry must all match. Symlinks, executable Git configuration, alternate object
indirection, dirty content, or divergent shared/private history are refused before
any host branch mutation. When server-owned integration application advanced the
shared task branch before worker launch, the private candidate must be a strict
fast-forward of that exact shared head. Promotion imports no tags or submodules,
advances the exact branch with an old-head compare-and-swap, and synchronizes only the worktree index;
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

## Initiative and backlog durability

Multi-component initiatives and bounded backlog requests are stored in the same
owner-private SQLite database as task authority. Each write validates the closed
domain record and commits in its own transaction; duplicate stable handles are
conflicts, malformed stored JSON fails closed, and deterministic reads validate
the reconstructed record before returning it. Backlog rows contain request and
readiness data only and have no managed-run, workspace, credential, terminal, or
delivery authority.

Backlog intake is an idempotent mutation rather than a direct table insert. The
service replays the exact item before minting another handle, requires every
named dependency to exist, and commits the item with its completed operation at
one global state version. A missing dependency or operation-write failure rolls
back the whole addition, including across restart.

Threat posture: an addition may describe a bounded desired outcome and refer to
existing backlog handles, but it cannot select a worktree, credential, terminal,
delivery route, or managed run. Initial readiness is limited to `ready` or
`needs_refinement`; terminal backlog postures cannot be forged at intake.

Backlog promotion reserves one ready item to one parent operation and one
deterministic child preparation operation before a worktree is allocated. The
reservation and its original timestamp survive restart. Only dependencies whose
durable backlog rows are already `promoted` satisfy the reservation. Finalization
then rereads the exact completed `PrepareTask` operation, moves the item to
`promoted`, records the item-to-task link, and commits the parent operation in one
transaction. A crash before preparation, after preparation, or during final
operation persistence can be retried without minting a second task.

Threat posture: promotion inherits repository and task shape from the reserved
item and prepends its requested outcome to the task acceptance contract. The
caller can complete validation, worker, delivery, and revision fields, but cannot
retarget the request or supply a worktree, credential, terminal, attachment, or
managed-run identity. Competing promotion operations are refused before task
preparation begins.

The strict local boundary exposes `AddBacklog` and `PromoteBacklog` as
idempotent `mutate` operations to both operator and MCP caller classes. Addition
accepts only bounded request fields. Promotion accepts the backlog handle and
the remaining normal task contract, while repository and shape stay owned by
the durable item. Unknown workspace, task, attachment, and managed-run fields
are refused during strict decoding. The trusted local promotion result retains
the private managed-run preparation for the MCP adapter and distinguishes the
child task version from the later parent promotion version.

The writable service composes both backlog coordinators from its sole SQLite
store and clock. Addition handles are stable hashes of the configured service
identity and operation ID. Promotion delegates its child creation to the same
reviewed task mutation coordinator used by `PrepareTask`, so repository,
workspace, attachment, validation, and worker checks cannot diverge between
ordinary preparation and backlog promotion.

Initiative preparation validates the complete caller-local graph and every
member contract before allocating a workspace. It then records stable member
intents, prepares each reversible worktree and task-scoped runtime attachment,
and commits the unbound initiative, all member tasks, all private activation
joins, and their replay outcomes in one transaction at one state version. A
partial allocation failure preserves the intents and already-created reversible
artifacts for exact retry, but writes no half-initiative and launches nothing.
Exact preparation replay reconstructs the original `preparing` initiative and
`prepared` task projection from the completed operation even after activation
has bound the live records. It clears only later activation bindings and restores
the operation's version and timestamp; the private preparation records retain
their live closure state, so abandoned authority stays closed. Altered reuse is
still audited and rejected as a conflict.

The running service exposes `PrepareInitiative` to operator and MCP caller
classes through the strict local boundary and the same reviewed preparation
dependencies used by standalone tasks. The boundary supplies its own operation
and service identities, refuses caller-supplied host authority fields, and
returns the exact private group preparation only when every prepared member and
durable operation agree on the initiative identity and state version. Contract
artifacts are durable byte records owned by one initiative producer; preparation
derives and stores their digest and refuses any consumer pin or artifact edge
that does not resolve to the exact handle, kind, producer, and digest.

Initiative list, detail, dependency graph, and backlog list projections read
their records and advertised state version from one read-only SQLite snapshot.
State and backlog-readiness filters reject unknown vocabulary instead of
returning an ambiguous empty list. Detail reads require every durable member,
carry the graph's source/confidence/completeness envelope, and return closed
non-executable next-action identifiers. The running service publishes these
through the strict local boundary as `ListInitiatives`, `GetInitiative`, and
`ListBacklog` read commands to both operator and MCP caller classes while
refusing fields outside their narrow scope.

The stateless MCP facade maps `prepare_initiative`, `get_initiative`,
`backlog_list`, `backlog_add`, and `backlog_promote` to those canonical commands.
Preparation, addition, and promotion are marked `mutate`; both reads are marked
`read`. Addition provenance comes only from authenticated call context, and the
promotion schema contains no repository or shape field. The complete private group join is validated against
the pinned protocol schema, including each canonical public relay identity, and
returned only in the MCP result extension, while
the model-visible preparation result contains bounded initiative and task
identities but no registration nonce or host resource path.

The operator console exposes initiative list, show, explain, and graph reads
through that same canonical local client. Human views retain dependency
readiness and closed safe actions; graph JSON is the graph DTO itself rather
than a second wrapper contract.

The operator console also exposes `backlog add` and `backlog promote` through
strict bounded file-or-stdin JSON contracts. The promotion target appears only
on the command line, and both mutations return the canonical local JSON result.

Initiative pause, resume, and cancel coordination reuses the existing task
mutation path with a deterministic operation identity per member. The result is
explicitly non-atomic: every member is reported as completed, rejected, unknown,
or not attempted. A separate durable group-operation record preserves that
whole answer for exact replay, including across restart, so a later state change
cannot rewrite what an earlier control request actually observed. Completed
member claims are accepted only when the durable member operation names the
expected command, task, and state version. The replay envelope stores the exact
member count and fails closed if any result row is missing.

The strict local boundary exposes `PauseInitiative`, `ResumeInitiative`, and
`CancelInitiative` only to the operator endpoint. Each accepts only an
initiative handle, validates the complete durable result before projection, and
returns the per-member outcomes with a `mutate` classification. The MCP endpoint
refuses all three commands before dispatch.

The running service composes those group controls from the same task mutation,
workspace-inspection, and SQLite authorities used by task-scoped pause, resume,
and cancel. If workspace inspection is absent, pause and cancel remain available
while resume returns an explicit retryable unavailable result.

The operator console exposes bounded initiative watch, pause, resume, and cancel
commands. Watch advances through the content-free service event cursor and
refreshes canonical detail on every pass; mutations emit the full durable
per-member JSON result and accept no caller-selected member set.

Threat posture: a group command carries only an initiative handle. It cannot
select an unowned task, forge member operation identities, or collapse a partial
distributed outcome into success; the authoritative member set is reread from
one store snapshot before execution and again before the replay result commits.

Group activation validates the private group nonce and the exact complete member
set under the SQLite write lock. It commits the host-managed group identity and
every run, lease, and execution-attachment handle atomically at one state
version. Runtime attachment binding begins only after that commit. If any local
binding remains uncertain, the response reports the outcome per member and the
initiative becomes durable `unknown`; only an all-completed result remains
`active` and eligible for later scheduling. The persistent Comis control session
negotiates `managed_run_group`, strictly validates the generated group request,
and returns those same per-member outcomes over the authenticated socket.

Group abandonment carries the exact host-minted member IDs and private member
nonces, so an unbound preparation can be closed without inventing authority.
The service joins the complete member set under the SQLite write lock, records
the initiative, task, preparation, operation, and replay-stable member outcomes
in one transaction, and reports uncertainty per member. Reap-safe cancellation
enters the reversible cleanup path; preserve retains prepared artifacts while
the initiative remains non-launchable and `unknown`.

Initiative scheduling is a deterministic fleet-wide decision. Existing workers
consume host, repository, and reviewed worker-profile capacity first; remaining
slots are offered one member per initiative per round in stable creation order.
Members that have already left the prepared or ready state retain their
initiative's durable round position when the schedule is recomputed, so an
older initiative cannot reset to round zero and take a second slot before a
later initiative receives its first.
Only `ready` members of an `active` initiative can be selected. Every held ready
member carries one closed reason: `dependency_blocked`, `resource_queued`,
`contract_stale`, or `integration_held`. Contract consumers must still pin a
handle listed by the initiative as current, integration waits for exact candidate
states, and a failed predecessor blocks only its dependent descendants. The same
decision derives the initiative aggregate state without treating a missing or
reconciling member as healthy. Aggregate derivation treats a dependency-ready
member as progress before capacity allocation, so one completed component cannot
trap an unstarted independent sibling behind the integration owner's expected
hold. Member state mutations update that aggregate in the same SQLite transaction
and at the same global state version; an aggregate write failure rolls back the
task, operation, and event with it. A durable `unknown` initiative is never
reactivated by derivation after restart — only the explicit host reconciliation
path may restore its authority.

Threat posture: aggregate progress comes only from the scheduler's validated
dependency and contract decision. It does not bypass the transactional capacity
recheck, reactivate an unknown initiative, or make a held integration owner
launchable.

The canonical fleet projection publishes the same reviewed concurrency limits
alongside exact durable usage. Host, observed-repository, and configured-profile
dimensions are sorted and independently marked saturated, so normal status JSON
and table output identify the actual limiting scopes without a database read.

The launch boundary does not trust that projection as a reservation. For an
initiative member, the `ready` to `launching` transaction rereads every durable
initiative and task, recomputes fair allocation under the reviewed host,
repository, and worker-profile ceilings, and refuses the mutation unless that
exact member is selected. Missing scheduler configuration, a newly stale
contract, a newly blocked dependency, or capacity consumed after an earlier read
therefore commits no task, operation, or state event. Standalone task starts keep
their existing path and still count against initiative capacity.

Startup reconciliation now includes every nonterminal initiative. Preparing,
active, blocked, integrating, validating, and candidate-complete initiatives are
atomically moved to durable `unknown` with a new global state version before the
service advertises readiness. Delivered, failed, cancelled, and already-unknown
initiatives remain stable, and replay is idempotent. A corrupt initiative aborts
the whole reconciliation transaction, so no subset can be presented as recovered.

After the authenticated Comis control session is available, startup attempts a
second, narrower reconciliation for bound `unknown` initiatives whose complete
member set belongs to the current service instance. The service reads the host's
content-free managed-run group rollup on that persistent session and compares the
exact managed-run identities plus all nine host state counts with current durable
task rows. When a member has a currently forwardable durable Comis report or
evidence publication, an older host projection can be a temporary egress lag.
Only in that case, startup refreshes both local rows and the host rollup at the
normal report poll interval within the existing per-group deadline. Preserved
cancelled or unresolved-task evidence and a candidate report held behind
ineligible evidence do not grant retry time. The global evidence forwarder admits
only candidate-complete, delivering, or delivered tasks, so one unresolved task
cannot block a later task's publication. A group with no forwardable egress
receives no projection retry. Only an exact settled match may atomically
restore the aggregate state derived from those rows. A foreign service instance,
missing or duplicate member, unexplained changed state count, stale local
snapshot, unavailable host read, or aggregate that still derives to `unknown`
leaves the initiative unchanged. Readiness waits for each eligible group attempt,
with a bounded per-group deadline, but a preserved `unknown` group does not
prevent unrelated work from being inspected. A failed recovery boundary record
names the opaque initiative and group, attempt count, closed mismatch class, and
both complete content-free state-count projections. The next occurrence can
therefore be diagnosed from one structured log line without joining the two
databases by hand.

Threat posture: nested initiative graphs and backlog dependencies are encoded as
data, never executable input, and are revalidated after decoding. Only the
single-writer service process opens the mutable store. Restart cannot silently
resume initiative authority: ambiguous nonterminal coordination is downgraded to
`unknown`, and corrupted durable state prevents readiness rather than broadening
run or scheduling authority. The host rollup cannot mint local authority by
itself: its service scope is fixed by the authenticated session, and the final
SQLite transaction rechecks group identity, complete membership, current service
ownership, local state counts, snapshot version, and monotonic time before the
initiative can leave `unknown`. Pending egress grants only bounded retry time; it
does not relax any recovery comparison or transaction precondition.

## Integration candidate application

Candidate application is a reserved single-writer operation. A caller names the
initiative, its recorded integration owner, a component task, the component's
exact candidate head, and the expected integration head. It cannot supply a
repository path, Git command, shell fragment, or strategy. The initiative's
operator-owned policy resolves to the closed `merge`, `rebase`, or `cherry_pick`
vocabulary, and SQLite rechecks that policy and owner before reserving the
operation.

The reservation resolves distinct task worktrees from durable preparations and
requires current accepted candidate evidence whose repository, base, task, head,
and expiry still agree. Candidate-complete, delivering, delivered, cleanup-held,
and cleaned predecessors satisfy the same dependency rule used by scheduling and
the initiative graph; host report acknowledgement is not required after accepted
candidate evidence. A dependency-ready integration owner may receive those
server-owned applications while it is still `ready`; this keeps Git application
and conflict materialization ahead of the confined worker launch. A launched owner
remains writable only in its explicit working, decision, or blocked states. The Git
registry then revalidates both worktree identities,
cleanliness, and heads while holding its mutation lock. Fixed argv performs the
selected operation with hooks and signing disabled. Rebase applies the candidate
range from its frozen base onto the current expected integration head, then
compare-and-swaps the integration branch; it never rebases existing integration
commits onto a later component. Applied heads and sorted,
bounded conflict paths are durable records; conflicts remain in the dedicated
integration worktree for an actionable resolution. The integration worker may
edit only those paths, but it preserves the server-staged non-conflicting
candidate changes and commits the complete index. A path-limited conflict commit
that leaves candidate changes staged cannot pass clean-candidate handoff.

Content-free Git refs bridge the interval between a Git result and its SQLite
commit. Exact applied and conflicted calls replay without repeating Git. A crash
before a receipt is written leaves the reserved operation and changed worktree
ambiguous, so the retry refuses instead of inferring success. A crash after the
receipt or after SQLite completion replays the one exact result. Completion and
the canonical operation ledger commit in one transaction, and accepted evidence
expiry blocks a new mutation without invalidating a result already completed.
Another operation for the same candidate task and head is rejected before Git
and before a second reservation is inserted. Its typed precondition directs the
caller to the original operation or its applied or conflicted receipt.

The closed local service protocol exposes `ApplyIntegrationCandidate` to the
operator and MCP caller classes as a mutation. Its request contains only the
initiative, integration-owner task, candidate task, and exact heads. Its result
projects the reviewed strategy, evidence digest, applied head or bounded conflict
paths, and durable state version without exposing either worktree path or the
candidate base path.
Candidate head or cleanliness drift completes as the third closed outcome,
`invalidated`. The Git adapter performs no mutation, and SQLite atomically
records that outcome with the affected candidate's transition back to
`validating`, whether the accepted evidence was still sealed as
`candidate_complete` or had already reached `delivered`; sibling candidates and
the integration owner are untouched. Delivering, cleaned, and every other task
state remain outside that invalidation authority.
Exact replay returns the durable invalidation without re-entering Git.
If automatic revalidation receives an incomplete process receipt, the service
diagnostic names only the closed mismatched field (for example `profile_id` or
`output_hash_length`). It never emits the receipt, process output, or task
content, so one service diagnostic identifies the broken contract safely.
The official MCP facade exposes the same operation as
`apply_integration_candidate`, marks it idempotent and mutating, and keeps policy,
strategy selection, repository paths, and argv out of its input schema. A new
application uses the authenticated call operation, while transport uncertainty
retries that exact operation automatically. For a staged rebase conflict, the
optional `recoveryOperationId` names the immutable conflicted receipt and the
authenticated call supplies a separate durable resolution operation. The service
revalidates the original target ref and rebase sequencer, advances the branch by
compare-and-swap, and reattaches the worktree; changed or incomplete state is
preserved and refused.
The operator CLI reaches the identical boundary through `initiative integrate`
and rejects authority-bearing or self-retargeting contract fields before opening
the service socket.

Integration-owner completion is also provenance-gated. A `candidate_complete`
report is accepted only after every incoming `integrates_after` predecessor has
a completed `applied` or `conflicted` application receipt bound to that
initiative, owner, and predecessor's latest accepted evidence. A direct terminal
cherry-pick therefore cannot make an initiative look delivered, even if later
validation would pass the resulting tree.

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

The typed local client and strict handler are the canonical mutation boundary for
task, initiative, backlog, integration, and merge operations. The task set
includes preparation, reconciliation, handback, cleanup, pause, resume, cancel,
verify, promote, replace, steer, merge, and the operator-only discard. Each is
idempotent under its stable operation ID and reconciles rather than re-sends an
uncertain outcome. An
independently acknowledged discard retry resumes the one durable discard hold
after a staged failure, while exact task, repository, and worktree identity
remain mandatory. Dirty or unpinned contents carry no delivery authority, an
ordinary cleanup hold cannot be converted into a discard, and original and retry
receipts are both classified as `DiscardTask`. A retry operation ID owned by a
different command is refused before resuming any host stage.
Threat posture: every retry must pass the external acknowledgement gate again;
resumption cannot change the task, repository, worktree, or release authority,
and it cannot turn discarded contents into delivery evidence.
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
`read` classification. Shared mutations keep the same stable result and
side-effect semantics, while caller-class checks deliberately keep the catalogs
non-identical: discard and initiative controls are operator-only, and destructive
merge completion requires private Comis approval context that the CLI cannot
supply.

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
Successful attachment retirement first moves the exact recorded inode into a
fresh owner-only isolation directory, verifies the pinned identity again, removes
only the authorized socket, single-link record, generation hard link, or empty
task directory, and synchronizes both namespaces. A completed restart therefore
does not accumulate successful `.devcrew-remove-*` namespaces. Interrupted or
ambiguous retirement remains isolated and is reconciled by exact identity on the
next attempt.

Threat posture: cleanup never recursively walks or removes a task directory and
never follows a link. Generation-link removal requires the durable generation
directory, anchor inode, task link, link count, mode, and owner-private namespace
to agree. An unexpected child, special node, replacement, unsafe mode, identity
change, or synchronization failure preserves the isolated object and refuses
cleanup. This bounds restart resource use without widening worker or model
authority and without converting an ownership ambiguity into deletion authority.
The authenticated Comis control connection starts only after this attachment
recovery finishes, so host reconciliation observes the reconstructed socket
identity rather than an inode that the same startup is about to replace.

Non-decision report commands render the durable acceptance line followed by
`PauseRequested=true` and `Instruction=<plain text>` only when those control
fields are present. Instructions are bounded and revalidated before stdout. A
decision report keeps stdout private-response-only and therefore does not
consume a queued instruction; the next ordinary report delivers it exactly once.

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
its branch, head, base, state, URL, and check conclusions. The sealed forge
evidence retains that exact branch so a later merge never derives authority
from a naming convention. Scout delivery reads only the
reviewed bounded artifact. Both use durable outbox identities for exactly-once
host delivery across restart.

A validation process that is durably absent before it can produce a receipt
leaves its task validating and is retried with a fresh operation identity. The
absent attempt cannot contribute evidence, while a malformed purported receipt
still stops supervision as an invariant failure. This keeps transient process
admission failure from restarting the service without weakening receipt checks.

The reviewed Codex and Claude launch bootstrap prohibits pushes and Git-remote
changes without changing persisted brief bytes. Workers produce and report
task-local commits; only the service may select the configured remote and use its
scoped delivery credential after candidate verification.

Task explanation reads the latest durable candidate judgment while validation is
in progress as well as after failure. Operator-facing candidate diagnoses are
documented in [running.md](running.md).

Forge API and pull-request truth and branch push use distinct credentials. HTTPS
token push is supported, and an SSH route allows a repository-scoped deploy key
to be the push identity. The latter decodes the owner-private key only into a
transient `0600` file, invokes the canonical OpenSSH executable through fixed
service-owned argv, pins host keys, accepts only the configured Git receive or
upload command, and removes the key before returning.

The candidate configuration may also enable a third, owner-private merge
identity with one immutable `merge`, `squash`, or `rebase` method. Its file path
must differ from both ordinary identities, and its contents are intentionally
not read by installed composition. Only the merge adapter resolves it, after
fresh exact-head, required-check, and matching branch-protection reads. The
application coordinator consumes the exact authenticated Comis receipt and
SQLite atomically reserves current accepted evidence, records the complete
approval before forge mutation, and joins exact post-merge truth to the same
operation. A recorded mutation intent first performs read-only outcome
reconciliation; when the pull request is still open, every retry revalidates
the approval against a fresh UTC clock before the forge mutation, so an expired
receipt cannot authorize a later merge. Pending approval and recorded mutation intent survive startup
reconciliation; altered replays, stale evidence, split ledger writes, and
unprotected branches fail closed. The canonical local API exposes one
`MergeTask` mutation to both protected endpoint classes: operator calls can
carry only the task handle, while MCP calls must bind the approval request and
the identical operation ID; neither can choose forge coordinates or method.
Installed composition now joins that mutation to the sole SQLite writer, the
persistent authenticated Comis connection, and the separately credentialed
forge adapter only when all three authorities exist. The operator CLI now
reserves exact evidence through `task merge TASK` without accepting approval or
forge fields. The destructive `merge_task` MCP tool accepts only the task handle
and obtains the approval request, managed run, and matching operation from the
private schema-validated Comis call context. It exposes success only after
validating an exact durable completion and replays the same merge transaction
after an uncertain transport outcome. Neither surface can submit forge
coordinates or select a merge method.

Threat posture: the model can name only an opaque task. Public approval, forge,
head, credential, and method arguments are rejected before the local service is
called. The private Comis context must contain a schema-valid approval request,
managed run, service instance, and stable operation; the coordinator then
consumes the host receipt against store-resolved current evidence before the
separate merge credential is resolved. A lost reply replays only that same
durable transaction, and malformed, pending, or mismatched completion data is
reported as an internal failure rather than success.

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
head is what proves the tree did not move under it. Resume persists that head
with the exact ready-state generation, and both launch-plan reads and terminal
creation select the resume bootstrap only while that generation is current.
The transition returns to `ready`, then follows the ordinary authenticated
`launching` and worker-acknowledgement path; it never claims `working` while the
paused terminal is already gone. A clean lease-private worker commit is verified
and promoted through the same exact branch handoff used for candidate recovery
before the resume generation is recorded. Actual developer edits remain dirty
and route to handback. Every ready generation receives distinct durable start
and wrapper-acknowledgement operations, and a settled terminal binding may rotate
only when the next authenticated `created` event arrives for that launching
generation. Earlier acknowledgements therefore cannot advance a resumed worker.
Replacement rotates the protected socket's pinned brief, reporter scope, and
acknowledgement operation together, while preserving its exact task, run, lease,
workspace, and attachment authority.

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

## Deliberately excluded from the E0 foundation

Two E0 exclusions remain worth stating because each would have been easy to add
badly. The process projection remains absent; the landed-proof boundary has since
become reachable.

**There is no `task processes` projection.** A per-process view is meant to join
what this service launched with what the host observed beneath the task's
terminal, and only the first half has a source here. The service registry covers
validation processes it started itself; nothing constructs a terminal-descendant
observation, because E0 has no durable process-observation contract to construct
one from. A command rendering half that join would read as a complete process
list and quietly answer "nothing else is running" whenever the missing half was
the interesting part. The validation half is reachable through the task views;
the joined projection waits for the contract that makes it honest.

**Cleanup still proves delivery; the landed proof exists beside it.** A worktree
is removable when its recorded pull request is open at exactly the evidence head
with every required check passed, or when a report artifact hash is recorded —
plus a clean tree. That rule is unchanged.

What changed is that work can now land. With `merge_after_approval` and a
separate merge credential, the three reachability questions became answerable,
so the proof they need is built and tested: reachability from any
remote-tracking branch including a fork remote, a merged pull request looked up
BY HEAD BRANCH so a missing local record never refuses on its own, and
containment in an up-to-date default branch for the
squash-merge-then-delete-branch case. Unreadable forge truth refuses rather than
letting a later route answer a question the earlier one never asked, and every
refusal names the evidence gap.

Cleanup consults the proof in exactly one place: where the delivery rule cannot
answer at all, having found neither a recorded pull request nor a report
artifact hash. That case used to refuse outright, and a missing record is not
evidence that nothing landed — a squash merge that deleted the branch leaves
precisely this state. The consultation can only turn that refusal into an
acceptance, never the reverse, so every removal the delivery rule already
refused is still refused.

A deployment that configures no evidence source keeps the earlier behaviour
rather than acquiring a route it never opted into, and a gatherer that fails is
not a cleanup failure — it established nothing, and nothing is not proof.
Gathering needs read authority only, stated in code, so the merge credential
cannot drift into a path every cleanup runs.

**Process signals are not exposed.** No interrupt, terminate, or kill verb
exists. Stopping a task's execution runs through terminal lifecycle rather than
process control, so no surface accepts a process reference as authority.

## Design record

The detailed design and ratification record is maintained privately by the
maintainer and is not part of this repository. The binding constraints a
contributor needs are reproduced in [AGENTS.md](../AGENTS.md), which is
self-contained and authoritative for this tree.
