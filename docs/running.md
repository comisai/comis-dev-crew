# Running comis-dev-crew

This is the operational reference for the four executables. It describes what the
current pre-release build actually accepts. Nothing here promises a supported
production deployment; see [implementation-status.md](implementation-status.md)
for what is and is not implemented.

## Read-only service

The minimal read-only service runs with explicit canonical paths:

```text
devcrew-service --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

The service creates its final state and runtime directories as owner-only, stores
the database as owner-only, and refuses relative, non-canonical, symlinked,
broad-root, non-regular, live, or identity-ambiguous targets. Without explicit
flags, `devcrew-service` and `devcrew` derive the same paths under the operating
system's user configuration directory.

## Full Comis and coding-worker lane

Prerequisites, all of which fail closed if unmet:

- The 32–256 character instance bearer is in an owner-private (`0600`) regular
  file.
- The Comis control socket already exists as an owner-only Unix socket.
- The primary checkout and the worktree parent are separate canonical
  directories under the approved root.

Task preparation first resolves the requested worker and validation profiles for
the exact task shape. An unavailable, incompatible, or incomplete profile is
rejected before any worktree or runtime attachment is allocated. Preparation
then creates or exactly adopts its operation-bound linked Git worktree beneath
that parent before requesting the Comis workspace lease. Launch the installed
service without hand-editing generated or runtime files:

```text
devcrew-service \
  --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/operator.sock \
  --mcp-socket /absolute/private/run/mcp.sock \
  --runtime-root /absolute/private/run/tasks \
  --service-instance service-instance-devcrew \
  --git-executable /absolute/path/to/git \
  --approved-root /absolute/repositories \
  --repository-id product-api \
  --repository-primary /absolute/repositories/product-api \
  --worktree-root /absolute/repositories/worktrees \
  --repository-default-branch main \
  --comis-socket /absolute/private/run/comis-control.sock \
  --comis-credential-file /absolute/private/comis.credential \
  --comis-handshake-operation handshake-devcrew-0001 \
  --preparation-ttl 10m \
  --decision-resurface-initial 30m \
  --decision-resurface-maximum 4h \
  --codex-profile codex-reviewed \
  --codex-executable /absolute/path/to/codex \
  --codex-version "codex-cli 0.147.0" \
  --codex-model gpt-5.5-codex \
  --codex-effort high \
  --codex-terminal-allow-entry codex-confined \
  --codex-network restricted \
  --codex-concurrency 2 \
  --claude-profile claude-reviewed \
  --claude-executable /absolute/path/to/claude \
  --claude-version "2.1.224 (Claude Code)" \
  --claude-model claude-opus-4-6 \
  --claude-effort high \
  --claude-terminal-allow-entry claude-confined \
  --claude-network restricted \
  --claude-concurrency 2 \
  --claude-config-directory /absolute/private/claude-config \
  --candidate-config /absolute/private/candidate.json
```

The installed service command validates one explicitly configured primary
repository and linked task worktree before opening its endpoints. It supplies
cryptographically random task and registration identities, advertises that
verified worktree in the managed-run preparation, and binds the same mutation
authority to the dedicated MCP endpoint. It never accepts the protected bearer on
its command line.

`--decision-resurface-initial` and `--decision-resurface-maximum` set how often an
unanswered decision is put back in front of the liaison. The wait doubles from the
initial value up to the maximum and stops growing there, so a question nobody
answered keeps coming back without ever competing with fresh work indefinitely.
Both default to the reviewed cadence of thirty minutes growing to four hours. A
non-positive interval, or a maximum shorter than the initial wait, is refused
before the service opens its endpoints.

The Codex profile is required by the installed E0 composition. The Claude Code
profile is optional but all of its flags are an atomic group. Its executable must
be the canonical regular reviewed artifact and its config directory must be a
canonical owner-private (`0700`) directory. The terminal allow entry exposes only
the required authentication material read-only. Both adapters use fixed argv,
ignore project-level worker configuration, keep task authority in the protected
reporter attachment, and remain explicitly degraded because neither reviewed CLI
provides a trustworthy task-settle signal.

### Candidate configuration

The candidate configuration is a strict owner-private JSON document. It fixes
absolute validation programs, typed argument templates, local and forge checks,
evidence lifetimes, output and polling bounds, and one GitHub route. The route
names distinct owner-private read and push credential files; the service rejects
shared identities. `localFixtureRemoteRoot` permits a `file://` remote only for
an explicitly bounded local test fixture and must be absent for the production
HTTPS route.

Accepted evidence advances a task to `candidate_complete`. A conclusive failed
local or forge check records the rejection and advances only that task to
`failed`; the candidate supervisor remains available for unrelated tasks and a
service restart does not rerun the rejected candidate. Incomplete, pending, or
otherwise unknown evidence stays `validating` and is retried without being
treated as success or failure. Temporary GitHub pull-request truth failures
also leave the task `validating` and are retried without stopping the candidate
supervisor. Other pull-request delivery errors still stop supervision so that
permanent failures remain visible.

