# Local diagnostic export evidence for #75

Ticket #75 extends the accepted Signal Rail Settings surface with independent
local diagnostic controls and preview-before-download support evidence. The
production UI preserves the reviewed operational hierarchy and labels the
default plainly as `Enabled · 14 days · 50 MB`.

## Behavioral evidence contract

- SQLite schema v22 persists enablement, `info`/`warning`/`error` severity
  floor, and 1–30 day retention independently. Recording, settings changes,
  and reads enforce age, severity, aggregate-count, occurrence-count, and
  encoded bundle bounds.
- The versioned bundle allowlists application/schema versions, OS
  family/architecture, coarse feature states, safe service/database/vault
  health, current controls, and stable component/error aggregates. It has no
  fields for identities, workspaces, projects, paths, usage, sessions, Codex or
  repository content, credentials, arguments, analytics, checkpoints, or
  configuration payloads.
- CLI and browser export require an exact server-created preview digest. The
  exported document is the cached document reviewed in that preview; changed
  settings or a stale confirmation cannot export it. CLI writes use exclusive
  creation, so an existing destination is preserved.
- The authenticated loopback API retains session, Host/Origin, CSRF, and
  command-token boundaries. The generated Go and TypeScript clients are
  derived from the versioned OpenAPI contract. No export action uploads data,
  opens an issue, enables telemetry, or enrolls a service.
- The deep real-service Playwright journey exercises persisted controls,
  cancellation, exact preview/export, the browser download, narrow reflow, and
  automated WCAG checks against real SQLite and the test vault. The downloaded
  JSON is inspected for the allowlisted schema and forbidden sentinels.

## Verification ledger

| Evidence | Result and qualification |
| --- | --- |
| Diagnostics domain/store tests | Pass: default and bounded settings, migration, persistence, filtering, retention cleanup, safe aggregates, exact preview/export, stale confirmation, cancellation, and size limits. |
| CLI and HTTP integration tests | Pass against the real loopback service: authorization/CSRF, persisted settings, dry-run, interactive confirmation, non-interactive rejection, exclusion checks, and non-replacement. |
| Deep browser journey | Pass in headless Chromium on WSL2/Linux AMD64: generated client, settings, preview/cancel/download, automated WCAG scan, forced colors, reduced motion, narrow layout, and Chromium page scaling. |
| Native/non-WSL runtime | Not performed under the owner-approved verification exception below. OS/architecture values are safe runtime projections; non-host target evidence remains compile-only. |
| Manual assistive technology | Spoken screen-reader, native OS contrast, Safari/Firefox, and non-Playwright manual checks were unavailable and remain unqualified. |

### Accepted verification exception

On 2026-09-19, the repository owner directed all remaining issue
implementations to proceed without non-WSL verification and without validations
that cannot be performed through the Playwright browser tooling. For #75,
native Windows, macOS, and non-WSL Linux runtime behavior and unavailable manual
assistive-technology checks therefore remain explicitly unqualified. WSL2 is
not relabeled as native Linux evidence, and cross-compilation is not runtime
qualification.

This exception does not waive WSL2 unit/integration tests, real authenticated
loopback browser coverage, generated-contract checks, canonical verification,
review, or hosted pipeline evidence. It also does not weaken the exclusion,
local-only, exact-preview, non-replacement, or authorization contracts.
