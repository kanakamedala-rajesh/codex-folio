# VenkataSudha CodexFolio acceptance criteria

Status: accepted implementation evidence contract

Each criterion needs automated evidence where feasible and a recorded manual result where platform or accessibility behavior cannot be automated reliably.

## A. Product boundary

- **PB-01:** Every launched process is the user-installed Codex executable; CodexFolio does not proxy model traffic or implement a replacement conversation UI.
- **PB-02:** Stable behavior uses only documented Codex/App Server interfaces, eligible local metadata, and CodexFolio-owned records.
- **PB-03:** Analytics storage contains no raw prompt, response, tool payload, repository diff, transcript, credential, cookie, or raw provider response.
- **PB-04:** Dashboard operations cannot mutate remote OpenAI identity or workspace information.
- **PB-05:** No CodexFolio artifact is written inside the working repository unless the user explicitly chooses a destination for an export.

## B. Profiles, authentication, and launching

- **PL-01:** A user can create and validate at least two profiles with distinct Identity Homes.
- **PL-02:** Browser and device-code authentication are delegated to the installed Codex and can be selected explicitly.
- **PL-03:** After successful onboarding, routine switching reuses Codex-managed authentication until Codex requires reauthentication.
- **PL-04:** Bare `codex-folio` opens an interactive selector and starts Codex in the foreground.
- **PL-05:** `codex-folio launch <alias> -- ...` is deterministic, scriptable, and does not change Selected Profile.
- **PL-06:** Changing Selected Profile synchronizes the dashboard and future interactive launches; it never alters a running Launch Profile.
- **PL-07:** Dashboard scope changes during a managed launch show an explicit warning and the next launch behavior remains unambiguous.
- **PL-08:** Managed and Referenced Identity Homes have distinct ownership-aware removal behavior.
- **PL-09:** Removing a managed profile requires typed alias confirmation, quarantines local owned data for seven days by default, supports restoration, and clearly states that the remote identity is unaffected.
- **PL-10:** Non-interactive flows fail closed; `--yes` cannot provide experimental consent, workspace-transfer attestation, plaintext acceptance, or telemetry enrollment.

## C. Usage and activity evidence

- **UA-01:** The default Dashboard Scope is the Selected Profile; Combined Scope is explicit.
- **UA-02:** Provider-reported limits, reset time, credits, and online summaries retain source and freshness.
- **UA-03:** Locally derived tokens, Estimated quota-window delta, and Observed during session code changes use their exact labels.
- **UA-04:** UI and exports never call estimated quota delta “session weekly usage” or observed changes “lines written by Codex.”
- **UA-05:** Missing, stale, unsupported, partial, and contradictory evidence is distinguishable from a reported zero.
- **UA-06:** Capacity ranking applies compatibility gates first and uses “best” only with sufficiently recent provider evidence.
- **UA-07:** Combined metrics deduplicate shared login/workspace contexts and overlapping evidence according to documented rules.
- **UA-08:** Managed Launch, Observed Session, and correlation confidence remain separate facts.
- **UA-09:** Project identity does not expose canonical paths in ordinary views, diagnostics, telemetry, or unencrypted exports.
- **UA-10:** JSON/CSV export includes normalized values, scope, time window, provenance, and freshness without excluded content.

## D. Continuation

- **CT-01:** Safe Continuation requires the source Codex process to stop before the target launch.
- **CT-02:** A repository-first checkpoint can be previewed, edited, approved, and used to start a new target-profile Codex process in the same working directory.
- **CT-03:** The checkpoint accurately separates goal, completed work, pending work, known validation, risks, and next action.
- **CT-04:** Repository inspection does not execute arbitrary project code or tests automatically.
- **CT-05:** Transcript-assisted recovery is opt-in per handoff, offers preview/edit/approval, persists only sanitized content, and uses a shorter retention period.
- **CT-06:** Encrypted `.cfolio` export is separate from analytics and configuration exports.
- **CT-07:** Exact Continuation requires explicit intent every time, a release-signed allowlist, stopped processes, backup, validation, and rollback.
- **CT-08:** Every Exact Continuation failure leaves the source/destination recoverable and offers Safe Continuation.
- **CT-09:** Cross-login/workspace exact attempts require explicit user authorization attestation and are never described as guaranteed.