A dirty worktree, a head that still equals the pinned base, a structurally
unverified worktree, a reconciliation mismatch, or candidate authority that
drifts during validation is not eligible for delivery. The supervisor seals that
Git posture as unknown, does not call the forge or artifact delivery adapter,
and remains available for other tasks. It skips local validation when the initial
posture is already invalid; if the worktree changes or becomes unverified during
local checks, it seals that drift afterward. While the observed head and
cleanliness are unchanged, later polls reuse the sealed unknown judgment instead
of rerunning validation processes.

Diagnostic reads report validation as `unknown` when a task is already
`validating` but no durable judgment or active validation process can be found;
they never turn that inconsistent posture into `not_started`.

For a worker-reported candidate, the final authenticated candidate report closes
delivery after both evidence publications are acknowledged. For a reconciled
candidate, no worker report is invented: acknowledgement of both server-owned
publications atomically closes the task as `delivered`. A restart preserves that
candidate in `candidate_complete` only when the completed reconciliation, accepted
sealed evidence, and exact pending outbox remain consistent, then resumes the same
publications. Incomplete recovery history becomes unresolved and cannot authorize a
second reconciliation. Cleanup accepts exactly one origin and refuses missing or
ambiguous evidence.

`task explain` reads the latest durable candidate judgment for failed and
validating tasks. It distinguishes required local-validation and forge-check
failures, and identifies an unverified worktree when current Git truth is dirty,
base-equal, or otherwise not a clean non-base candidate. Other terminal failures
retain the generic failed-task explanation.
`task show` and JSON `explain_task` output also include a content-free `evidence`
projection. It joins the candidate head and digest, latest authenticated report,
decision and resolution references, validation status, forge and outbox delivery
references, cleanup stage and open-hold count, and opaque run, lease, attachment,
and preparation-operation identities. Missing authority remains `none`,
`not_started`, or `unknown`; process and custody fields remain `unknown` because
E0 has no durable process-observation contract.

An SSH deploy-key push route uses
`ssh://git@HOST/OWNER/REPOSITORY.git` and additionally requires the canonical
service executable as `sshTransportExecutable`, the canonical OpenSSH binary as
`sshExecutable`, and a regular non-writable pinned host-key file as
`sshKnownHostsFile`. The push credential file contains a base64-encoded OpenSSH
private deploy key. The service materializes it as `0600` only for the bounded Git
operation, invokes fixed SSH argv without a shell, and removes it before return.

## MCP facade

Expose the replaceable official-SDK stdio MCP facade to the MCP client as a
separate process:

```text
devcrew-mcp \
  --socket /absolute/private/run/mcp.sock \
  --service-instance service-instance-devcrew
```

The facade defines twenty tools: `prepare_task`, `promote_scout`,
`reconcile_task`, `handback_task`, `cleanup_task`, `discard_task`, `pause_task`,
`cancel_task`, `resume_task`, `replace_worker`, `steer_task`, `verify_task`,
`attest_scout_decisions`, `sync_primary`, `list_tasks`, `get_task`,
`explain_task`, `get_launch_plan`, `worker_profiles`, and `doctor`. `promote_scout` returns the
same private managed-run registration metadata preparation does, because it
mints a task the same way.
`cancel_task` is destructive — it ends work an operator asked for and repeating
it does not undo that — but it is not removal.
`discard_task` is the removal a cancelled task has no other route to: cleanup
requires delivery evidence that a task which never delivered will never have.
It removes uncommitted work permanently and takes the operator's explicit
`acknowledged` argument, which is the only gate it has.
`attest_scout_decisions` records the liaison's inventory of a scout's still-open
human decisions. Only a model can inventory decisions from prose, so the service
never derives this and never infers it from silence: the finding is a stated
choice of `open_decisions` (with the keys that remain) or `no_open_decisions`,
and a request that states neither is refused. Neither cleanup nor
`promote_scout` proceeds until a `no_open_decisions` inventory exists —
promotion mints a ship task justified by the investigation, so it treats that
investigation as reviewed. Cleanup will not remove a scout's worktree, because the worktree
holds the only copy of the investigation and a buried question would be erased
along with it. A later inventory replaces an earlier one, so a stale look never
outvotes a fresher one. The operator equivalent is
`devcrew task attest <task-handle> --finding <finding> [--open-decision <key> ...]`.
`sync_primary` is the only way the primary checkout ever moves, and it only
fast-forwards. It never resets, merges, stashes, or switches branches. A dirty
checkout (untracked files included), divergent history, a detached head, a
non-default branch, an absent upstream, or a branch tracking another local
branch are each refused by name, and the checkout is left exactly as it was.
A refusal is a completed operation carrying its posture, not an error; an
unreachable remote or an unreadable checkout is a failure, because the repair
is not the working copy.
`pause_task` is a non-destructive mutation: it preserves all work and asks the
worker to settle, so it is not gated behind the confirmation that guards
cleanup. `worker_profiles` reports the reviewed dispatch
catalog — identity, accepted shapes, harness availability and its reason, and
whether a profile can run unattended — so a caller can tell "no profile accepts
this shape" from "the profile that accepts it cannot run" without provoking a
preparation failure. It carries no launch authority: never the executable, its
argument vector, or the environment keys a profile may read. Readiness is reachable from the agent surface as well as the operator
CLI because it is the one read that answers "why can nothing start"; it reports
what is configured and reachable, never a credential, a path, or a secret
reference. Every call must carry a
generated-schema-valid `comis.callContext`; the configured service identity must
match, while optional managed-run references grant no task authority. Preparation
returns its visible task outcome separately from the private schema-validated
`comis.managedRun` result metadata. Only retryable uncertain mutations trigger one
bounded operation reconciliation and, after a completed durable outcome, one exact
idempotent replay. A replay may project a monotonically newer task state only when
the completed operation's durable result reference still names that task. The
command defaults to a separate `mcp.sock` and validates the out-of-band service
instance against every private call context.

