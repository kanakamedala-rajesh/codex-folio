# VenkataSudha CodexFolio Architecture

## Architectural style

VenkataSudha CodexFolio is a modular monolith: one repository, one Go module, one frontend workspace, one native executable, and one user-scoped state-owning service. It uses ports and adapters only at real external seams. The objective is a small set of deep module interfaces with high leverage and locality, not a layer or interface for every type.

The executable has several process roles selected by commands—interactive CLI, foreground launcher, on-demand/persistent service, migration helper—but they share one compiled implementation and release version.

## Dependency direction

```text
CLI commands ─┐
HTTP handlers ├──> application workflows ──> domain modules
scheduler ────┘              │
                             └──> injected ports at external seams
                                      │
                         production and test adapters
```

- Domain modules contain vocabulary, invariants, ranking, provenance, retention, and lifecycle policies without transport, SQL, filesystem, browser, or Codex protocol knowledge.
- Application workflows coordinate domain modules and external ports for onboarding, selection, launch preparation, collection, alerts, export, purge, and continuation.
- Adapters contain Codex CLI/App Server protocol details, platform vault/service/notification behavior, SQLite, filesystem operations, HTTP, update checks, and telemetry transport.
- The composition root in `cmd/codex-folio` selects platform adapters and constructs modules. Modules do not locate their own dependencies.

## Feature-owned deep modules

Suggested ownership, subject to interface design during implementation:

- `internal/profile`: Identity Profile registry, Selected Profile, home ownership, duplicate warnings, quarantine, and restoration.
- `internal/launch`: Codex discovery/capabilities, Launch Plan creation, Managed Launch lease reconciliation, and foreground lifecycle reporting.
- `internal/usage`: allowlisted collection, normalization, freshness, availability, snapshots, aggregation, and recommendation eligibility.
- `internal/activity`: Managed Launch and Observed Session chronology, supported correlation, projects, filters, and export projections.
- `internal/continuation`: repository-first checkpoint creation/review state, Safe Continuation launch preparation, and isolated experimental adapter orchestration.
- `internal/sharedhome`: authentication-only switching transaction, vault generations, lock/journal recovery, and compatibility checks.
- `internal/configpack`: immutable pack versions, projection, local overrides, diff/review, and explicit promotion.
- `internal/alerts`: condition evaluation, deduplication, acknowledgement, delivery privacy, and history.
- `internal/settings`: retention, diagnostics, telemetry consent, locale/appearance, service, and experimental-feature preferences.

Package names describe ownership, not thin technical layers. Internal helper seams remain private unless callers genuinely need them.

## External seams and adapters

Real seams exist where production and test behavior both matter:

- **Installed Codex:** production CLI/App Server adapters plus fake-Codex and protocol-fixture adapters.
- **State store:** SQLite adapter plus an isolated test store/migration harness.
- **Vault:** DPAPI, Keychain, Secret Service, passphrase-file, and in-memory test adapters.
- **Platform lifecycle:** Task Scheduler, LaunchAgent, systemd-user, and fake adapters.
- **Notification delivery:** native OS notification adapters plus a recording test adapter.
- **Clock and scheduling:** system clock/timer adapters plus deterministic test time.
- **Filesystem/process operations:** native adapters plus fault-injection adapters for atomicity, locking, permissions, signals, and crash recovery.
- **Remote CodexFolio endpoints:** update and telemetry HTTPS adapters plus disabled and recording adapters.

Do not add a port merely to mock a pure function or wrap a single implementation. Pure domain computation is tested directly; local-substitutable dependencies use their real local test form where practical.

## State-owner and foreground-launch protocol

The service alone opens writable SQLite and vault state. A foreground launch avoids routing terminal I/O through that service:

1. CLI requests a Launch Plan for a profile and Codex arguments.
2. Service validates companion-owned preconditions, records a pending Managed Launch lease, and returns executable path, working directory, non-secret environment changes such as the Identity Home, arguments, and lease identifier.
3. CLI starts the installed Codex executable as its foreground child with native stdin/stdout/stderr and signal handling.
4. CLI reports start/PID and final exit status to the service.
5. Service reconciles abandoned leases after CLI crash using conservative process checks; it never assumes that absence of a report means Codex was safely terminated.

Deterministic `launch <profile>` does not mutate Selected Profile. Codex owns its session and execution behavior throughout.

## Browser seam

The loopback HTTP interface is versioned through OpenAPI. TypeScript transport types and client calls are generated; handwritten frontend code consumes domain-oriented response models and stable error codes. Credentials, encrypted values, raw source responses, full paths by default, and Codex protocol objects never cross into the SPA.

The service hosts immutable embedded assets under a restrictive CSP and authorizes the browser using the settled one-time bootstrap and short-lived local session.

## Persistence

SQLite stores normalized allowlisted facts, aggregates, lifecycle records, settings, and encrypted blobs. Migrations are ordered, transactional where supported, backed up before risky changes, and exercised forward and backward in tests. The vault performs envelope encryption for sensitive SQLite fields and owns key generation/rotation; SQLite never stores credentials.

## Verification architecture

Tests use the same deep module interfaces as production callers:

- pure domain tests for policies and aggregation;
- workflow tests with recording/fault-injection adapters;
- SQLite and migration integration tests;
- fake-Codex process and App Server contract tests;
- browser contract, accessibility, and E2E tests against the real loopback service;
- platform-native lifecycle and archive smoke tests;
- stricter crash/rollback/manual qualification for experimental adapters.

Tests assert observable outcomes through module interfaces rather than internal call sequences, allowing implementations to evolve without rewriting the suite.

## Initial repository shape

```text
cmd/codex-folio/
internal/profile/
internal/launch/
internal/usage/
internal/activity/
internal/continuation/
internal/sharedhome/
internal/configpack/
internal/alerts/
internal/settings/
internal/platform/
internal/store/
internal/httpapi/
web/
api/
packaging/
docs/
```

`internal/platform`, `internal/store`, and `internal/httpapi` contain adapters and composition support; domain policy must not migrate into them.
Every new top-level `internal` package must be classified by the repository
architecture check before it can enter the build. Unclassified packages fail
verification so a newly named adapter cannot silently acquire feature-module
dependency privileges.
