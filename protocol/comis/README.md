# Pinned Comis capability-service protocol

This directory contains the exact language-neutral bundle consumed by the Go adapter. The
manifest owns the schema and fixture inventory; `provenance.json` records the immutable Comis
source commit, protocol identifier, bundle digest, and generator identity.

Refresh the pin only from an explicit clean Comis worktree and full commit:

```sh
make protocol-sync COMIS_ROOT=/absolute/path/to/comis COMIS_COMMIT=<full-commit>
```

Verify artifact hashes, the aggregate digest, and provenance with:

```sh
make protocol-check
```

Generated Go files remain under `internal/comiswire/`, carry the standard generated marker,
and are never edited by hand.