`reconcile_task` accepts only an opaque task handle and
`action: "validate-clean-candidate"`. It is a non-read-only, idempotent,
closed-world mutation. The service derives the preparation, run, lease, terminal,
repository, worktree, branch, base, and head authority; callers cannot supply or
override those fields. An eligible unknown task must have a settled terminal and
an exact clean non-base candidate. A candidate committed under Comis's
lease-private Git confinement remains read-only during explanation; only this
mutation may validate its source, generated controls, and inert commit identity, import its objects,
compare-and-swap the prepared branch from the pinned base, and synchronize the
worktree index without replacing files. Recovery records fresh evidence and enters the
existing validation pipeline without creating a worker candidate report or
advancing the report cursor. Validation and pull-request delivery must match the
persisted recovery branch and head; a changed worktree is refused before validation
or forge mutation. Task detail and explanation keep that reconciliation operation
beside the judged candidate after validation and delivery, allowing a content-free
closeout to prove whether the candidate came from recovery.

Call `explain_task` before recovery. It distinguishes a settled terminal without
candidate evidence, unresolved restart evidence, unavailable host integration, an
unrecoverable workspace, unproven reporter-relay identity, and reconciliation
already in progress. Only the settled clean-candidate reason recommends
`reconcile_task`; an unrecoverable workspace remains preserved and requires an
explicitly approved replacement task. An unproven reporter relay is likewise
preserved without cleanup authority and recommends inspection rather than
reconciliation.

`cleanup_task` may refuse after it has placed the task in a durable cleanup hold,
for example when the exact worktree is dirty. Once that safety condition is
corrected, call `cleanup_task` again with the same task handle. The service resumes
the existing cleanup record even though the new MCP invocation has a fresh caller
operation ID, and records both operation identities against the single completed
cleanup effect.

After host release, cleanup closes and removes the exact service-owned reporter
attachment before it re-verifies Git and forge truth and authorizes worktree
removal. If attachment ownership cannot be proven, the service preserves the
runtime path and directs the operator to inspect that exact task attachment before
retrying cleanup.

Cleanup refuses before release when an operator cleanup hold remains open. The
error names the closed `open task hold` category and directs the operator to close
that exact hold; it never includes the operator-authored hold reason. Dirty-worktree
refusals likewise name the clean-worktree action required before a safe retry. An
unresolved decision is a distinct retryable precondition: the error directs the
operator to resolve that exact task decision without exposing its prompt or answer.
Positive active execution or validation evidence instructs the operator to wait
for settlement. Missing, lost, or mismatched terminal/run/lease authority instead
requires reconciliation and is never treated as idle. Current pull-request truth
that differs from the delivered head or required checks is a separate stale-forge
precondition and blocks host release. When GitHub retains repeated runs for one
required check name, the most recently started run is authoritative so an older
green run cannot mask a newer pending or failed rerun. Missing or malformed check
identity, recency, or state; duplicate check identities; incomplete pagination;
and two changing full snapshots all resolve the required checks to `unknown`.

## Liaison installation

The agent-side assets ship in this repository and are installed by the operator
into exactly one selected agent workspace. They are deliberately not bundled with
the Comis daemon: its bundled-skill tree auto-seeds into every deployment's data
directory at boot, which would hand a persona for this service to deployments
that do not run it.

```text
skills/dev-crew/SKILL.md            opt-in liaison skill
skills/dev-crew/references/         procedures loaded per step
workspace-template/ROLE.md          named-agent policy scaffold
workspace-template/TOOLS.md         deployment environment notes
workspace-template/HEARTBEAT.md     periodic-work policy scaffold
```

Copy `skills/dev-crew/` into the selected agent's workspace skill directory.
Copy each `workspace-template/` file only when the agent's workspace does not
already have that file; never overwrite a saved policy, and never install these
into a shared or default workspace. Each template keeps its `COMIS-TEMPLATE`
marker until an operator has filled it in and verified the result.

An empty prompt-skill allowlist means allow-all. A deployment that wants
enforced selectivity must add `dev-crew` to an explicit allowlist on the selected
agent; copying the skill into one workspace is not by itself an exclusion of the
others.

The skill recommends procedure only. It grants no tool, credential, or approval,
and it renders no operator command line: worker credentials, terminal profiles,
workspace roots, approval gates, forge branch protection, and service scopes
enforce the boundary in code regardless of what the prose says.

## Operator CLI surface

