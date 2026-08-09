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
catalog changes. The service-side client exposes only handshake, health, and report; generated
activate and abandon DTOs are inbound handler contracts and cannot be used as outbound client
methods. Strict runtime validation rejects unknown or duplicate fields, trailing JSON, invalid
closed discriminators, operation-envelope disagreement, response identity drift, and size-limit
violations before they can cross the adapter boundary.

`make test-conformance` materializes the manifest's digest token and evaluates every pinned
fixture through the generated schema dispatch. The canonical examples decode into generated DTOs
and serialize back to identical compact JSON bytes. Replay, envelope identity, and combined UTF-8
report-size behavior are checked by a direct test evaluator; it is not a socket listener and does
not stand in for a runnable Comis host.

## Live handshake status

At the pinned source commit, the SDK has no `bin` entry and exposes scripts only for building,
testing, and protocol generation/checking. The only capability-service fixture host is under the
daemon's Vitest-only `src/__tests__/` surface, with no standalone socket listener or runnable host
entry. Consequently, a real cross-repository socket handshake cannot be executed from this lane.
No local server is used as substitute evidence; this is the sole remaining cross-repository
validation gap.
