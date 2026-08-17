# Validation, delivery, and cleanup

## Completion is a candidate, not a verdict

A worker reporting that it finished puts the task in a candidate state. It is
not done. Readiness is decided by fixed validation and by forge or artifact
evidence the service reads for itself.

`validating`, `candidate_complete`, and `delivering` are all in-progress. Only a
durable `delivered` state, backed by current validation and current delivery
evidence, means the work has landed.

Worker prose, terminal output, and a URL someone typed are not evidence. If a
report claims a pull request exists, the pull request that counts is the one the
service re-read from the forge.

## What invalidates evidence

Evidence is bound to an exact head. It goes stale when the branch moves, when a
required check expires or turns red, when the repository identity no longer
matches, or when the forge cannot be reached. A stale bundle is not a failure —
it is a reason to revalidate before claiming anything.

Never describe a check as passing because it passed earlier.

## Delivery modes

A ship task delivers a pull request. A scout task delivers an immutable report
artifact. A scout cannot switch to a mutation-bearing mode; converting an
investigation into implementation is an explicit promotion that creates a new
ship revision.

Missing, symlinked, oversized, non-regular, or changed-after-hash artifacts fail
closed. That is correct behavior, not an error to route around.

## Cleanup

Cleanup is a proof, not a command. The service releases the lease and removes
the worktree only when every required fact is positively safe: no task-attributed
process still running, identities still matching, no dirty or unlanded work, no
open decision or hold, delivery evidence and receipt present, forge and local
heads not diverged.

Cleanup refuses for six distinct reasons: an open decision, an open hold, active
execution, unknown execution authority, a dirty worktree, and stale forge truth.
Each refusal names its own category without exposing the underlying content.

When cleanup refuses:

1. call `explain_task` and read the named condition;
2. resolve only that condition;
3. retry the same task.

Do not resolve a condition by deleting the thing that raised it. Do not retry
against a different task. Do not treat a refusal as a transient error worth a
second immediate attempt — nothing changed between the two calls.

Ambiguous work is preserved on purpose. A fleet view that looks clean because
uncertain work was removed is worse than one that shows a task still held.