```text
devcrew [--socket PATH] service status
devcrew [--socket PATH] doctor [--format table|json]
devcrew [--socket PATH] status [--watch [--passes N] [--interval DURATION]] [--format table|json]
devcrew [--socket PATH] tasks list [--format table|json]
devcrew [--socket PATH] workers list [--format table|json]
devcrew [--socket PATH] task show TASK [--format yaml|json]
devcrew [--socket PATH] task explain TASK [--format text|json]
devcrew [--socket PATH] task diff TASK [--stat|--name-only] [--format text|json]
devcrew [--socket PATH] task logs TASK [--source worker|service|validation] [--follow [--passes N]] [--format text|json]
devcrew [--socket PATH] task launch-plan TASK [--format json]
devcrew [--socket PATH] task operation OPERATION [--format text|json]
devcrew [--socket PATH] task prepare --input FILE|- [--operation OPERATION] [--format json]
devcrew [--socket PATH] task reconcile TASK --action validate-clean-candidate [--operation OPERATION] [--format json]
devcrew [--socket PATH] task handback TASK --action validate-developer-work [--operation OPERATION] [--format json]
devcrew [--socket PATH] task pause TASK [--operation OPERATION] [--format json]
devcrew [--socket PATH] task cancel TASK [--operation OPERATION] [--format json]
devcrew [--socket PATH] task resume TASK [--operation OPERATION] [--format json]
devcrew [--socket PATH] task verify TASK [--operation OPERATION] [--format json]
devcrew [--socket PATH] task promote SCOUT --input FILE|- [--operation OPERATION] [--format json]
devcrew [--socket PATH] task replace TASK --worker PROFILE [--operation OPERATION] [--format json]
devcrew [--socket PATH] task steer TASK --instruction TEXT [--operation OPERATION] [--format json]
devcrew [--socket PATH] task cleanup TASK [--operation OPERATION] [--format json]
devcrew [--socket PATH] task discard TASK --yes [--operation OPERATION] [--format json]
devcrew [--socket PATH] events tail [--after SEQUENCE] [--format text|jsonl]
devcrew [--socket PATH] repair reconcile [--task TASK] [--format table|json]
devcrew [--socket PATH] decisions list [--task TASK] [--format table|json]
devcrew [--socket PATH] decision show TASK DECISION [--format text|json]
devcrew [--socket PATH] decision cancel TASK DECISION [--operation OPERATION] [--format json]
```

`events tail` follows the service event stream. The stream is content-free by
construction: every column is an identity, a closed discriminator, a version or a
time, so no column can hold a question, an objective, a path or a branch. That is
what makes it safe to read beside unrelated work; anything that identifies what a
task is about stays in the authenticated per-task reads.

Events are appended in the same transaction as the change they describe, so the
log can never claim a transition the durable state did not take, and a crash can
never keep a state whose event was lost. Each page reports the cursor to resume
from — including when it is empty — so following is a caller-side loop over
`--after` rather than a held connection: a follower that drops reconnects at the
sequence it last saw and neither replays nor skips. `--format jsonl` emits one
event per line so a consumer reads it incrementally.

`status --watch` re-reads the authoritative snapshot on every pass rather than
mutating a display from events. A dropped or reordered event therefore cannot
leave the view claiming a state the service is not in — the snapshot is the truth
and the stream only says when to look again. `--passes` bounds the run so the
command always terminates, and `--interval` paces it.

`repair reconcile` answers "what is stuck, and what would fix it". It surveys the
tasks in the unknown state — the only state the reconcile command accepts — and
classifies each against the same evidence that command requires: whether the
durable authority and terminal settlement are proven, whether the registered
worktree verifies, whether it is clean, and whether it holds a commit ahead of
the pinned base. Each posture carries the move it calls for, so an operator does
not have to re-derive from the code what the service already knows.

It reports and never acts. Choosing an action from evidence is the authority the
explicit `task reconcile TASK --action ...` command holds, and a survey that
reconciled on its own would become a second writer of that transition. One task
whose evidence cannot be read becomes its own posture rather than an error, so a
single stuck task never hides the rest of the fleet.

`task logs` reads one task's private history from one durable source, and the
sources stay separate because they carry different authority. `worker` is what the
worker reported about its own progress — a claim. `service` is the task's slice of
the durable event log — what the service actually did. `validation` is which
reviewed programs ran and how they ended. Blending them would leave an operator
unable to tell a claim from a fact, which is the distinction the precedence model
in §13.2 rests on. An unspecified source reads the worker's account, which is what
"what has this task been doing" means.

Worker text is bounded and rejected for control characters when the report is
accepted, so it cannot carry a terminal escape sequence into whatever renders it;
validation entries carry only the operator-sanitized executable label, never a
host path or an argument vector. Every source exposes the same monotonic cursor,
so `--follow` behaves identically whichever one is watched and each pass resumes
where the last page ended. It is bounded by `--passes` so the command always
terminates. Reading a task's history proves the task exists first, so an unknown
handle is a not-found rather than an empty page that would read as "this task did
nothing". Like `task diff`, it is operator-only: raw private history is not a
model surface.

