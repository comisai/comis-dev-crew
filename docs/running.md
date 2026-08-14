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

Task preparation creates or exactly adopts its operation-bound linked Git
worktree beneath that parent before requesting the Comis workspace lease. Launch
the installed service without hand-editing generated or runtime files:

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

Diagnostic reads report validation as `unknown` when a task is already
`validating` but no durable judgment or active validation process can be found;
they never turn that inconsistent posture into `not_started`.

For a worker-reported candidate, the final authenticated candidate report closes
delivery after both evidence publications are acknowledged. For a reconciled
candidate, no worker report is invented: acknowledgement of both server-owned
publications atomically closes the task as `delivered`. Cleanup accepts exactly
one of those origins and refuses missing or ambiguous evidence.

`task explain` reads the latest durable candidate judgment for a failed task and
distinguishes a required local-validation failure from a required forge-check
failure. Other terminal failures retain the generic failed-task explanation.
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

The facade defines eight tools: `prepare_task`, `reconcile_task`, `handback_task`,
`cleanup_task`, `list_tasks`, `get_task`, `explain_task`, and `get_launch_plan`. Every call must carry a
generated-schema-valid `comis.callContext`; the configured service identity must
match, while optional managed-run references grant no task authority. Preparation
returns its visible task outcome separately from the private schema-validated
`comis.managedRun` result metadata. Only retryable uncertain mutations trigger one
bounded operation reconciliation and, after a completed durable outcome, one exact
idempotent replay. The command defaults to a separate `mcp.sock` and validates the
out-of-band service instance against every private call context.

`reconcile_task` accepts only an opaque task handle and
`action: "validate-clean-candidate"`. It is a non-read-only, idempotent,
closed-world mutation. The service derives the preparation, run, lease, terminal,
repository, worktree, branch, base, and head authority; callers cannot supply or
override those fields. An eligible unknown task must have a settled terminal and
an exact clean non-base candidate. Recovery records fresh evidence and enters the
existing validation pipeline without creating a worker candidate report or
advancing the report cursor. Task detail and explanation keep that reconciliation
operation beside the judged candidate after validation and delivery, allowing a
content-free closeout to prove whether the candidate came from recovery.

Call `explain_task` before recovery. It distinguishes a settled terminal without
candidate evidence, unresolved restart evidence, unavailable host integration, an
unrecoverable workspace, and reconciliation already in progress. Only the settled
clean-candidate reason recommends `reconcile_task`; an unrecoverable workspace
remains preserved and requires an explicitly approved replacement task.

`cleanup_task` may refuse after it has placed the task in a durable cleanup hold,
for example when the exact worktree is dirty. Once that safety condition is
corrected, call `cleanup_task` again with the same task handle. The service resumes
the existing cleanup record even though the new MCP invocation has a fresh caller
operation ID, and records both operation identities against the single completed
cleanup effect.

Cleanup refuses before release when an operator cleanup hold remains open. The
error names the closed `open task hold` category and directs the operator to close
that exact hold; it never includes the operator-authored hold reason. Dirty-worktree
refusals likewise name the clean-worktree action required before a safe retry. An
unresolved decision is a distinct retryable precondition: the error directs the
operator to resolve that exact task decision without exposing its prompt or answer.

## Operator CLI surface

```text
devcrew [--socket PATH] service status
devcrew [--socket PATH] doctor [--format table|json]
devcrew [--socket PATH] status [--format table|json]
devcrew [--socket PATH] tasks list [--format table|json]
devcrew [--socket PATH] task show TASK [--format yaml|json]
devcrew [--socket PATH] task explain TASK [--format text|json]
devcrew [--socket PATH] task launch-plan TASK [--format json]
devcrew [--socket PATH] task operation OPERATION [--format text|json]
devcrew [--socket PATH] task prepare --input FILE|- [--operation OPERATION] [--format json]
devcrew [--socket PATH] task reconcile TASK --action validate-clean-candidate [--operation OPERATION] [--format json]
devcrew [--socket PATH] task handback TASK --action validate-developer-work [--operation OPERATION] [--format json]
devcrew [--socket PATH] task cleanup TASK [--operation OPERATION] [--format json]
```

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
and one cleanup operation per task. Its eleven opaque `e0cp-*` markers are sent by
the human from the Telegram app at the named checkpoints. The unrelated marker
must use a newer distinct conversation; all other markers remain in the original
preparation conversation. Their timestamps must follow the documented manifest
order exactly, from task request through both restart acknowledgements to cleanup;
partial or reordered milestone evidence is refused.

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
backup, isolated restore, and previous-binary rollback. The backup and restore
roots must not exist and must be separate from every live or synthetic data root
bound by the manifest:

```sh
DEVCREW_LIVE_MANIFEST=/absolute/private/e0-campaign.json \
DEVCREW_LIVE_BACKUP_ROOT=/absolute/private/recovery-backup \
DEVCREW_LIVE_RESTORE_ROOT=/absolute/private/recovery-restore \
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
resource observation, the verified backup/restore/rollback report, artifact
hashes, and a single verdict. Raw Telegram message bodies and command stderr are
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
reads, reconciliation, handback, two completed cleanups, and at least two
precondition-classified cleanup refusals. Missing tool evidence cannot be replaced
by a final clean task projection. The failure previews must independently contain
the closed unresolved-decision and dirty-worktree messages; two generic
precondition counts do not prove both safety rows.

## Worker reporter contract

Workers receive `devcrew-report` with `COMIS_EXECUTION_ATTACHMENT` and
`COMIS_EXECUTION_ATTACHMENT_TARGET_NAME` set by the host-managed terminal from the same
activation-returned binding. The former is the Comis-protected task socket at
`/run/comis/attachments/<attachmentTargetName>`; the latter must exactly match
that path's host-assigned `attachment-<32 lowercase hex>.sock` basename. The
command rejects a different directory, basename, or assigned name and accepts no
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

This boundary treats both environment values as untrusted selectors. The mount
directory must already exist without group or other access and must equal its
`EvalSymlinks` result. The socket must already exist at client construction, be a
Unix socket with mode `0600`, and keep its pinned inode. The fixed directory and
exact assigned-name equality must also agree. Any missing, altered, symlinked, or
differently named target fails closed. Client-construction failures are written to
worker stderr with their concrete safe reason before command dispatch; a nil
capability is never used.

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
