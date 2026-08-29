# Version and compatibility policy

CodexFolio versions three contracts independently. A change must update the
contract it actually affects; one version must not stand in for another.

## Product version

`internal/buildinfo/version.txt` is the single product version source and uses
Semantic Versioning. Before `1.0.0`, incompatible product behavior increments
the minor version; compatible fixes increment the patch version. From `1.0.0`,
incompatible behavior increments the major version, compatible capability
increments the minor version, and compatible fixes increment the patch version.
Prerelease identifiers do not qualify an artifact as stable.

## Loopback API version

`api/openapi.json` owns the browser boundary. Its
`x-codex-folio-api-version` value is a `v<major>` namespace independent of the
product version. Compatible additions retain the API major. Removing or
reinterpreting a field, operation, error meaning, or authorization expectation
requires a new API major, an ADR, updated generated clients, and explicit
compatibility handling. The OpenAPI `info.version` continues to identify the
product build that published the contract; it is not the API namespace.

## Persisted schema version

Milestone 1 will introduce an integer SQLite schema version and ordered
migrations independent of both product and API versions. Any persisted-shape
change requires a migration decision covering forward application, backup,
failure recovery, and supported rollback or explicit non-rollback behavior.
Destructive, lossy, or compatibility-window changes require an ADR before
implementation. Phase 0 defines this policy but creates no database or schema.

Stable error identifiers are governed separately by the
[error-code policy](ERROR-CODES.md); retiring one does not permit reuse.