`task diff` answers "what did the worker actually change". Committed and
uncommitted work are shown apart because they mean different things: committed
work is what the worker stands behind, while uncommitted work is what a handback
would land in a developer's editor. The base revision and the worktree are read
from durable state rather than from the request, so a caller cannot aim the read
at a tree the task does not own or measure the work from a revision nobody
pinned.

Only bounded summaries exist. `--stat` reports per-file line counts and totals and
`--name-only` reports paths; there is no flag that produces a patch body, because
a patch is unbounded worker-authored content. A binary change is marked as binary
rather than counted as zero lines, a rename keeps its previous path attached so
the work is never attributed to a file that no longer exists, and a change set
larger than the read bounds says its file list is truncated instead of presenting
a partial listing as complete. Change paths arrive verbatim from Git, so a path
carrying control characters or invalid encoding is refused rather than escaped —
an anomalous filename is worth stopping on, not displaying. Untracked files are
not part of a diff; the task's cleanliness in `task show` is what reports them.

`decisions list` and `decision show` answer "what is waiting on me". A decision is
named by its task and its key, because that pair is what identifies it durably;
there is no separate decision identity to quote. The task scope is applied by the
service, so naming one task never puts another task's private questions on the
socket.

Each row states which side is being waited on. `awaiting_host` means the question
exists but the host has not acknowledged the report carrying it, so nobody has
been asked yet; `awaiting_human` means it reached the host and no resolution has
come back. Those are different failures with different repairs, and collapsing
them would make a jammed delivery lane look like an unresponsive liaison. The
listing also states when the question will next be raised, derived from the same
cadence the service runs so the console cannot publish a schedule the supervisor
does not keep. An absent airing renders as `unknown` rather than as an epoch,
which would read as long overdue.

`task diff` is read-only and operator-only for the same reason the decision reads
are: it is private task detail.

`decision cancel` withdraws a question the human no longer wants answered. Only a
worker resolution or a human cancellation closes a decision, so without it a
question asked in error blocks completion and cleanup permanently with no way
out. It answers nothing on the worker's behalf and is recorded as its own fact
rather than as a resolution: a resolution says the work may proceed on an answer
somebody gave, and attributing that to a worker who never answered would be a
false record.

A withdrawn question is closed for every gate at once — cleanup safety, the
re-surfacing cadence, the operator inventory, candidate reconciliation and the
evidence view all consult one shared definition of "still awaiting a human". They
share it precisely so a withdrawn question cannot keep blocking cleanup in one
layer while reading as closed in another.

Answering a decision is deliberately not here. Under §14.2 the answer arrives
from Comis over the attention path, and the wire has no method for submitting one
from this side; adding one is a coordinated contract revision, not a local change.

Both decision reads are read-only and operator-only, and the refusal lives in the canonical
handler rather than only in the set of tools the model facade exposes: an open
question is private task detail, and a facade that later grew a tool must not
thereby gain the authority to read it. Answering a question stays on the generic
Comis attention path; neither command can submit or close an answer.

`task pause` asks one task's worker to reach a safe boundary and stop. It does
not itself pause the task: the worker is still holding the worktree when the
request lands, so the request rides the worker's next report receipt and the
worker's own paused report is what settles the state. That ordering is what
makes the worktree safe to hand to a developer — a task marked paused while its
worker kept committing would be changing under their editor. The request carries
no instruction text and no interrupt, and a repeat replays rather than stacking.

`task discard` removes the worktree of a task that stopped without delivering
anything. It exists because cancellation preserves work on purpose and cleanup
requires delivery evidence a cancelled task will never have — without it, the
worktree, lease and run binding of every cancelled task stay held with nothing
able to release them.

It is operator-only and has no agent tool. `--yes` is required and never
defaulted: cleanup proves removal is safe by pointing at delivered work, and a
discard has nothing to point at, so the operator typing it is the only gate the
command has. A dirty worktree is expected rather than refused — uncommitted work
is usually the thing being thrown away, and the acknowledgement covers it. Only
a cancelled or failed task can be discarded, nothing is removed while a terminal
or validation process is still running, and host authority is released before
removal exactly as cleanup does. The durable record says which proof authorised
the removal, so an audit can tell delivered-work removal from acknowledged
removal.

`task steer` sends one bounded instruction to a task's current worker. The
worker reads it on its next report receipt, so an instruction arrives at a
boundary the worker chose rather than being injected into a terminal — there is
no keystroke path to get wrong, and nothing can land mid-edit.

The instruction is stored once and delivered once. Instructions queue in the
order they were sent rather than overwriting one another, because two
instructions are two things the operator said. A repeat of the same operation
replays instead of queueing the words twice, and an uncertain send is reconciled
against its operation for the same reason. Control characters are refused: the
instruction is text a human wrote, not a command sequence, and must not smuggle
terminal escapes into whatever renders it later. Steering does not move the
task — a state change would claim the instruction had an effect nobody has yet
observed.

`task replace` hands one paused task to a different reviewed worker. The
worktree, the run binding and the lease are all preserved — replacement is for
when the work is worth keeping and the worker is not, so discarding the tree
would destroy the thing being preserved. The task passes through reconciliation
back to `ready` under a fresh brief revision, which is what makes the swap one
generation rather than a second worker joining the first: the previous worker's
reports name a revision that is no longer current.

