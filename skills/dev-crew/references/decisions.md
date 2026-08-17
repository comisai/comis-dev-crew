# Decisions, holds, and when to ask

## What a decision is

A worker raises a decision when it hits a choice it cannot make alone — an
ambiguous requirement, two defensible designs, a missing piece of information.
It is a domain question, not a permission request.

Each decision has a key that is stable within its task. The same key raised
again is the same question; a materially different question needs a different
key. The service holds the inventory; you surface it and carry the answer back.

## Answering

Deliver the answer to the person who owns the conversation, in their nouns, with
enough context to decide and no more. Include the alternatives and their
consequences when there really are alternatives; do not present a decision the
worker has effectively already made.

A reply binds to one open question. When the human answers plainly and exactly
one question is open in their scope, that is unambiguous. When two are open, ask
which one they mean — never pick the more recent, never pick the one that seems
more likely. A wrong binding sends an answer into the wrong worker.

Acknowledged delivery of the answer closes the captain-facing question: the
moment the answer lands, it is no longer waiting on the human. It does not close
the decision itself. The decision stays open — and keeps blocking completion and
cleanup — until the worker reports the same key resolved, or the human cancels
it. A delivered-but-unapplied answer is not a resolved decision.

A failed or unconfirmed delivery closes nothing. Say so.

## Holds

A hold is a recorded reason a task must not be retired yet. Open decisions and
open holds both block cleanup. That is the point: it is how a buried question
avoids being deleted along with the worktree.

Never resolve a hold by working around it. Resolve the named condition, then
retry the same task.

## Decisions are not approvals

A question about an implementation choice uses the decision path. A merge, a
discard, a force-push, a destructive cleanup, a credential use, or any reduction
in security posture uses the host's typed approval path.

A "yes" to a worker's question is never reusable as approval for a different
action. If you find yourself reasoning that the user "already said go ahead",
stop: they answered a question, and the action in front of you needs its own
approval.

## When to ask rather than retry

Ask the human when:

- required catalog, base, or contract authority is missing or ambiguous;
- a refusal names a condition only a person can resolve;
- two readings of the request would produce materially different work;
- evidence contradicts itself and no further tool call would settle it.

Retry only when the refusal names a transient condition and the same operation
identity is safe to repeat. Never retry a mutation because the first outcome was
uncertain — reconcile the operation first, then decide.
