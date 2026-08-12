# Pinned Comis capability-service protocol

This directory contains the exact language-neutral bundle consumed by the Go adapter. The
manifest owns the schema and fixture inventory; `provenance.json` records the immutable Comis
source commit, protocol identifier, bundle digest, and generator identity.

Refresh the pin only from an explicit clean Comis worktree and full commit:

```sh
make protocol-sync COMIS_ROOT=/absolute/path/to/comis COMIS_COMMIT=<full-commit>
```

Verify artifact hashes, the aggregate digest, provenance, and a temporary regeneration against
the committed Go output with:

```sh
make protocol-check
```

Generated Go files remain under `internal/comiswire/`, carry the standard generated marker,
and are never edited by hand.

## Authority and threat boundary

The pinned manifest and provenance are authenticated inputs to generation. Generation fails
closed if the accepted protocol identifier, bundle digest, schema inventory, or closed method
catalog changes. The service-side client exposes handshake, health, report, evidence, attention-
response receive, and workspace release. Generated activate, abandon, and terminal-event DTOs
are inbound handler contracts and cannot be used as outbound client methods. Strict runtime validation rejects unknown or duplicate fields, trailing JSON, invalid
closed discriminators, operation-envelope disagreement, response identity drift, and size-limit
violations before they can cross the adapter boundary.
Negotiated scope arrays are treated as duplicate-insensitive sets: ordering grants no authority,
every requested scope must be active, and any unexpected grant is rejected. The manifest method
list and method catalog are likewise matched by unique method name rather than position; MCP tool
catalog verification already uses the same name-set posture.

`make test-conformance` materializes the manifest's digest token and evaluates every pinned
fixture through the generated schema dispatch. The canonical examples decode into generated DTOs
and serialize back to identical compact JSON bytes. Replay, envelope identity, and combined UTF-8
report-size behavior are checked by a direct test evaluator; it is not a socket listener and does
not stand in for a runnable Comis host.

## Live handshake status

The pinned Comis source includes a test-only standalone Unix-socket fixture host. Cross-repository
conformance launches that host explicitly from the pinned source and verifies the generated Go
client's authenticated handshake, exact digest, credential rejection, and closed wire schemas.
The fixture host is validation infrastructure, not a production capability service.
