# VenkataSudha CodexFolio implementation plan

Status: accepted implementation baseline, including approved M5A contract (#82); implementation and native qualification require separate evidence

## 1. Outcome

VenkataSudha CodexFolio will be a local-first companion for the installed Codex application. Its primary answer is: **Can I continue working, and which Identity Profile should I use?**

The MVP is one native Go executable containing:

- a foreground CLI launcher;
- a user-scoped local service and state owner;
- an authorization-protected loopback API;
- SQLite metadata and aggregate storage;
- platform-backed credential protection; and
- a React, TypeScript, and Vite SPA embedded as versioned static assets.

CodexFolio does not replace Codex. Codex remains responsible for authentication UI, models, sessions, tools, repository work, and execution. CodexFolio selects an Identity Home, launches the installed Codex executable, observes supported metadata, and helps the user start a fresh Codex process with enough approved context to continue repository work.

## 2. Non-negotiable boundaries

1. Use only documented Codex/App Server interfaces, eligible local Codex metadata, and CodexFolio-owned records. Do not scrape web UI or depend on undocumented provider endpoints.
2. Never collect raw prompts, responses, tool payloads, repository diffs, or transcripts for analytics.
3. Never place `.codex-folio` files or other continuation state inside a repository.
4. Never claim live identity switching or exact cross-workspace thread transfer. A running Codex process retains its Launch Profile.
5. Guarantee repository-first Safe Continuation; keep Exact Continuation experimental, explicit, allowlisted, transactional, and replaceable by the safe fallback.
6. Keep Identity Homes isolated by default. Shared Work Home is experimental and visibly marked.
7. Never store vault keys beside encrypted records or downgrade to plaintext; preserve protected state and recovery when secure storage is unavailable.
8. Keep the dashboard read-only with respect to remote identities. Local profile and preference management is allowed.
9. Bind to loopback only in the MVP, but authenticate and authorize the browser boundary as a network boundary.
10. Automatically starting the on-demand companion does not authorize background collection or OS-login enrollment; obtain their separate consent. Shell/PATH setup, telemetry, and experimental features also require their own explicit user action.

## 3. Delivery architecture

The application is a modular monolith: one repository, Go module, executable, release version, and service state owner. Domain modules expose narrow application interfaces; infrastructure adapters remain replaceable.

### 3.1 Domain modules

| Module | Owns | Must not own |
|---|---|---|
| `profile` | Identity Profiles, Login Identity references, Identity Homes, selection, lifecycle | Codex authentication implementation |
| `launch` | launch plans, capability gates, process leases, foreground lifecycle | terminal emulation or Codex execution |
| `usage` | normalized observations, provenance, freshness, capacity ranking | raw provider response archives |
| `activity` | managed launches, observed sessions, project identities, correlation | claims of causation not supported by evidence |
| `continuation` | checkpoints, consent, review, Safe and Exact workflows | repository-local state or implicit transcript transfer |
| `sharedhome` | experimental shared-home transaction and recovery | stable profile switching |
| `configpack` | immutable shared packs, local overrides, reviewed promotion | silent reverse synchronization |
| `alerts` | bounded operational alerts and notification state | general-purpose rule engine |
| `settings` | retention, diagnostics, service, display, telemetry consent | credentials |

### 3.2 Adapters

- Installed Codex discovery, version and capability detection, App Server transport, and local metadata readers.
- SQLite migrations, transactions, backups, retention, and exports.
- Windows DPAPI, macOS Keychain, supported Linux secure storage, explicit passphrase mode, and the M5A Windows-user-backed WSL integration through the existing vault boundary (ADR 0035); qualification awaits native proof.
- Platform paths, process supervision, user-service installation, notifications, browser launch, clock, and filesystem.
- Optional CodexFolio-controlled update and telemetry endpoints.

### 3.3 Process contract

The service is the only owner of SQLite migrations, vault generations, snapshots, and background scheduling. For a managed launch:

1. The CLI requests a Launch Plan and lease from the service.
2. The service resolves the target profile, home, working directory, configuration pack, compatibility gates, and safe environment delta.
3. The CLI starts the installed Codex executable in the foreground with direct standard I/O and signal forwarding.
4. The running process retains that immutable Launch Profile even if Dashboard Scope or Selected Profile changes.
5. The CLI reports lifecycle facts back to the service and offers a handoff after exit when a quota condition was observed.

The SPA communicates only through a generated TypeScript client from the versioned OpenAPI contract. It does not access Codex, SQLite, or vault APIs directly.

## 4. Risk-ordered milestone sequence

Phase 0 is planning and repository initiation. Milestones 1–7 are vertical, independently demonstrable increments. Stable companion behavior must not depend on Milestone 6 experiments.

### Phase 0 — approved baseline and repository scaffold

Goal: establish the project contract before implementation begins.

Deliverables:

- approval of this plan and its acceptance criteria;
- greenfield `codex-folio` repository, without prototype history;
- Apache-2.0 license, DCO, contribution and security policies;
- root `AGENTS.md`, architecture index, ADR workflow, and dependency policy;
- Go workspace and `web/` package with reproducible tool versions;
- CI skeleton for Linux, Windows, and macOS target builds;
- semantic versioning, schema/API version policy, and stable error-code registry;
- build metadata, license inventory, SBOM/provenance placeholders, and release scripts.

Exit gate: clean checkout can run format, lint, unit-test, frontend type-check/test/build, OpenAPI drift check, and all three target builds without application secrets.

### Milestone 1 — secure local foundation

Goal: prove the hardest local security and state assumptions before product features accumulate.

Deliverables:

- platform-native paths with an explicit absolute-path override;
- single user-scoped service and single-writer lock;
- versioned SQLite migrations, integrity checks, three rotating backups, and explicit recovery;
- field-level envelope encryption with platform vault adapters and no plaintext fallback;
- authenticated loopback bootstrap, short-lived browser session, Host/Origin/CSRF enforcement, and no permissive CORS;
- bounded structured diagnostics containing component/error codes rather than Codex or user content;
- generated Go/OpenAPI/TypeScript boundary and embedded placeholder SPA;
- clock, process, filesystem, vault, and store test doubles.

Initial storage areas:

- profiles, homes, profile aliases, and selected profile;
- encrypted project identities and sensitive checkpoint fields;
- usage observations, normalized metrics, freshness, and provenance;
- managed launches, observed sessions, and correlation evidence;
- checkpoints, alerts, settings, configuration packs, service leases, and experimental transactions;
- migration ledger, local diagnostic aggregates, and retention state.

Credentials and raw provider payloads are explicitly absent from the schema.

Exit gate: threat-model tests demonstrate that an unbootstrapped browser, hostile Origin, forged CSRF request, second writer, missing vault, and corrupt migration all fail closed with recoverable diagnostics.

### Milestone 2 — profiles, onboarding, authentication delegation, and launch

Goal: deliver the dependable CLI-only account-switching workflow.

Deliverables:

- resumable setup with Isolated Identity Homes as the recommended default;
- installed Codex discovery, explicit executable override, version/capability report, and validation without taking ownership of installation;
- Managed and Referenced Identity Homes;
- profiles with stable IDs, unique CLI aliases, optional display metadata, and Pending/Ready/Needs Reauthentication/Unavailable states;
- browser-first Codex-owned authentication with device-code fallback and explicit overrides;
- bare `codex-folio` interactive selector and deterministic `codex-folio launch <profile> -- ...`;
- shared Selected Profile contract for interactive CLI and dashboard, while deterministic launches do not mutate it;
- immutable Launch Profile record and running-process warning when dashboard scope changes;
- profile edit, removal, typed alias confirmation, ownership-aware seven-day quarantine, restore, and purge;
- immutable Shared Configuration Pack baseline with profile-local overrides and reviewed promotion;
- generated shell integration for manual user sourcing only.

Exit gate: on each Tier 1 platform, two authenticated profiles can be launched repeatedly without routine reauthentication, an existing Codex home can be referenced without being claimed, and process exit/signal behavior matches direct Codex invocation.

### Milestone 3 — supported collection and honest analytics core

Goal: collect enough safe evidence to answer current capacity and continuation eligibility.

Deliverables:

- narrow Codex/App Server adapter for documented account, auth-status, and usage capabilities;
- allowlisted local metadata readers that exclude transcript content;
- normalized metric registry with units, source, identity scope, workspace scope, collection time, window boundaries, and freshness;
- labels enforced in domain and UI contracts:
  - Provider reported;
  - Locally derived;
  - Estimated;
  - Observed during session;
- current Dashboard Scope plus explicit Combined Scope without double counting shared Login Identities or overlapping windows;
- privacy-preserving Project Identity using encrypted canonical path material and user-controlled aliases;
- Managed Launch and Observed Session distinction with explicit correlation confidence;
- capacity compatibility gates before ranking, and a recent-provider-evidence requirement before saying “best”;
- on-demand snapshots at dashboard open, manual refresh, pre-launch, and post-exit;
- 13-month default retention, configurable from 30 days to unlimited, with aggregation and explicit purge;
- JSON and CSV exports containing normalized data and provenance only.

Exit gate: fixture-driven contract tests cover supported, missing, stale, partial, contradictory, and future-unknown fields without storing unrecognized data or displaying misleading zeroes.

### Milestone 4 — Safe Continuation and recovery

Goal: guarantee that a user can start a fresh Codex session under another eligible profile with repository-first context after the source process stops.

Deliverables:

- explicit `codex-folio handoff <target>` plus automatic post-exit offer after an observed quota condition;
- hard compatibility gates before capacity ranking;
- deterministic repository-state inventory from working tree, branch, status, configured project commands, and user-authored checkpoint fields;
- no automatic execution of project tests or arbitrary repository commands during checkpoint capture;
- consent and review flow in the dashboard or `$EDITOR` for headless use;
- editable, sanitized checkpoint containing goal, completed work, pending work, known validation, risks, and suggested next action;
- fresh target Codex process in the same working directory using the target Identity Home;
- short-lived transcript-assisted recovery only after explicit consent, preview, editing, and approval; only the sanitized checkpoint is persisted or sent;
- encrypted `.cfolio` continuation export separate from analytics and portable configuration exports;
- checkpoint expiry, purge, and recovery tests;
- no repository-local CodexFolio artifacts.

Exit gate: a source launch can stop because of quota, the user can review and approve a checkpoint, and a new process under a different authenticated profile can continue from the same repository. The product language never represents this as live switching or exact thread ownership transfer.

### Milestone 5 — dashboard, background service, and operational companion

Goal: deliver the glance-first responsive product surface and opt-in periodic operation.

Frontend workflow mandated for this milestone:

1. Use `impeccable` to maintain the comp-first acceptance direction and design-system constraints.
2. Use `image-to-code` and `build-web-apps:frontend-app-builder` to create full-route, full-state reference comps and implement the accepted composition.
3. Use `build-web-apps:react-best-practices` during React implementation and review.
4. Use `build-web-apps:frontend-testing-debugging` for browser acceptance, responsive testing, and visual regression diagnosis.

The existing Signal Rail overview is compositional option one, not permission to harden a generic card grid. Before production UI components stabilize, create and approve wide and narrow references for Overview, Profiles/onboarding, Sessions/detail, Analytics/compare, Alerts/Settings, Handoff, and empty/stale/reauthentication/offline/recovery states.

Deliverables:

- six destinations: Overview, Profiles, Sessions, Analytics, Alerts, Settings;
- active scope limits first, eligible alternatives second, trends and detail after progressive disclosure;
- Analytics tabs: Capacity, Tokens, Projects, Models, Activity, Compare;
- one filterable timeline with record type, correlation, and provenance labels;
- persistent stale/partial layouts showing last-known evidence rather than collapsing or showing zero;
- wide labeled rail, medium icon rail, and narrow top profile/status plus bottom primary navigation;
- no horizontal page scrolling for essential actions or dense tables; accessible row drill-downs instead;
- full system-default dark/light themes, forced/high-contrast resilience, reduced motion, 200% zoom, and one accessible density;
- semantic charts with table equivalents, visible focus, full keyboard operation, and screen-reader status announcements;
- externalized English strings and operating-system locale for dates, numbers, time zones, and week boundaries;
- opt-in native user service with five-minute active and thirty-minute idle defaults, reset-boundary snapshots, provider-safe minimum intervals, backoff, and jitter;
- small operational alert set for capacity/reset, reauthentication, stale data, collection failure, and compatibility changes;
- update notification without in-place self-update;
- local diagnostics controls and explicit diagnostic export;
- optional privacy-preserving telemetry client kept non-blocking and disabled until the user opts in.

Exit gate: WCAG 2.2 AA automated and manual checks pass for core journeys; browser acceptance passes at supported wide/narrow viewports, dark/light/high contrast, keyboard-only, reduced motion, and 200% zoom; service install/uninstall is explicit and reversible.

### Milestone 5A — effortless everyday Codex companion

Approved parent: #82. Contract reconciliation: #83 and ADRs 0035–0037. This milestone follows M5 without renumbering M6/M7; the parent is not an executable ticket. Contracts and their required ticket review precede dependent behavior implementation.

Deliverables through bounded executable tickets:

- plain startup guides one-time setup, starts/reuses one state owner, highlights eligible Selected Profile, and launches installed Codex in the foreground with preserved streams, input, arguments, working directory, signals, and exit status; the picker retains the usable service through launch;
- companion availability after child exit, explicit/deferred stop that never kills Codex, and separate remembered background-collection choice and optional OS-login enrollment; refusal preserves launch/on-demand refresh;
- qualified prompt-free Windows DPAPI, macOS Keychain, supported Linux secure storage, and Windows-user-backed WSL2 integration, with guided prerequisites/recovery, explicit passphrase alternative, recoverable migration, and no plaintext/colocated-key or silent replacement-key downgrade;
- Codex-owned onboarding with existing-home registration, distinct managed/referenced ownership, immediate terminal instructions, automatically detected completion, validated readiness, and resumable Pending Profiles;
- explicitly trusted, revocable browsers across ordinary restarts, transparent short-session renewal, repeatable dashboard reopening, and unchanged loopback/bootstrap/Host/Origin/CSRF and secret-custody protections;
- consented source-preserving history import, explicit Unassigned History separate from selectable identities and Combined Identity View, reversible single/bulk assignment, durable provenance, deduplication, correct aggregates, and honest metric coverage under existing privacy/retention/purge/export rules;
- user-approved one-time PATH setup, direct binary support, preserved advanced commands, printed dashboard address without ordinary-launch browser opening, and safe foreground launch despite optional analytics failures;
- complete automated CLI/service/browser journeys plus storage failure injection, accessible changed flows, and usage documentation updated only as behavior ships.

Exit gate: satisfy EC-01–EC-08 in `ACCEPTANCE-CRITERIA.md`, all child tickets, required canonical verification, and independent milestone audit. Mandatory candidate-bound native evidence uses installed Codex and two real authenticated identities on Windows AMD64, macOS ARM64, Linux AMD64 with supported secure storage, and WSL2 with actual Windows-user-backed protection. Cover setup/addition and completion detection, repeated switching/foreground launch, trusted dashboard reopening, post-child companion availability, and repetition after environment restart without routine application passphrase or separate service/unlock commands. Preserve sanitized environment/prerequisite/action/result/limitation records. Fake adapters, compilation, a foreground-only WSL helper, explicit passphrase mode, or inherited prior-milestone exceptions do not establish this core promise. Missing native/live evidence leaves M5A incomplete. Acceptance of these contracts is not implementation or qualification evidence.

### Milestone 6 — isolated experimental seams

Goal: add high-value experiments without allowing them to destabilize ordinary launch, analytics, or Safe Continuation.

Deliverables:

- release-signed bundled compatibility manifest with exact supported Codex versions and per-capability gates;
- Exact Continuation invoked only by explicit `handoff --exact`, never inherited from profile settings or `--yes`;
- both relevant Codex processes stopped before an exact attempt;
- staged backup, destination preparation, App Server validation, atomic commit, rollback, and automatic Safe Continuation fallback;
- explicit cross-login/workspace authorization attestation and managed-restriction checks;
- Shared Work Home mode with clear experimental labeling, Codex-owned mutable configuration, versioned optional pack baselines, vault generation tracking, and transaction recovery;
- disposable-identity manual test protocol and compatibility evidence ledger;
- experiments removable or disableable by capability without schema corruption.

Exit gate: every injected failure point restores pre-operation state, preserves credentials, records a content-free failure code, and offers Safe Continuation. Experimental failures do not block stable-core release candidates.

### Milestone 7 — production hardening and signed stable release

Goal: meet the agreed production-standard MVP definition across Tier 1 platforms.

Deliverables:

- layered unit, property, contract, migration, integration, end-to-end, security, accessibility, visual, performance, and recovery test suites;
- compatibility fixtures for current and previous two documented Codex versions where available;
- Windows AMD64 ZIP plus installer script, Linux AMD64 tarball plus installer script, and macOS ARM64 tarball plus installer script;
- user-session service integrations and uninstall/restore verification;
- checksums, Sigstore signatures, attestations, SBOM, provenance, Windows Authenticode, and macOS Developer ID/notarization;
- update manifest and notification path;
- clean-machine install, upgrade, downgrade-rejection, backup restoration, and uninstall tests;
- release-candidate soak covering quota-boundary snapshots and background scheduling;
- release documentation, privacy notice, threat model, security reporting process, support matrix, and known limitations;
- formal name/trademark clearance before stable publication.

Exit gate: all acceptance criteria in `ACCEPTANCE-CRITERIA.md` are evidenced, no unresolved release-blocking security or accessibility defect remains, stable capabilities work on every Tier 1 target, and experiments can be disabled independently.

## 5. Critical path and parallelizable work

```text
Phase 0
   |
M1 Secure foundation
   |
M2 Profiles + launch
   |
M3 Collection + analytics domain
   |------------------\
   |                   \
M4 Safe Continuation   M5 UI/service operational surface
   |                   /
   |------------------/
   |
M5A Everyday companion
   |
M7 Production hardening

M6 Experiments branches after M2/M4 foundations and rejoins only through
its own stricter release gate; it is not a stable-core dependency.
```

Within a milestone, platform vault/service adapters, fixture construction, frontend comps, and documentation can proceed independently once their shared contracts are frozen. One implementation owner must retain control of migrations, OpenAPI generation, and shared domain contracts.

## 6. Quality strategy

### 6.1 Test layers

- Unit and property tests for domain invariants, ranking, windows, deduplication, retention, and state machines.
- Golden/fixture contract tests for Codex versions and source-shape changes.
- Migration tests from every released schema and failure-injection recovery tests.
- Integration tests across service, vault, SQLite, App Server fake, and CLI process lifecycle.
- End-to-end journeys for setup, reauthentication, launch, profile switch, handoff, quarantine/restore, purge, and service opt-in.
- Security tests for loopback authorization, CSRF, Origin/Host validation, vault absence, path traversal, symlinks, environment leakage, archive import, and malicious local metadata.
- Accessibility tests combining automation with keyboard, screen-reader, zoom, contrast, table-equivalent, status-announcement, and reduced-motion checks.
- Cross-platform tests for paths, signals, process trees, permissions, user services, notifications, browser opening, and archive installers.
- Performance regression tests against the budgets below.

### 6.2 Regression budgets

- Managed warm launch overhead: less than 300 ms.
- Cached dashboard shell and last-known evidence visible: less than 1 second on reference hardware.
- Normal provider refresh: less than 10 seconds excluding user authentication.
- Idle service: less than 75 MB resident memory and less than 1% sustained CPU.
- Bounded SQLite writes and responsive 13-month default history.
- Release archive above 50 MB triggers an explicit review.

These are regression budgets, not universal marketing guarantees.

## 7. Release and migration strategy

- Development builds may be unsigned and visibly labeled.
- Preview builds validate migration, API, platform, and UI contracts.
- Release candidates require all stable-core gates and signed compatibility artifacts.
- Stable archives require native platform signing and notarization where applicable.
- No automatic credential import from prototypes, undocumented payload import, or repository-history merge.
- Prototype migration is explicit and previewable: safe aliases and nonsecret preferences may be imported; authentication is re-established through Codex.
- Portable multi-device preparation is a versioned nonsecret configuration bundle only. Credentials are never exported; a new device requires reauthentication.

## 8. Deferred beyond MVP

- Enterprise/admin connectors and organization-wide management.
- LAN binding, remote access, and synchronized multi-device state.
- Wails or another native window shell.
- Native MSI/PKG/DEB/RPM packaging and in-place self-update.
- Detaching or replacing the Codex terminal experience.
- Live no-restart identity switching or guaranteed exact cross-team thread transfer.
- General-purpose alert rule builder.
- Raw transcript analytics or automatic transcript transfer.
- Exportable credential archives.
- Multiple visual-density modes and shipped translations beyond externalized English.

## 9. Implementation start approval

Approval of this document authorizes only Phase 0 repository scaffolding and its non-production foundation. External publishing, signing purchases, telemetry deployment, package release, or remote-service mutation still require separate explicit authorization.