The profile is required and is checked against the reviewed catalog for the
task's own shape, so a replacement cannot launch an unreviewed executable and a
ship profile cannot take over a scout. Replacement is refused while a terminal
or validation process is still running, and the durable trail records which
worker was swapped for which, on which tree, under which brief revision.

`task promote` creates a ship task from a scout's investigation. The scout is
untouched — its handle, shape, evidence and history stay exactly as they were,
and the promotion is recorded as a link naming the exact evidence digest that
justifies the new task. A worker cannot change its own task's shape; promotion
is an explicit operator or agent operation that mints a new task instead.

The scout handle is given on the command line and the contract carries only what
the ship revision must achieve. The contract must not name a scout, a
repository, a base revision or a shape: the repository and base revision are
inherited so a promotion cannot aim the ship task at code the investigation
never covered, and a contract naming its own scout could disagree with what the
operator typed. All four are refused by strict decoding rather than ignored.

`task verify` asks the service to validate one task now instead of waiting for
the worker to declare a candidate. It opens validation and nothing more: the
reviewed profile, the candidate inspection, the unresolved-decision check and
the evidence refresh all stay with the supervisor that already owns them, so the
command's result reports that validation started, never that it passed. It
selects no profile and no checks — a caller able to choose either could validate
against an easier bar than the one the task was accepted under. A task already
validating is left alone rather than restarted, and an unverifiable worktree is
judged `unknown` by the candidate inspection rather than blocked here.

`task resume` returns one paused task to the worker already running it. It is
refused when the worktree has uncommitted changes: the paused worker still holds
a brief, a base revision and an evidence set describing the tree it stopped on,
and none of them would notice a developer's edit, so resuming onto a changed
tree would continue from a description of a tree that no longer exists. The
refusal names the way out — `task handback --action validate-developer-work`,
which captures the fresh head, invalidates the evidence the edit stales and
revalidates. Resume selects no worker; choosing a different one is replacement.

`task cancel` stops work on one task and preserves its worktree, artifacts, run
binding and lease. It names no disposition: stopping and discarding are separate
decisions with deliberately different evidence requirements, and removal stays
behind `task cleanup`. Cancelling an already-cancelled task reports the settled
task rather than refusing, and cancelling clears any pause request standing
against it — a cancelled task has no worker left to answer one.

`workers list` reports the reviewed dispatch catalog: each profile's identity,
the task shapes it accepts, whether its harness is available and why not, and
whether it can prove a turn settled unattended. It answers "why can nothing
start" before a task exists to fail. It never renders the executable, its
argument vector, or the environment keys a profile may read — those stay launch
authority, reviewed once by an operator and used only to build a descriptor.

JSON outputs are stable versioned projections. Human and YAML views are
presentation only and carry no authority. The CLI never opens SQLite as a
normal-operation fallback. Task preparation reads one strict bounded JSON
contract, rejects unknown authority fields, and uses either the explicit stable
operation ID or one locally minted request ID.

All four commands support `--help` and `--version`.

## Protected real-Telegram campaign

The real-channel E0 gate is intentionally separate from repository verification.
It requires a dedicated self-hosted Linux runner operating as the service owner,
an isolated Comis/DevCrew deployment, one real human Telegram sender, repository-
scoped GitHub read/push authority without merge permission, and permission to
restart only the three isolated systemd units named in the manifest. The Telegram
bot credential remains in Comis secret management; it is never a workflow input.
The live helper starts children with a small environment allowlist. `GH_TOKEN` is
injected only into the two fixed GitHub CLI truth reads and is absent from Git,
workers, installers, systemd commands, service probes, and local CLIs.

Copy `test/live/manifest.example.json` outside the repository, replace every
placeholder with current content-free identities, set mode `0600`, and keep the
evidence root owner-private. Record both exact source commits, the compiled
protocol ID and digest, and the SHA-256 plus reported version of the Comis CLI
and all four DevCrew executables. Artifact paths must select canonical installed
files rather than `PATH` symlinks. The canonical `comis.codeRoot` and
`devcrew.codeRoot` checkouts must have `HEAD` at their respective recorded source
commit. Runtime validation re-hashes each file and
executes its fixed `--version` command; it refuses digest, version, or protocol
drift before the campaign starts. The three isolated systemd units are likewise
pinned to the SHA-256 of `systemctl cat <unit>` so drop-ins and unit-definition
changes cannot enter an accepted run unnoticed. The exact Codex and Claude Code
executables are bound to the two task profiles by canonical path, SHA-256, and
their native `--version` output. The manifest must describe
exactly two ship lanes:
one Codex/Claude profile per lane, exactly one recovered candidate, one handback,
and one cleanup operation per task.

The manifest declares one closed `campaignKind`, and that kind decides which
checkpoint arc the campaign may claim. `real_telegram` means a human drives the
channel from the Telegram app: its eleven opaque `e0cp-*` markers are sent by that
human at the named checkpoints. The unrelated marker must use a newer distinct
conversation; all other markers remain in the original preparation conversation.
Their timestamps must follow the documented manifest order exactly, from task
request through both restart acknowledgements to cleanup; partial or reordered
milestone evidence is refused.

