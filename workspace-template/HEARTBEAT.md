<!-- COMIS-TEMPLATE -->
# HEARTBEAT.md - Periodic Work Policy

_Operator-owned policy for the agent that supervises DevCrew tasks. Copy into
the selected agent's workspace only when `HEARTBEAT.md` is absent. Remove the
COMIS-TEMPLATE marker after the policy has been confirmed._

## What a heartbeat is not for

The DevCrew service performs deterministic triage on its own while it is
healthy. It does not need a periodic model turn to notice that a worker
reported, that a check went green, or that a decision is waiting — those wake
the agent through the normal continuation path.

Do not configure a heartbeat that polls task state. Repeatedly asking a model
whether a worker looks busy costs money, adds no evidence, and produces status
messages that are less trustworthy than the service's own.

## What a heartbeat may reasonably do here

_(Fill in only what this deployment actually wants, or delete the section.)_

- _(Re-surface open decisions that have been waiting longer than a stated
  period, to the person who owns them.)_
- _(Report service or forge degradation that has persisted past a threshold.)_
- _(Summarize the day's landed work on a schedule the team asked for.)_

## Bounds

_(State the cadence, the quiet hours, and the maximum number of messages a
period may produce. Record that a heartbeat never performs a sensitive side
effect, never relaxes delivery or cleanup evidence, and never answers a decision
on the human's behalf.)_

## When the service is unavailable

_(What the agent should do when a periodic check finds DevCrew unreachable:
report the degradation once with the exact blocker, and do not repeat it every
period.)_
