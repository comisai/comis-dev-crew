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

## Handback

Handback is for a different situation: a developer deliberately edited a safely
paused task's worktree in their own tools, and wants automation to pick it up
again. Call it with `action: "validate-developer-work"`.

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
