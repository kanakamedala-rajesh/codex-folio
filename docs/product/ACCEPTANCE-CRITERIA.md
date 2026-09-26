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
- **PL-04:** Bare `codex-folio` guides required one-time setup, starts or reuses one usable companion, highlights eligible Selected Profile in the picker, and starts installed Codex in the foreground with native input/output, arguments, working directory, signals, and exit status. Selection does not release the usable service before launch. No unsafe/missing selection silently substitutes another identity.
- **PL-05:** `codex-folio launch <alias> -- ...` is deterministic, scriptable, and does not change Selected Profile.
- **PL-06:** Changing Selected Profile synchronizes the dashboard and future interactive launches; it never alters a running Launch Profile.
- **PL-07:** Dashboard scope changes during a managed launch show an explicit warning and the next launch behavior remains unambiguous.
- **PL-08:** Managed and Referenced Identity Homes have distinct ownership-aware removal behavior.
- **PL-09:** Removing a managed profile requires typed alias confirmation, quarantines local owned data for seven days by default, supports restoration, and clearly states that the remote identity is unaffected.
- **PL-10:** Non-interactive flows fail closed; `--yes` cannot provide experimental consent, workspace-transfer attestation, telemetry enrollment, collection consent, browser trust, or OS-login enrollment.

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
- **CT-10:** An interrupted Safe Continuation start with no process report remains non-authorizing during the same OS boot session; recovery after a verified boot-session change restores the retained checkpoint and reruns current handoff checks without automatically terminating a process.

## E. Storage, privacy, and security

- **SP-01:** Vault keys are never stored beside encrypted records; unavailable secure storage fails closed with actionable recovery.
- **SP-02:** DPAPI, Keychain, supported Linux secure storage, explicit passphrase mode, and the WSL2 Windows-user-backed integration have platform-specific integration tests. Prompt-free everyday qualification requires actual native protection and restart evidence, not adapter mocks or compile-only checks; arbitrary unsupported headless configurations and explicit passphrase mode do not satisfy it (ADR 0035).
- **SP-03:** Sensitive fields use envelope encryption while normal aggregate facts remain queryable.
- **SP-04:** SQLite has transactional migrations, integrity checks, three rotating backups, and explicit restore; corruption never causes a silent reset.
- **SP-05:** Default analytics retention is 13 months and is configurable from 30 days to unlimited.
- **SP-06:** Analytics purge is independent of profile/credential removal and is available through explicit CLI and UI flows.
- **SP-07:** Loopback API requires local-launcher bootstrap authorization for new/private browsers; explicitly granted, revocable browser trust may renew distinct short-lived sessions across ordinary browser/service/machine restarts. Provide current-browser/all-browser revocation and repeatable reopening independent of a consumed bootstrap URL or prior ephemeral address. Trust ends on revocation, cleared browser state, or authorization reset. Preserve one-time bootstrap, loopback binding, Host/Origin/CSRF, no permissive CORS, and server-side secrets (ADR 0036).
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

- **OP-01:** On-demand collection and foreground launch work without OS-login service enrollment and after background collection refusal. Plain startup automatically starts/reuses one companion; automatic availability is not collection consent.
- **OP-02:** Native OS-login user-service enrollment is explicit, separately optional, reversible, unprivileged, and never silently enabled. The on-demand companion survives Codex exit; explicit stop offers deferral while Managed Launches run and never kills Codex. Forced external termination must not claim tracking continuity.
- **OP-03:** Periodic collection respects active/idle intervals, reset boundaries, provider-safe minimum refresh, backoff, and jitter.
- **OP-04:** The service is the sole SQLite/vault/snapshot writer and prevents competing generations or migrations.
- **OP-05:** Operational alerts are limited to the agreed MVP set and do not become a general rule engine.
- **OP-06:** Update checks are opt-in network activity and provide notification only, not in-place self-update.
- **OP-07:** User-generated shell integration is inspectable and sourced manually; PATH setup is a one-time explicit user-approved action with guidance where unavailable; direct binary execution remains supported. Startup files are never changed silently and the Codex binary is never rewritten.
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

<a id="everyday-companion-milestone-5a"></a>

## I. Milestone 5A everyday companion (#82)

