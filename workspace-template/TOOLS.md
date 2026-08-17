<!-- COMIS-TEMPLATE -->
# TOOLS.md - Environment and Tool Notes

_Operator-owned notes for the agent that supervises DevCrew tasks. Copy into the
selected agent's workspace only when `TOOLS.md` is absent. Remove the
COMIS-TEMPLATE marker after the notes have been confirmed._

_Capabilities are defined by the runtime and by the installed service. This file
records deployment facts that help use them correctly. Nothing written here
grants a tool, a credential, or an approval._

## Configured repositories

_(List the repository IDs this deployment exposes and, in plain language, what
each one is. Record the default branch and the validation profile each uses.
These are opaque catalog identities: the agent selects them by ID and never by
path.)_

## Worker profiles

_(List the configured worker profile IDs, which task shapes each supports, and
any profile that is explicitly degraded — a profile without a trustworthy
end-of-turn signal cannot be used unattended. Record concurrency limits so the
agent can explain a queued task rather than assume a failure.)_

## Validation profiles

_(What each validation profile actually runs, in terms a person would recognize,
and roughly how long it takes. This is so the agent can tell a human what is
being waited on, not so it can reproduce the commands.)_

## Delivery and forge

_(Which forge account is used for pushes, what branch protection is in force,
and who holds merge authority. Record explicitly that the worker credential
cannot merge.)_

## Service health

_(Where the operator checks DevCrew service health, and what the agent should
say when the service is unreachable. An unreachable service means unknown, not
failed.)_

## Known limitations

_(Record what this deployment cannot currently do, so the agent can say so
plainly instead of attempting a workaround: absent tools, unavailable delivery
modes, unsupported task shapes, or platform features not installed here.)_
