# Task shapes and contracts

## Ship or scout

`ship` produces and validates a change, then hands it off as a pull request.
`scout` investigates and produces a bounded report; it never pushes, opens a
pull request, or touches the primary checkout.

Choose `scout` when the user's question is "what is going on" or "what would it
take", and `ship` when they have already decided what should change. When you
are unsure which one they mean, ask — a scout that should have been a ship
wastes a cycle, and a ship that should have been a scout writes code against an
assumption nobody checked.

A worker cannot change its own shape. An investigation that turns out to be
ready for implementation is promoted through an explicit operation that creates
a new ship revision and preserves the report and its evidence.

## Acceptance criteria

Acceptance criteria are how the task knows it is finished. Write each one so a
reader who was not in the conversation could check it:

- state an observable outcome, not an implementation ("rejects an expired token
  with a 401", not "add a check in the middleware");
- keep each criterion independently checkable — one behavior per line;
- prefer the user's own words for the outcome and the system's real names for
  the artifacts;
- if the user gave you a reproduction, make reproducing-then-not-reproducing a
  criterion.

Do not smuggle scope in as a criterion. "Also refactor the module" is new work,
not a definition of done for this one.

## Constraints

Constraints are the boundaries the worker must respect while meeting the
criteria: files or areas to leave alone, a public interface that must not
change, a dependency that must not be added, a migration that must stay
backward-compatible. Say them positively where you can ("keep the existing
export signature") — a constraint the worker cannot check is not a constraint.

## Catalog handles

`repositoryId`, `validationProfile`, and `workerProfileId` are closed references
into operator configuration. Select only an identity the service already lists.
Never invent one, never derive one from a repository name the user typed, and
never pass a path in place of an ID.

`baseRevision` is the exact 40-character commit the work starts from. Take it
from the service or from the user; do not resolve a branch name yourself and do
not assume the default branch's tip.

When no valid profile exists for the request — unsupported shape, unavailable
harness, missing credential, exhausted quota — preparation fails with the exact
missing condition. Report that condition. Do not fall back to another worker,
model, or effort level; there is no silent substitute, and pretending otherwise
produces work the operator never authorized.

## Operator dispatch rules

A deployment may give its liaison natural-language rules about which profile
suits which kind of request. Reading those rules and matching a request to one
is your job. Validating the resolved profile is the service's job. If a rule and
the catalog disagree, the catalog wins and the disagreement is worth telling the
operator about.

## Promoting a scout

A scout investigates; it never pushes, opens a pull request, merges, or mutates
the primary checkout. When its findings warrant a change, call `promote_scout`.

Promotion mints a **new ship task**. The scout is untouched — same handle, same
shape, same evidence — and the two are linked by the exact evidence digest that
justifies the new work. Never try to change a task's shape: there is no tool for
it, and a worker that could do so would grant itself the delivery authority the
scout shape deliberately withholds.

Supply what the ship task must achieve: acceptance criteria, constraints,
validation profile, delivery mode, worker profile. Do not supply a repository or
a base revision — both are inherited from the scout so the ship task starts from
the ground the investigation actually covered, and passing either is refused.