`emulator` means the loopback Telegram Bot API emulator drives the channel. No
human sends anything, so an emulator campaign declares no checkpoints at all. A
manifest that declares an arc its channel cannot drive is refused before the
campaign starts, and the closeout records `real_human_telegram_checkpoints` with
status `not_claimed` rather than crediting a pass no human could have earned. The
protected runner below drives that human arc, so it accepts a `real_telegram`
manifest only.

The runner first observes both tasks simultaneously in `working`. It then replaces
the stateless MCP facade, waits for the human acknowledgement, waits for decision,
handback, and reconciliation checkpoints, and restarts DevCrew and Comis at their
separate ready markers. An acknowledgement recorded before its corresponding
restart does not satisfy the gate. The durable handback and reconciliation
operations must complete at or after their respective human Telegram checkpoints;
an operation completed before approval is refused even if the final task state is
otherwise clean. Immediately after the handback operation, the other manifest
task must still be observed in `working`; an earlier overlap snapshot cannot
substitute for that causal sibling-continuation proof. Final cleanup must leave
both tasks `cleaned`.

The manifest also pins the canonical Comis and DevCrew SQLite databases and the
task-worktree root. Resource snapshots read `MainPID`, `MemoryCurrent`, and
`TasksCurrent` for the exact three isolated systemd units, read each main
process's `/proc` RSS and open-file-descriptor count, count descendant bubblewrap
jails, total the Comis data tree and both database files, count active DevCrew
terminal bindings and task worktrees, and query both durable delivery backlogs.
Verification requires start and finish samples inside the campaign window
separated by at least one hour. It refuses excessive memory, RSS,
file-descriptor, or disk growth and requires two starting terminals/worktrees and
jails followed by zero residual terminals, jails, worktrees, or deliveries.

Recovery fields bind the owner-private candidate configuration, non-overlapping
synthetic rollback roots, and the exact previous Comis CLI plus four DevCrew
executables by path, SHA-256, and reported version. The live recovery support
also records the exact previous DevCrew release tag used by the installer; this
package coordinate is independent of the executables' reported version. It first
runs the repository-shipped installers in two new owner-private prefixes.
The fresh prefix must contain exact candidate Comis and DevCrew artifacts. The
upgrade prefix is verified first at every previous artifact hash and version,
then after an in-place candidate install at every current hash and version. All
three DevCrew installer invocations must report that the downloaded release
archive matched its published checksum. The support then
stops only the three isolated units, copies the Comis data tree without `.env`,
adds the DevCrew database, candidate configuration, and full unit definitions,
then restarts every stopped unit in reverse order even when backup construction
fails. Its manifest hashes every retained file and mode. Restore rechecks that
inventory, runs SQLite integrity checks, validates the copied Comis
configuration, and requires the candidate configuration and all three unit
definitions. Rollback uses only copied synthetic state: the previous Comis CLI
must validate it and the previous DevCrew service must open its copied database
and answer a healthy, complete status read through the previous CLI.

Run the protected campaign:

```sh
DEVCREW_LIVE_MANIFEST=/absolute/private/e0-campaign.json \
DEVCREW_LIVE_EVIDENCE_ROOT=/absolute/private/evidence \
DEVCREW_LIVE_BACKUP_ROOT=/absolute/private/recovery-backup \
DEVCREW_LIVE_RESTORE_ROOT=/absolute/private/recovery-restore \
DEVCREW_LIVE_FRESH_INSTALL_ROOT=/absolute/private/fresh-install \
DEVCREW_LIVE_UPGRADE_ROOT=/absolute/private/prior-release-upgrade \
make test-live
```

The full runner captures the start sample only after both task lanes are
simultaneously `working`, waits until that sample is at least one hour old, and
captures the finish sample only after both cleanups. A long manifest window by
itself cannot satisfy the resource gate.

`GH_TOKEN` must already be injected by the protected runner environment; never
place its literal in the manifest or shell history. The GitHub Actions workflow
uses the protected `devcrew-live` environment and `DEVCREW_LIVE_GH_TOKEN` secret.
It is manual-only because a scheduled job cannot supply the required real human
checkpoints honestly.

For evidence-only closeout, capture the non-overwriting baseline while both task
worktrees are present:

```sh
DEVCREW_LIVE_MANIFEST=/absolute/private/e0-campaign.json \
DEVCREW_LIVE_RESOURCE_BASELINE=/absolute/private/resource-baseline.json \
make live-baseline
```

After at least one hour and only after campaign cleanup is terminal, exercise
fresh installation, prior-release upgrade, backup, isolated restore, and
previous-binary rollback. All four output roots must not exist and must be
separate from every live or synthetic data root bound by the manifest:

```sh
DEVCREW_LIVE_MANIFEST=/absolute/private/e0-campaign.json \
DEVCREW_LIVE_BACKUP_ROOT=/absolute/private/recovery-backup \
DEVCREW_LIVE_RESTORE_ROOT=/absolute/private/recovery-restore \
DEVCREW_LIVE_FRESH_INSTALL_ROOT=/absolute/private/fresh-install \
DEVCREW_LIVE_UPGRADE_ROOT=/absolute/private/prior-release-upgrade \
DEVCREW_LIVE_RECOVERY_EVIDENCE=/absolute/private/recovery-evidence.json \
make live-recovery
```