These accepted requirements govern subsequent executable tickets; #83 records contracts, not implementation or qualification evidence (ADRs 0035–0037).

- **EC-01:** Qualified native secure storage permits repeated ordinary use without a routine CodexFolio passphrase or specialist service/unlock commands. Setup and recovery stay within plain startup. Explicit passphrase mode remains available; migration allows one final old-passphrase unlock, preserves profiles/history/retained state, and remains recoverable after failed protection or interruption. No plaintext, colocated key, silent replacement key, or reset downgrade is permitted. WSL helper/transport secrets never enter arguments, environment, URLs, logs, or browser responses.
- **EC-02:** Existing homes can be registered in place with valid Codex authentication and an explanation of sharing with direct Codex; additional identities default to separate Managed Identity Homes. Dashboard onboarding immediately shows copyable terminal instructions and waiting state, detects actual terminal completion, validates readiness, and refreshes inventory without resubmitting authentication. Interrupted Pending Profiles remain resumable and unselectable until ready. Codex continues to own login/refresh; browser projections exclude credentials and raw auth output.
- **EC-03:** Background metadata collection is explained/offered once and the choice remembered separately from ordinary companion startup, history import, telemetry, and OS-login enrollment. Dashboard/analytics failures preserve safe foreground launching; inability to establish identity safely requires actionable recovery without identity substitution.
- **EC-04:** Offer discovered supported history sources for explicit import consent separately from registration; read source files without mutation. Unknown ownership remains Unassigned History, never inferred solely from current login or represented as a selectable profile. Show it in labeled overall history, exclude it from individual-profile and Combined Identity View totals, and preserve Combined Identity View as a profile aggregate.
- **EC-05:** Single/bulk assignment, reassignment, and return to Unassigned preserve immutable source/session identity and attribution provenance independently from mutable assignment. User assignment is not evidence-verified provider ownership. Reimports/additional registrations deduplicate without conflating unrelated sessions by timestamp; corrections consistently update aggregates without changing source files. Migrations retain existing profile-linked observations without strengthening their evidence.
- **EC-06:** Import all supported available historical metrics with availability, freshness, source, units, and coverage. Absence is not zero; current quota/credit values cannot reconstruct historical provider snapshots. Allowlisted metadata exclusions and existing retention, purge, safe export, and canonical-project-path privacy apply to imported and assigned records.
- **EC-07:** Automated complete CLI/service and authenticated-browser journeys cover picker-to-launch, preserved child input/status/signals, one owner, migration/recovery, post-child lifetime/deferred stop, onboarding completion, trust renewal/revocation, consent, history deduplication/assignment, and negative browser/security boundaries. Changed flows retain Signal Rail accessibility, keyboard/status behavior, narrow layouts, and zoom. Focused vault fault injection supplements journey tests.
- **EC-08:** Mandatory delivered-candidate exit evidence demonstrates installed Codex with two real authenticated identities on Windows AMD64, macOS ARM64, Linux AMD64 with supported secure storage, and WSL2 with actual Windows-user-backed protection. Each platform covers setup/addition, completion detection, repeated switching/foreground launch, dashboard reopening, companion availability after Codex exits, and repetition after environment restart without routine CodexFolio passphrase or separate service/unlock commands. Record sanitized environment/prerequisites, actions/results, candidate identity, and limitations. Fake-provider, compile-only, foreground-helper-only, or prior milestone exceptions cannot replace this evidence; missing native/live proof leaves M5A incomplete. Independent milestone audit and required canonical verification remain mandatory. M6 experiments and M7 release hardening keep their separate gates.

Required EC-08 qualification matrix (requirements only; no results asserted by #83):

| Native configuration | Secure-storage proof | Required real-user journey |
|---|---|---|
| Windows AMD64 | User-scoped DPAPI after environment restart | Complete EC-08 with installed Codex and two authenticated identities |
| macOS ARM64 | Qualified Keychain access after environment restart | Complete EC-08 with installed Codex and two authenticated identities |
| Linux AMD64 | Supported secure storage with recorded prerequisites after environment restart | Complete EC-08 with installed Codex and two authenticated identities |
| WSL2 | Actual Windows-user-backed integration, prerequisites and detached companion after WSL/environment restart | Complete EC-08 with installed Codex and two authenticated identities; compilation or foreground helper success is insufficient |
