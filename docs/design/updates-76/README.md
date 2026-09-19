# Issue #76 update-check evidence

The update workflow has one state owner in `internal/updates`. Reads and consent
changes perform no network access. An explicit check performs one source call
without changing automatic-check consent. Automatic checks require the saved
opt-in and a persisted due time.

Production composition uses the unconfigured source because the repository has
no approved release endpoint or download-origin contract. The HTTPS adapter is
qualified with recording fixtures that enforce a bounded strict manifest,
HTTPS-only origins, rejected redirects, and an immutable download-path
allowlist. It reads metadata only and cannot download, open, execute, install,
or replace software.

Browser and CLI surfaces show release notes, download location, and installer
guidance only after valid `update_available` evidence. Offline, unavailable,
malformed, and unconfigured states remain non-fatal to local use.

## Verification scope

WSL unit, integration, real-service browser, generated-contract, architecture,
and canonical repository checks are required. Per the user-approved exception,
native non-WSL verification and validation that cannot be exercised with the
available Playwright browser tooling are recorded as skipped; fixture evidence
does not claim a live production endpoint, installer, signing, deployment, or
native-platform qualification.
