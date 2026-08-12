# Security policy

## Project status

`comis-dev-crew` is pre-release software under active development. No version is
supported for production use, and no security guarantees are offered for any
build. Do not run it against repositories, credentials, or hosts you are not
prepared to lose access to.

## Supported versions

None. The repository has no tagged release. Security fixes land on `main` only.

## Reporting a vulnerability

Report privately. Do not open a public issue for a suspected vulnerability.

Use GitHub private vulnerability reporting:
<https://github.com/comisai/comis-dev-crew/security/advisories/new>

Please include the affected commit, the component (`devcrew-service`, `devcrew`,
`devcrew-mcp`, `devcrew-report`, or a shared adapter), a reproduction, and the
impact you believe it has. Redact credentials, tokens, socket paths, and worker
output from anything you attach.

Expect an acknowledgement within 7 days. Because this is a pre-release project
maintained without a funded security team, remediation timelines are best effort
and will be agreed with you on the advisory thread.

## Scope

The following are in scope and treated as security-relevant:

- Authority escalation across the local API, MCP facade, or reporter seam,
  including any path where worker or model content grants capability.
- Escapes from filesystem containment: worktree, runtime root, attachment mount,
  or approved-root resolution, including symlink and TOCTOU variants.
- Credential exposure in tracked files, arguments, logs, reports, events, or
  diagnostics.
- Introduction of a second durable writer to the SQLite store, or a replay or
  reconciliation defect that turns an unknown outcome into a claimed success.
- Command execution reachable from worker, model, forge, or protocol content.

The following are out of scope:

- Findings that require an already-compromised owner account, since the local
  API, database, and runtime directories are owner-only by design.
- Missing hardening in code paths explicitly marked as not yet implemented,
  including the protected live campaign and unratified deferred stages.
- Reports produced solely by automated scanners without a demonstrated impact.

## Design posture

The threat model and the invariants this project refuses to weaken are recorded
in [AGENTS.md](AGENTS.md), primarily its Security section. A report showing that
a stated invariant does not hold in the implementation is always welcome, even
when the practical impact is limited.
