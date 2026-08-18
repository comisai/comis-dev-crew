# Recovery

## Reading an unknown task

`unknown` is a real, durable state: the service could not join the facts it
needs, and it refuses to guess. It is never a synonym for failed, idle, or safe
to clean.

Call `explain_task` and branch on the reason it names:

- **`terminal_exited_without_candidate_evidence`** — the worker's terminal ended
  without reporting a candidate. After the normal approval, call
  `reconcile_task` with only the task handle and
  `action: "validate-clean-candidate"`. The service re-proves the worktree,
  branch, base ancestry, and clean index itself.
- **`reconciliation_in_progress`** — a recovery is already running. Inspect;
  do not start a second one.
- **`host_integration_unavailable`** — the host control plane is unreachable.
  Report the blocker and inspect service health. Do not retry a mutation blindly;
  the outcome of an in-flight operation is uncertain, not failed.
- **`restart_evidence_unresolved`** — a restart left the join incomplete.
  Inspect the task and service health. Do not infer a settled terminal or a
  candidate that was never reported.
- **`workspace_not_recoverable`** — do not reconcile. The existing task stays
  preserved. Prepare a replacement only after the user approves a new bounded
  task contract, and say plainly that the original work is being left in place.

## What reconciliation is not

Reconciliation recovers one narrow case: a worker exited without reporting, and
its registered worktree holds a clean commit descended from the pinned base. It
does not synthesize a report, it does not advance the report cursor, and it does
not invent a candidate where the worktree is dirty, empty, divergent, or
ambiguous.

Never add a worktree path, repository, branch, head, run, lease, terminal, or
attachment field to the call. Those are derived and re-proved server-side, and a
refused recovery must leave the task and worktree exactly as they were.

## Steer

`steer_task` sends one bounded instruction to a task's current worker. The
worker reads it on its next report, so a successful call means the instruction
is queued — not that the worker has seen it, and not that it has acted on it.
Never report a steer as having changed anything.

Say it once. Instructions queue in order rather than overwriting, so sending the
same words again because you did not see the first call land means the worker
hears them twice. An uncertain send is reconciled against its operation instead.

The instruction is plain text: no control characters, no escape sequences, and
bounded in length. If it is refused, rewrite it as plain prose rather than
looking for a way around the check.

## Pause

`pause_task` asks one task's worker to reach a safe boundary and stop. It takes
a task handle and nothing else — no instruction, no interrupt.

It does not pause the task. The worker is still holding the worktree when the
request lands; it reads the request on its next report and answers with a paused
report, and that report is what settles the state. So a successful `pause_task`
means "the request is standing", not "the worker has stopped". Read the task
before telling anyone the worktree is safe to edit, and never report a pause as
complete on the strength of the call returning.

A repeated pause replays rather than stacking, so retrying a call whose outcome
you did not see is safe.

## Discard

There is no agent tool for discarding. Removing work nobody delivered is an
operator decision made at the CLI with an explicit acknowledgement, and there is
no way to reach it from here.

If a cancelled task's worktree needs to go, say so plainly and let the operator
run `devcrew task discard <handle> --yes`. Never describe cancellation as having
freed disk: it preserves the work on purpose.

## Cancel

`cancel_task` stops work and keeps it. The worktree, the artifacts, the run
binding and the lease all survive; removing them is `cleanup_task`, which is
evidence-gated for that reason. Never reach for cancel when the intent is to
free disk, and never describe a cancel as having cleaned anything up.

Cancelling a task that is already cancelled reports the settled task instead of
refusing, so a repeat is safe.

## Replace

`replace_worker` hands a paused task to a different reviewed worker. Use it when
the work is worth keeping and the worker is not — a wedged harness, a profile
that turned out wrong for the task.

The worktree and everything in it survive. Replacement changes who continues,
never what exists; if the intent is to stop or throw away the work, say that
with `cancel_task` instead.

Name a profile from `worker_profiles`. It is checked against the reviewed
catalog for this task's shape, so a guessed name is refused rather than
launched, and a ship profile cannot take over a scout.

After a replacement the task is `ready` under a new brief revision. The previous
worker's reports name the old revision and are refused — that is what makes the
swap one generation rather than two workers sharing a tree.

## Verify

`verify_task` opens validation now rather than waiting for the worker to declare
a candidate. Its result says validation started — never that it passed. The
verdict arrives later as evidence, so read the task before reporting an outcome.

It selects no profile and no checks. There is no flag to skip a check or pick an
easier profile, and looking for one is a sign the intended action is different.

## Resume

`resume_task` returns a paused task to the worker already running it. It is
refused when the worktree has uncommitted changes, and that refusal is the point
of the command: the paused worker holds a brief and an evidence set describing
the tree it stopped on, and neither would notice an edit.

A refusal is a routing signal, not an obstacle. Do not retry it and do not look
for a force flag — there is none. Hand the work back with
`action: "validate-developer-work"` so the edit is revalidated, or replace the
worker. Resume selects no worker of its own.

## Handback

Handback is what follows a settled pause: a developer deliberately edited a
safely paused task's worktree in their own tools, and wants automation to pick it
up again. Call it with `action: "validate-developer-work"`.

The service captures the fresh head and dirty state, invalidates every piece of
evidence whose recorded head changed, and resumes exactly one worker generation.
Evidence produced before the developer's edit does not survive it.

Handback is not a way to recover an exited worker that emitted no candidate
report — that is reconciliation. Using the wrong one produces a refusal, which
is the correct outcome.

## Uncertain operations

A timeout means the outcome is uncertain, not that it failed. Query the same
operation identity before deciding anything. Retrying a mutation on an uncertain
outcome is how duplicate work gets created; the operation ledger exists so you
do not have to guess.