## E. Storage, privacy, and security

- **SP-01:** Vault keys are never stored beside encrypted records; unavailable secure storage fails closed with actionable recovery.
- **SP-02:** DPAPI, Keychain, Secret Service, and headless passphrase paths have platform-specific integration tests.
- **SP-03:** Sensitive fields use envelope encryption while normal aggregate facts remain queryable.
- **SP-04:** SQLite has transactional migrations, integrity checks, three rotating backups, and explicit restore; corruption never causes a silent reset.
- **SP-05:** Default analytics retention is 13 months and is configurable from 30 days to unlimited.
- **SP-06:** Analytics purge is independent of profile/credential removal and is available through explicit CLI and UI flows.
- **SP-07:** Loopback API requires bootstrap authorization and a short-lived session, validates Host/Origin/CSRF, and does not expose permissive CORS.
- **SP-08:** Logs and local diagnostics are bounded and contain no identity, Codex content, repository content, canonical project path, or secret.
- **SP-09:** Remote telemetry is off by default, separately consented, non-blocking, inspectable, and contains only its narrow product-health schema.
- **SP-10:** Raw telemetry events expire after 30 days and anonymous product-health aggregates after 13 months.

## F. Dashboard and accessibility

- **UX-01:** Overview answers continuation readiness with current limits first and eligible alternatives second.
- **UX-02:** The six-route hierarchy is Overview, Profiles, Sessions, Analytics, Alerts, and Settings.
- **UX-03:** Narrow screens preserve overview, profile selection, alerts, and handoff with no essential horizontal scrolling.
- **UX-04:** Stale/offline/partial states retain layout and last-known values, prominently show evidence age, and do not substitute zero.
- **UX-05:** Dark and light themes are complete; system theme is the default.
- **UX-06:** All core journeys meet WCAG 2.2 AA, keyboard operation, visible focus, reduced motion, high contrast resilience, 200% zoom, and status announcements.
- **UX-07:** Every semantic chart has an equivalent accessible table and meaningful non-color encoding.
- **UX-08:** English strings are externalized; dates, times, numbers, time zones, and week boundaries use operating-system locale.
- **UX-09:** Accepted route/state comps and visual regression baselines exist before the frontend milestone closes.
- **UX-10:** Frontend code uses the generated API client; contract drift fails CI.

## G. Service and platform operation

- **OP-01:** On-demand collection works with no service installed.
- **OP-02:** Native user-session service install is explicit, reversible, unprivileged, and never silently enabled.
- **OP-03:** Periodic collection respects active/idle intervals, reset boundaries, provider-safe minimum refresh, backoff, and jitter.
- **OP-04:** The service is the sole SQLite/vault/snapshot writer and prevents competing generations or migrations.
- **OP-05:** Operational alerts are limited to the agreed MVP set and do not become a general rule engine.
- **OP-06:** Update checks are opt-in network activity and provide notification only, not in-place self-update.
- **OP-07:** User-generated shell integration is inspectable and sourced manually; startup files and the Codex binary are never rewritten automatically.
- **OP-08:** Stable capabilities work on Windows AMD64, Linux AMD64/WSL2, and macOS ARM64.

## H. Release gate

- **RG-01:** Required format, lint, type-check, unit, integration, contract, migration, end-to-end, security, accessibility, visual, and platform checks pass.
- **RG-02:** Regression budgets are measured and no unexplained regression is accepted.
- **RG-03:** Stable archives include checksums, SBOM, provenance, Sigstore artifacts, and native platform signing/notarization.
- **RG-04:** Install, service enrollment, upgrade, backup restore, uninstall, and retained-data behavior are tested on clean Tier 1 systems.
- **RG-05:** Current and previous two documented Codex versions are tested where available; untested versions expose capability-level status without blocking basic launch.
- **RG-06:** Experimental capability failure or disablement does not prevent stable launch, usage views, exports, or Safe Continuation.
- **RG-07:** Apache-2.0, DCO, security policy, privacy notice, support matrix, and known limitations ship with the release.
- **RG-08:** Formal name clearance is complete before the working name is used for stable publication.
