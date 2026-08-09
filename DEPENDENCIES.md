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

## Official Go MCP SDK

`github.com/modelcontextprotocol/go-sdk` version `v1.6.1` is the official Go implementation
used only by the stateless `devcrew-mcp` facade. The pin resolves to upstream commit
`d454bbaf06a342aee5336df3370321d9cdec2478`, requires Go 1.25 or newer, and is compatible
with this repository's Go 1.26 toolchain. Its module checksum is
`h1:0zOSupjKUxPKSocPT1Wtago+mUHU2/uZ4xSOY0FGReU=`. The reviewed upstream distribution is in
an MIT-to-Apache-2.0 transition and includes both grants; both license classes are allowed by
the repository policy. The facade uses the typed tool registration, private MCP metadata,
in-memory test transport, and stdio production transport only. Version changes require a new
API, license, checksum, vulnerability, and reachability review.
