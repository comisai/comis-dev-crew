---
name: dev-crew
version: 1.1.0
description: "Supervise durable isolated software-development tasks through the explicitly enabled DevCrew MCP service. Use when a user asks the current agent to prepare, inspect, recover, hand back, or clean up a DevCrew coding task. Do not use for ordinary coding requests when the DevCrew capability is not enabled."
comis:
  requires:
    bins: []
    env: []
---

# Dev Crew

Use the DevCrew MCP tools as the only task-control surface. Never assemble a
command line, open a database, invent a filesystem path, or infer task state
from worker prose or terminal output. This skill recommends procedures; it
grants no capability and never bypasses host policy or approval.

Keep task, operation, managed-run, lease, attachment, branch, and report
identities separate. Never copy authority from one task to another.

## Read before you write

1. `list_tasks` to discover durable task handles.
2. `get_task` for the selected handle.
3. `explain_task` whenever the state is blocked, failed, reconciling, or
   unknown. Follow only a suggested action present in the live tool set.
4. `get_launch_plan` only for a task whose durable posture says it is ready for
   launch. Treat returned run and lease references as opaque.

Before proposing a worker profile, read `worker_profiles`. It distinguishes the
three answers a preparation failure collapses into one: no profile accepts this
shape, the profile exists but its harness is unavailable, and it is usable but
cannot prove a turn settled unattended. Name a profile that read returned —
never one you inferred.

A terminal or coding-CLI exit is not candidate evidence and never means success.
Quiet output is not idle. When the service reports `unknown`, that is the answer
until fresh evidence changes it — do not narrate a guess as a state.

## Reference material

Load the reference that matches the step you are on:

- `references/task-shapes.md` — choosing ship or scout, writing acceptance
  criteria and constraints, selecting configured catalog handles.
- `references/delegation.md` — the delegation boundary, how to talk to the human
  about a task, and the session-knowledge sweep.
- `references/decisions.md` — worker decisions, holds, and when to ask rather
  than retry.
- `references/delivery.md` — validation, delivery, and cleanup safety.
- `references/recovery.md` — unknown tasks, reconciliation, and handback.

## The live surface

Call only tools the connected service actually exposes; the set grows as the
product does, and this list is not permission to guess a name.

| Intent | Tool | Changes |
|---|---|---|
| Look | `list_tasks`, `get_task`, `explain_task`, `get_launch_plan` | Nothing |
| See what can run | `worker_profiles` | Nothing |
| Check readiness | `doctor` | Nothing |
| Start work | `prepare_task` | Creates a prepared task and worktree |
| Settle a worker safely | `pause_task` | Asks the worker to stop at a safe boundary; changes no state itself |
| Recover an exited worker | `reconcile_task` | Validates one exact clean candidate |
| Resume after a developer edit | `handback_task` | Revalidates the developer's work |
| Retire a task | `cleanup_task` | Evidence-gated release and removal |

Read each tool's own side-effect metadata before calling it. Destructive tools
require the normal approval; never describe an approval as a formality, and
never re-use a human's answer to a worker question as approval for an action.

Anything not in the live tool set is unavailable, not merely undocumented. If a
user asks for a merge, a force-push, a deployment, raw terminal custody, or
sibling-worktree access, say plainly that it is not available here and name who
can do it instead.

## What you never send

Do not provide a path, command, executable, credential, run, lease, attachment,
branch, terminal, or service identity in any argument. DevCrew derives and
re-proves that authority server-side, and a refused call must leave the task
unchanged. If required catalog or base authority is unavailable, say what is
missing and ask the user to choose — do not substitute a plausible value.
