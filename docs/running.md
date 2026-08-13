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

`task explain` reads the latest durable candidate judgment for a failed task and
distinguishes a required local-validation failure from a required forge-check
failure. Other terminal failures retain the generic failed-task explanation.

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

The facade defines seven tools: `prepare_task`, `handback_task`, `cleanup_task`,
`list_tasks`, `get_task`, `explain_task`, and `get_launch_plan`. Every call must carry a
generated-schema-valid `comis.callContext`; the configured service identity must
match, while optional managed-run references grant no task authority. Preparation
returns its visible task outcome separately from the private schema-validated
`comis.managedRun` result metadata. Only retryable uncertain mutations trigger one
bounded operation reconciliation and, after a completed durable outcome, one exact
idempotent replay. The command defaults to a separate `mcp.sock` and validates the
out-of-band service instance against every private call context.

`cleanup_task` may refuse after it has placed the task in a durable cleanup hold,
for example when the exact worktree is dirty. Once that safety condition is
corrected, call `cleanup_task` again with the same task handle. The service resumes
the existing cleanup record even though the new MCP invocation has a fresh caller
operation ID, and records both operation identities against the single completed
cleanup effect.

Cleanup refuses before release when an operator cleanup hold remains open. The
error names the closed `open task hold` category and directs the operator to close
that exact hold; it never includes the operator-authored hold reason. Dirty-worktree
refusals likewise name the clean-worktree action required before a safe retry.

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
```

JSON outputs are stable versioned projections. Human and YAML views are
presentation only and carry no authority. The CLI never opens SQLite as a
normal-operation fallback. Task preparation reads one strict bounded JSON
contract, rejects unknown authority fields, and uses either the explicit stable
operation ID or one locally minted request ID.

All four commands support `--help` and `--version`.

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
