# comis-dev-crew

Pre-release E0 foundation. The service now owns durable SQLite state and a strict owner-only
local API; the operator CLI provides read-only service, fleet, task, and operation views. The
protocol foundation pins the 24-artifact Comis capability-service revision at source commit
`b84577fe790829bf1f043af6c2626f6b27ef7b89` and bundle digest
`94ec7bd173cd20f0de2cb4e9ab719d392f240236ac80d56e3a7ea1abe4e20cb8`, and generates a closed Go
adapter. The durable-work wave now includes the closed E0 task transition authority and one
canonical task contract whose deterministic worker brief is pinned by a stored SHA-256 revision
hash. The application now commits replay-safe task preparation, attachment request, and exact host
binding atomically with their durable operation outcomes. Tagged integration tests prove the
restart/replay matrix and the generated client handshake against Comis's standalone fixture host.
Real worker execution, public mutation transport, and end-to-end production Comis runtime wiring
are not implemented yet.

`comis-dev-crew` is a companion product for the [Comis](https://github.com/comisai/comis)
agent platform: a long-lived Go service (`devcrew-service`), an independent operator CLI
(`devcrew`), a thin MCP facade (`devcrew-mcp`), and a restricted task reporter
(`devcrew-report`) for supervising multiple coding-CLI workers in isolated git worktrees.

## Status: independent E0 foundation

The maintainer-created bootstrap was adopted without reinitializing its history. The
repository now has its engineering protocol, verification contract, CI foundation, pure
E0 domain records, a pure-Go SQLite store, canonical read application handlers, a bounded
newline-delimited local protocol over an owner-only Unix socket, the first read-only operator
CLI, and an authenticated Comis protocol pin with generated DTO, validation, and Unix control
client support. The protocol-foundation join gate is implemented for protocol
`comis.capability-service/1`, including workspace-lease, terminal-event, and execution-attachment
control scopes. End-to-end production worker execution and public mutation transport are not
claimed yet.

The Comis adapter now also contains the supervised persistent bidirectional connection used by
the next service composition step. It authenticates the exact pinned handshake, dispatches only
`managedRuns.activate` and `managedRuns.abandon`, carries `managedRuns.report` on the same socket,
and reconnects with bounded backoff. Wrong credentials, altered operation envelopes, unknown
fields, excess concurrency, and forged run references fail before handler authority. The adapter
does not retry an uncertain report itself. A separate stateless forwarder polls the durable outbox,
maps the closed worker vocabulary to the pinned Comis report vocabulary, and retries uncertain
outcomes with the same operation and service-report identities until it can durably record an exact
host acknowledgement. The service lifecycle can now supervise exactly one supplied control
connection and that forwarder alongside both local endpoints, cancelling and joining all of them
if any component fails. Authenticated inbound activation is backed by the same durable mutation
coordinator: the stored external reference, registration nonce, service instance, UTC expiry, and
workspace-request invariant are checked in the transaction that commits the exact managed run and
workspace lease. Inbound abandonment durably closes the private preparation; `preserve` retains
the prepared task with its closed reason, while `reap_safe` enters the unbound reversible
cancellation/cleanup path. Exact operation replay returns the original wire acknowledgement and
altered reuse is rejected with a content-free durable conflict audit. The installed service
command constructs that connection from an explicit owner-local configuration profile and never
accepts the protected bearer on its command line.

Task records persist bounded acceptance criteria and constraints with the exact brief revision
hash. Any changed contract field invalidates the pin, and incomplete lifecycle reconciliation
can become only `unknown` until verified evidence selects a known E0 state.

Before opening its local socket or advertising readiness, the service reconciles durable startup
state. Prepared and ready tasks remain known because no work-start evidence exists; tasks whose
runtime may have been active become `unknown`, as do operations left merely `accepted`. Stable
terminal task evidence and completed or already-unknown operations are preserved, and a repeated
restart is idempotent.

The first mutation boundary prepares a service-minted task and later activates it with the exact
host-managed run and workspace lease. Each change commits the task and its completed operation
at one global state version. Identical operation replays recover the original task across a
service restart; altered subjects fail closed, and concurrent identical preparation creates one
logical task. Preparation also atomically stores the private external reference, registration
nonce, bounded UTC expiry, requested workspace root, and open/abandoned posture needed for the
Comis two-phase join; exact replay returns that same private registration instead of minting
another. A separate durable start command records `ready` to `launching` intent and its
operation outcome before the deterministic fixture may emit progress or begin work.

The typed local client and strict handler now expose `PrepareTask` as the first canonical mutation.
Its public payload contains only task-contract fields: the stable operation ID comes from the
request envelope and the configured service instance comes from endpoint composition. The result
classifies the operation as `mutate` and carries the private durable registration for the later MCP
adapter. The service composition can bind this same coordinator to a dedicated owner-only MCP
endpoint while retaining a separate operator endpoint, and both servers cancel and join together.
The installed service command validates one explicitly configured primary repository and linked
task worktree before opening its endpoints. It supplies cryptographically random task and
registration identities, advertises that verified worktree in the managed-run preparation, and
binds the same mutation authority to the dedicated MCP endpoint.

The stateless MCP adapter now uses the exact-pinned official Go SDK and defines only five tools:
`prepare_task`, `list_tasks`, `get_task`, `explain_task`, and `get_launch_plan`. Every call must carry a generated-schema
valid `comis.callContext`; the configured service identity must match, while optional managed-run
references grant no task authority. Preparation returns its visible task outcome separately from
the private schema-validated `comis.managedRun` result metadata. Only retryable uncertain mutations
trigger one bounded operation reconciliation and, after a completed durable outcome, one exact
idempotent replay. The `devcrew-mcp` executable now runs this facade on the SDK stdio transport,
requires the exact service instance out of band, and connects only to its dedicated local socket.

The adapter parity fixture drives one real service and database through the typed client, CLI JSON,
and official-SDK MCP transport. Preparation produces the same normalized task, operation, state
version, and `mutate` classification across all three paths; repeated calls create one task, and an
altered stable operation remains the same non-retryable `conflict`. List, get, explain, and launch
plan return
identical versioned projections through all adapters and retain their `read` classification.
A tagged integration test also builds and kills the real stdio `devcrew-mcp` process, replaces it,
and proves the prepared task, completed operation, exact private extension, and one logical replay
remain intact; a forged managed-run metadata hint changes no task authority. A second tagged test
builds and launches both installed processes, performs the authenticated pinned handshake,
prepares through the official-SDK MCP facade, activates through the persistent control socket,
and returns the safe reviewed Codex launch plan from the installed service.

The repository registry resolves only operator-configured opaque IDs. It validates canonical
primary checkouts and dedicated worktree roots beneath approved roots, pins the primary Git
common-directory filesystem identity, and creates or exactly adopts an operation-bound task
worktree only when real Git queries prove the exact repository, pinned base, branch, and path
identity. Deterministic bounded branch naming is collision-safe; retries return the same worktree.
Dirty, divergent, unpushed, symlink-escaped, primary-checkout, live-sibling, untracked-target, and
cleanup-ambiguous postures fail closed without overwriting or removing work.

The reporter seam is append-only and task-scoped: its endpoint stores only a digest
of the protected credential, derives the task identity instead of accepting it from worker
content, requires the exact pinned brief revision/hash, bounds the sparse closed report payload,
and rejects a mismatched sink receipt. Its application sink now atomically persists the exact
authenticated report, advances the task cursor and closed E0 lifecycle, and enqueues a stable
Comis delivery identity at the same global state version. Pending deliveries survive restart and
remain eligible for exact-identity resend until the store records the host's matching sequence and
retention acknowledgement. Identical task/report IDs replay the original receipt across restart;
altered payloads, acknowledgements, and ambiguous decision keys fail closed. A real worker uses
an owner-only per-task Unix socket to read its brief, acknowledge its exact run/lease/canonical-cwd
and brief binding, and append reports without a task or authority selector. Preparation creates
that socket under the configured private runtime root and declares it as `requestedAttachment`;
activation then supplies the execution-attachment ID and target name that bind the same listener.
The listener and any durable activation binding are reconstructed after a service restart. The
control-socket forwarder is implemented as an independently supervised adapter and participates
in the installed service lifecycle. The deterministic fixture continues to use the same reviewed
in-process report boundary and launches no subprocess.

The deterministic fixture worker runs synchronously from a verified brief, emits authenticated
progress, requests exactly one keyed decision, records its resolution, and ends by reporting only
a validation candidate. It supports explicit fault stops before or after each durable-report
boundary. The restart matrix independently cancels
the service and requesting context before and after prepare, binding acknowledgement, and report
acceptance. Stable identities replay one logical effect; an interrupted runtime becomes `unknown`
without relaunch or false success. The fixture launches no subprocess and exists to prove these
properties around the report lifecycle. Fixture composition is available only as a programmatic
test seam; the production service command exposes no fixture-worker flags. Candidate completion
advances only to `validating`; it never claims validation, delivery, or terminal success.

The design authority lives in the private `comisai/planning` repository
(`comis-companion-ecosystem/`), including the implementation design, the common
companion-service architecture contract, the parallel-development process, and the
binding ratification record.

## Commands

- `devcrew-service` — long-lived store and local read-API authority
- `devcrew` — operator control console over the typed local client
- `devcrew-mcp` — stateless five-tool MCP facade over the official SDK stdio transport
- `devcrew-report` — restricted worker brief reader and sparse-report client

All four commands support `--help` and `--version`. Build them with `make build`.

Workers receive `devcrew-report` with `DEV_CREW_ATTACHMENT` set by the launch descriptor to the
Comis-protected task socket at `/run/comis/attachments/<attachmentTargetName>`. The command accepts
no task, run, lease, socket, or credential selector. `brief` reads the exact pinned contract;
`acknowledge` verifies and echoes the socket-bound task/run/lease, actual canonical working
directory, and brief revision before task state may become `working`;
`progress`, `decision`, `blocked`, `paused`, `candidate-complete`, `failed`, and `resolved`
append bounded sparse reports. A candidate report remains non-terminal until service validation.

The first real harness adapter builds a fixed no-shell `codex exec --json` descriptor from an
exact-version static profile. The descriptor validates the activation-returned attachment ID and
target name, references no host socket source, and binds only the exact protected mounted target to
`DEV_CREW_ATTACHMENT`. Task/run/lease and brief authority remain only in the protected attachment,
never argv or the generic bootstrap prompt. Structured activity is classified only
while fresh; a completed turn without a task report is `unknown`. Because the reviewed Codex CLI
does not expose a trustworthy settle signal, the current profile is explicitly degraded and cannot
run unattended. The repository does not infer settled or successful work from a completed Codex
turn and does not invent a private attachment protocol or a second terminal backend.
The canonical `GetLaunchPlan` read invokes that configured adapter with the durable task,
workspace, brief, and activation binding, but projects only the profile ID, terminal allow-entry
ID, brief revision hash, and attachment target with durable source/confidence/freshness metadata.
Executable paths, argv, shell text, environment bindings, workspace paths, and attachment IDs are
not part of the local, CLI, or MCP result.
Production composition accepts this profile only from operator startup configuration, requires a
canonical regular non-symlink executable that is not a shell launcher, and proves the exact pinned
version before opening service endpoints. The fixed adapter owns argv and the protected mount
binding; task or model content cannot redirect either, and lifecycle settling remains degraded.

The minimal read-only service remains available with explicit canonical paths:

```text
devcrew-service --database /absolute/private/state/devcrew.db \
  --socket /absolute/private/run/devcrew.sock
```

The service creates its final state/runtime directories as owner-only, stores the database
as owner-only, and refuses relative, non-canonical, symlinked, broad-root, non-regular, live,
or identity-ambiguous targets. Without explicit flags, both binaries derive the same paths
under the operating system's user configuration directory.

The attachment threat boundary is deliberately narrow: the service rejects symlinks in every
runtime-root component before creating anything, owns each task directory and socket, and never
places the host source path in Codex argv, stdin, or environment. Comis alone carries the source
into the protected mount identified by activation; an altered attachment ID, target name, or mount
path fails closed.

For the runnable Comis/Codex lane, first place the 32–256 character instance bearer in an
owner-private (`0600`) regular file. The Comis control socket must already exist as an owner-only
Unix socket. The primary checkout and worktree parent must be separate canonical directories
under the approved root. Task preparation creates or exactly adopts its operation-bound linked
Git worktree beneath that parent before requesting the Comis workspace lease. Then launch the
installed service without hand-editing generated or runtime files:

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
  --codex-concurrency 2
```

Expose the replaceable official-SDK stdio MCP facade to the MCP client as a separate process:

```text
devcrew-mcp \
  --socket /absolute/private/run/mcp.sock \
  --service-instance service-instance-devcrew
```

The current CLI adapter surface is:

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
devcrew-mcp [--socket PATH] --service-instance ID
```

JSON outputs are stable versioned projections. Human and YAML views are presentation only
and carry no authority. The generated authenticated client is verified against Comis's standalone
test-only capability-service host over a real owner-only Unix socket, including exact protocol and
digest agreement plus altered-digest and wrong-credential rejection. The installed integration
test extends that conformance evidence through activation and the production-composed Codex launch
plan without starting an unconstrained worker process. The CLI never opens SQLite as a
normal-operation fallback. Task preparation reads one strict bounded JSON contract, rejects
unknown authority fields, and uses either the explicit stable operation ID or one locally minted
request ID. Service-side mutation composition requires an explicit repository catalog, task and
registration identity sources, service instance, expiry, and dedicated MCP socket. The installed
Codex profile supplies those dependencies only when its complete flag set and exact version probe validate; otherwise
the minimal service does not open an alternate writer. The SDK facade package is implemented and
tested over its official in-memory transport before the command root uses the production stdio
transport. The MCP command defaults to a separate `mcp.sock` and validates the out-of-band service
instance against every private call context. Tagged integration tests include the full
direct/CLI/MCP parity matrix.

## Development

Read [AGENTS.md](AGENTS.md) before changing the repository. Install the repository hook
with `git config core.hooksPath .githooks`, then use:

```text
make verify       # handoff gate
make verify-full  # push/readiness gate, excluding the protected live campaign
```

The protected E0 live campaign remains intentionally separate as `make test-live` and is
not yet implemented. See [DEPENDENCIES.md](DEPENDENCIES.md) for accepted runtime dependency
provenance and [CLAUDE.md](CLAUDE.md) only for Claude-specific operational notes.

## License

Apache-2.0 — see [LICENSE](LICENSE).