Close out with that exact campaign- and source-bound resource baseline and
recovery evidence:

```sh
DEVCREW_LIVE_MANIFEST=/absolute/private/e0-campaign.json \
DEVCREW_LIVE_EVIDENCE_ROOT=/absolute/private/evidence \
DEVCREW_LIVE_RESOURCE_BASELINE=/absolute/private/resource-baseline.json \
DEVCREW_LIVE_RECOVERY_EVIDENCE=/absolute/private/recovery-evidence.json \
make live-closeout
```

Each run creates one non-overwriting `0700` campaign directory with `0600`
artifacts. It contains service/fleet/task/operation projections, content-free
Telegram checkpoint rows, Comis explanation and system-health reports, clean Git
truth proving both tasks share one pinned base that remains an ancestor of the
current base-branch tip, current open/unmerged GitHub pull-request and required-check truth,
plaintext-secret audit plus count-only residency results, the verified one-hour
resource observation, the verified fresh-install/upgrade/backup/restore/rollback
report, artifact hashes, and a single verdict. Raw Telegram message bodies and command stderr are
never retained.

The Comis reports are acceptance oracles, not opaque attachments. Closeout rejects
a system-health report unless its campaign window, session totals, hard-degraded
posture, Telegram/agent activity, and session-index, summary, and billing coverage
are complete. Each session explanation must resolve to the manifest agent and
origin Telegram conversation, carry a non-failed outcome, bounded tool evidence,
and prove either trajectory or lossless-context coverage. The original validated
JSON is retained so richer additive report fields are not discarded.
Across the explanation set, closeout also requires the complete eight-tool E0
workflow: two preparations and launch-plan reads, diagnostic list/get/explain
reads, reconciliation, handback, two completed cleanups, and at least six
precondition-classified cleanup refusals. Missing tool evidence cannot be replaced
by a final clean task projection. The failure previews must independently contain
the open-decision, open-hold, active-execution, unknown-execution,
dirty-worktree, and stale-forge-truth messages; six generic precondition counts do
not prove the safety rows.

## Worker reporter contract

Workers receive `devcrew-report` with `COMIS_EXECUTION_ATTACHMENT`,
`COMIS_EXECUTION_ATTACHMENT_TARGET_NAME`, and `COMIS_EXECUTION_ATTACHMENT_IDENTITY` set by
the host-managed terminal from the same activation-returned binding. The path names the
Comis-protected task socket at `/run/comis/attachments/<attachmentTargetName>`; the target name
must exactly match that path's host-assigned `attachment-<32 lowercase hex>.sock` basename. The
identity is the pinned lowercase Ed25519 public key used to authenticate an ephemeral encrypted
session with the connected task relay before any request crosses the socket. The command rejects
a different directory, basename, assigned name, or relay proof and accepts no
task, run, lease, socket, or credential selector.

Subcommands:

- `brief` reads the exact pinned contract.
- `acknowledge` verifies and echoes the socket-bound task, run, and lease, the
  actual canonical working directory, and the brief revision, before task state
  may become `working`.
- `progress`, `blocked`, `paused`, `candidate-complete`, `failed`, and
  `resolved` append bounded sparse reports.
- `decision` first durably appends the keyed attention report, then waits for
  the exact owner response and writes only that private response to stdout.
  Pending delivery stays silent and cancellation exits without inventing an
  answer.

A candidate report remains non-terminal until service validation.

This boundary treats all three environment values as untrusted inputs: the path
and target name are selectors, while the relay identity is public authentication
material rather than authority. The mount
directory must already grant its owner full access while denying group and other
read/write access; execute-only traversal is permitted so a dedicated worker UID
can reach its assigned socket without listing the directory. Every directory
component is opened without following symlinks; the protected mount identity and
socket filesystem identity are pinned and rechecked before and after each dial.
The socket must already exist at client construction and be a Unix socket with
mode `0600`. The fixed directory and exact assigned-name equality must also agree.
Any missing, altered, symlinked, mount-replaced, or differently named target fails
closed. Protected mounted reporter calls currently require Linux mount-identity
support; Darwin builds fail closed for this worker-mounted path. Client-construction
failures are written to worker stderr with their concrete safe reason before
command dispatch; a nil capability is never used.

## Attachment threat boundary

The boundary is deliberately narrow. The service rejects symlinks in every
runtime-root component before creating anything, owns each task directory and
socket, and never places the host source path in worker argv, stdin, or
environment. Comis alone carries the source into the protected mount identified by
activation; an altered attachment ID, target name, or mount path fails closed.
For attention responses, the worker supplies only the bounded decision key. The
socket-bound server derives the managed run, mints a fresh operation identity
for every poll, and reaches Comis only through the service-owned authenticated
control connection. Unbound sockets, altered response identity, invalid state,
and content on a pending response fail closed. The private response crosses only
the owner-only attachment and worker stdout; errors and service diagnostics do
not include it.
