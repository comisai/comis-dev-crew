# Dependency decisions

Runtime dependencies are admitted only for concrete callers and remain exact-pinned in
`go.mod`. The automated policy in `tools/licenses.json` allows permissive license classes
globally and records any other accepted license against an exact module version.

## Pure-Go SQLite

`modernc.org/sqlite` version `v1.56.0` provides the service-owned durable SQLite adapter.
It is pure Go, so the supported Darwin and Linux targets cross-compile without CGO or a
platform C toolchain. The acceptance spike verifies WAL mode, foreign keys, bounded lock
contention, transactional migration rollback, owner-only storage, race detection, and all
four release target builds. Module content is authenticated by the Go checksum database
and scanned by `govulncheck`.

The reachable dependency closure includes `github.com/hashicorp/golang-lru/v2` version
`v2.0.7`, licensed under MPL-2.0. That exact version is a reviewed exception rather than a
global license-class allowance. MPL-2.0's file-level source and notice obligations apply to
redistribution of its covered code; a future release process must preserve the upstream
license and make the covered source available. Any version change fails the license gate
until it receives a new explicit review.
