# Milestone 5 qualification summary — issue #79

**Status: fixes verified; delivery authorized with owner-deferred qualification checks. Parent milestone remains open.** Audit date: 20 September 2026.
Application baseline: `93d0405383485d5e6c2a4f61d8fc76c9486416d9`.

This sanitized report is the only retained audit document. At the user's request,
detailed screenshots, observations, account data, CSV ledgers, JSON results,
manifests, logs and audit backup copies were removed. The aggregate statements
below summarize prior execution; they are not independently reproducible from
retained evidence. Full platform qualification still requires the missing checks. The source/test
index below can be reproduced using isolated fixtures without private accounts.

## Owner-directed delivery scope

On 20 September 2026 the owner explicitly authorized committing and wrapping up
#79, with the remaining qualification checks to be handled separately after the
PR is merged. This supersedes the earlier instruction to keep #79 open until
those checks are performed. The accepted delivery exceptions are:

- **R4 / AC3 / parent TD7:** actual browser-level 200% zoom, spoken screen-reader
  operation and complete native/manual contrast remain unverified.
- **R5 / AC4 / parent TD6:** native Windows AMD64, macOS ARM64 and Linux AMD64
  user-service enrollment/removal, retained-data, vault and notification/privacy
  qualification remain unverified. WSL, compile-only and recording-adapter checks
  do not substitute for these demonstrations.
- Detailed real-account artifacts remain deleted for privacy. This report retains
  sanitized source/test traceability and aggregate execution results only.
- #79 may be delivered and closed after the reviewed commit passes local and
  hosted verification. This does not declare the parent milestone fully qualified.
  #57 stays open, PR comments remain to be addressed, and the PR is not merged by
  this delivery. Independent milestone readiness findings remain visible there.

These are accepted scope deferrals, not passing test results. No follow-up issue
number is invented; the owner will handle the deferred work separately.

## Implementation in #79

The user explicitly authorized implementing the deferred fixes in this ticket,
overriding the earlier requirement to return them to their owning tickets. No
other issues were reopened or modified.

Implemented corrections:

- Current capacity selects the active provider window (or latest last-known
  window), preserving SQLite history and genuine same-window contradictions.
- Configuration transfer fits the 320px reflow boundary; narrow keyboard focus
  clears the fixed navigation bar.
- Alerts supports arrow/Home/End tab navigation, roving focus and panel
  relationships. Failed actions are caught and save failures retain edits.
- Isolated Capacity samples have visible markers without connecting missing data.
- Referenced-home setup exposes **Use existing sign-in**, using the existing
  noninteractive preparation path rather than starting another login.
- Overview labels use reported quota durations and omit unsupported secondary
  slots when another quota is present; unknown states remain explicit.
- Settings has direct section navigation; Analytics filters can be collapsed
  while retaining a summary of the active scope.

Verification uses isolated regression fixtures, not a new real-account audit.
Canonical verification passed (all suite, exit 0). Independent confirmation
resolved R1–R3; R4/R5 remain unqualified under the owner-accepted delivery exceptions. Historical
live-case counts below are not upgraded by these code changes.

## Scope and results

Automated tests used an authenticated local service with deterministic fake Codex
and isolated data. Separate browser checks used real accounts and installed Codex.
Fixture results must not be represented as real-account coverage. Earlier fixture
screenshots were incorrectly associated with real accounts; that attribution was
corrected before removing the images.

The cumulative real-account audit covered 127 case families: **24 passed,
74 partially exercised, 3 failed, 1 needed improvement, 3 were blocked,
18 were not executed and 4 were excluded**. These span two different service
states, not a single unchanged dataset. A partial case is not a pass.
The safe follow-up completed 16 previously unexecuted families and partially
exercised six others, reducing the unexecuted count from 40 to 18.

The repeat focused on medium and large screens. Narrow-only exclusions do not
waive the accepted milestone's accessibility or reflow requirements. Fresh-state
success does not resolve defects reproduced with historical observations.

## Findings and required follow-up

| ID | Finding / required outcome | Owner or prerequisite |
| --- | --- | --- |
| R1 — RESOLVED | Expired historical observations create false current-capacity conflicts and suppress recommendations. Use applicable current evidence while preserving genuine conflicts and provenance. | Collection/ranking #39/#42; displayed through #59/#65. |
| R2 — RESOLVED | Settings measured 345px wide in a 320px viewport. Essential import controls must fit or wrap. | #78; historical narrow finding, excluded from the desktop repeat. |
| R3 — RESOLVED | Fixed mobile navigation completely obscures keyboard-focused profile editing. Keep focused controls visible. | #59/#60; historical narrow finding. |
| R4 — OWNER-DEFERRED | Actual 200% browser zoom, spoken screen reader and full native/manual contrast were not established. | Suitable environment and actual results, or requirement-specific accepted exceptions. |
| R5 — OWNER-DEFERRED | Native enrollment/removal, retained-data, vault and notification/privacy matrix remains incomplete. | Native Windows AMD64, macOS ARM64 and Linux AMD64; WSL and recording adapters do not substitute. |

Additional observations addressed by the implementation above:

- Alerts tab semantics and arrow-key behavior are incomplete; offline save also
  produced an unhandled rejection despite recovery guidance.
- Referenced-home setup entered a login path despite existing authentication,
  with unhelpful pending/cancellation behavior.
- A one-sample Capacity chart had no visible point despite a populated table.
- Provider primary/secondary terminology obscures actual quota-window meaning.
- Long Settings pages and narrow Analytics filters could be easier to navigate.

Remaining live checks depend on service restart/recovery setup, clean or imported
profile state, reauthentication, profile lifecycle/removal operations, explicit
transcript/network consent, configuration import application, or an approved
checkpoint and export consent. These are not silently marked passed.

## Verification and acceptance limits

The final product-fix candidate passed `node scripts/verify.mjs` (all suite,
exit 0), including Go checks, contracts, builds/archives, frontend checks,
browser smoke/deep journeys, startup benchmark and tracked-source immutability.
The 21-file dirty candidate identity was
`a143ae048ad5ed87ef6dec66ac2231db108552169770b785b1e57dad5cf34991`:
SHA-256 of sorted compact JSON mapping each modified/untracked candidate path
to its file SHA-256. Final report bookkeeping follows that frozen candidate.
Detailed verification output was deleted after recording this summary.

Regression checks passed for expired-history/current-window selection, genuine
same-window conflicts, retained history, 320px Settings reflow, deliberately
obscured Edit focus followed by keyboard Tab, Alerts keyboard/panel semantics,
rejected saves retaining edits without unhandled errors, isolated chart points,
existing-sign-in preparation, reported quota labels, Settings section focus,
and collapsed filters preserving project/window scope. Rendered fixture
Overview and Analytics were visually inspected. This is not live-account retest
or native desktop qualification.

The isolated fake-Codex launch overhead measured 50.2ms and cached startup median
139.1ms in this run. Typechecking and quota-duration boundary checks also passed.
An earlier attempt exposed a test helper's missing response return, an obsolete
label assertion, and a locator that stopped matching when its disclosure closed;
those harness defects were corrected before the final passing run.

Performance measurements met the recorded budgets under WSL/fixture conditions;
native interactive performance and lifecycle qualification were not established.
Automated accessibility samples and reflow tests do not prove spoken-reader
behavior, native zoom or complete WCAG conformance.

Round-1 independent confirmation resolved R1–R3 and found no direct remediation
regression. R4/R5 remain unverified and are accepted delivery deferrals. The bounded UI review found an incomplete
collapsed-filter scope summary; its project fallback and window label were
corrected; bounded confirmation marks F1 RESOLVED and the regression assertions
pass. No further implementation blocker was found in that bounded review.
Removing detailed evidence limits
acceptance traceability; it does not resolve any remaining qualification gap.

| #79 acceptance area | Current disposition |
| --- | --- |
| AC1 — requirement traceability | Sanitized US/ID/TD and referenced-acceptance index reconstructed below; private detailed evidence remains deleted. |
| AC2 — composed journeys | Automated regressions pass; historical live-case variants have not been rerun. |
| AC3 — UI/accessibility | Code regressions pass; remaining manual/platform checks accepted as R4 deferral. |
| AC4 — native lifecycle | Unqualified; accepted R5 delivery deferral. |
| AC5 — engineering budgets | Historical WSL/fixture measurements only; detailed evidence removed. |
| AC6 — revision-bound verification | Final dirty-candidate all-suite pass; no clean #79 commit or hosted run. |
| AC7 — qualification boundaries | Production continuation exception retained; telemetry disabled/unavailable. No deployment, signing, release or later-milestone claims. |
| AC8 — milestone completion | #79 closure authorized after delivery checks; parent completion remains deferred pending PR review and outstanding qualification work. |

Both usage guides contain a from-scratch setup path. Validation test additions
remain in source. This pre-delivery report does not claim 100% functionality or full milestone
qualification. Exact commit and hosted results will be recorded on #79 and #57. Product fixes are included in #79 under the user’s explicit scope override;
the owner-accepted delivery exceptions above do not certify the omitted checks.

## Sanitized requirement traceability

This index covers all **70 user stories, 25 implementation decisions and 12
testing decisions** in #57. It was reconstructed from the accepted parent and
child specifications and existing source assertions. Entries identify executable
checks; their mere existence is not a passing result. The canonical run above
supplies aggregate execution status. Native/manual exceptions remain explicit.
All #58–#78 children are closed; their states are coordination metadata, not proof.

Browser abbreviations: **B** = `web/scripts/browser-test.mjs`, **A** =
`web/scripts/analytics-browser-test.mjs`, **S** = `web/scripts/sessions-browser-test.mjs`.
These execute against the authenticated service through
`cmd/codex-folio/dashboard_browser_test.go::TestOverviewBrowser`.
**Design** = the accepted SR-v1 contract in `docs/design/signal-rail/v1/README.md`,
`references.json` and `browser-check.mjs`; these use illustrative data.

| Parent user stories | Child tickets | Concrete automated assertions / boundary |
| --- | --- | --- |
| US1–6 | #59, #61, #68 | B capacity, alternatives, refresh and launch/handoff; `internal/usage/ranking_test.go` unique fresh provider capacity and eligibility gates. |
| US7–10 | #59, #61 | B persisted single selection, explicit Combined scope, unchanged running Launch Profile. |
| US11–13 | #59, #65, #66 | B adverse provider states; `internal/usage/usage_test.go` partial/stale/contradictory and failed-refresh preservation; `internal/store/usage_test.go` current-window/history regression. |
| US14–16 | #59, #70 | B six destinations, offline assets, expiry/relaunch; `internal/httpapi/server_test.go` restrictive offline headers and session invalidation. |
| US17–21 | #60, #61 | B resumable setup/referenced home/device flow/edit/reauthentication; `cmd/codex-folio/profile_test.go` committed-stage resume; `launch_test.go` Pending rejection. |
| US22–24 | #60, #62 | B typed confirmation, running/selection protection, quarantine/restore/purge and referenced non-ownership; `internal/platform/profile_quarantine_test.go`. |
| US25 | #63 | B pack review/application; `internal/store/configuration_packs_test.go` immutable versions/overrides; CLI launch tests for projection before start. |
| US26 | #61 | B launch preparation/Back; `cmd/codex-folio/launch_test.go` foreground streams, exit and service-owned lifecycle. |
| US27–28 | #64 | S filters, timeline/detail, metadata and pagination; `internal/store/activity_test.go` distinct sessions/launches and conflicting correlation. |
| US29–30 | #65, #66 | A six tabs, scoped chart/table agreement; `internal/usage/usage_test.go` compatible aggregation and shared-evidence deduplication. |
| US31–34 | #64, #66, #67, #69, #78 | A exact cached export previews; `internal/store/export_test.go`, `purge_test.go`, `secure_test.go` path disclosure, purge isolation and safe project projection. |
| US35–38 | #68 | B checkpoint review/redaction/approval/cancel; `internal/httpapi/continuation_test.go` stale approval; `cmd/codex-folio/handoff_test.go` fresh target in source repository. Production continuation exception retained. |
| US39–40 | #69 | B assistance consent/recovery/export/purge; `internal/httpapi/continuation_test.go` per-handoff transcript boundary and metadata-only checkpoint inventory. |
| US41–46 | #58, #59, #64–66, #79 | B keyboard/themes/reduced motion/accessibility samples; A semantic tables and isolated points; `narrow-browser-checks.mjs` reflow/focus; Design. R4 remains unverified. |
| US47–49 | #59, #71, #72 | `cmd/codex-folio/service_lifecycle_test.go` enrollment-gated scheduling; `service_enrollment_test.go` explicit install/uninstall without state changes. Actual native evidence deferred under R5. |
| US50–51 | #72 | `internal/usage/scheduler_test.go` active/idle/reset boundaries, bounded backoff/jitter, overlapping-trigger coalescing. |
| US52–53 | #70–72 | `cmd/codex-folio/service_lifecycle_test.go` locked-state suppression; `internal/platform/passphrase_test.go` locked restart; `owner_test.go` second-writer rejection. Native reboot/session qualification separate. |
| US54–56 | #73, #74 | `internal/alerts/alerts_test.go` capacity/operational/credit-expiry conditions; `internal/store/alerts_test.go` deduplication, acknowledgement, resolution and reopening. |
| US57–58 | #71, #74, #76–78 | `internal/alerts/service_test.go` generic versus consented detail, bounded delivery failures; B independent controls. Actual native notification/privacy remains R5. |
| US59–60 | #76 | B independent explicit/automatic update consent; `internal/updates/service_test.go` no-network defaults and safe failures; `internal/adapters/updates/source_test.go` response validation. |
| US61–62 | #75 | B diagnostic settings and preview/download; `internal/diagnostics/service_test.go` exact versioned bundle and forbidden-content exclusions. |
| US63–64 | #77 | `internal/telemetry/service_test.go` prerequisites/default-off/enable/revoke/reset; `schema_test.go` allowlist. Production backend remains disabled/unqualified. |
| US65–67 | #78 | B import/export/conflicts; `internal/store/configuration_bundle_test.go` Pending-only imports, conflict resolution and stale previews; `internal/configbundle/configbundle_test.go` strict bounded decoding. |
| US68–70 | #58, #59, #71, #74, #79 | Design contract, real-service browser harness and explicit platform boundaries. Full milestone completion remains separate from child delivery. |

| Implementation decisions | Child tickets | Assertion mapping / limit |
| --- | --- | --- |
| ID1 | #59 | Real-service harness and sole-writer platform tests; existing service/store composition. |
| ID2–3 | #58, #79 | Accepted Design reference/workflow contract. Approval history is documentary, not retroactively proved by tests. |
| ID4 | #58, #59, #64–66, #73 | B navigation, S timeline and A tabs/tables. |
| ID5 | #59–63, #67–70, #78 | HTTP Host/Origin/CSRF checks and safe profile/continuation/configuration browser APIs. |
| ID6 | #58, #59, #65 | Design benchmark, embedded offline assets, B no remote resources and A chart/table agreement. |
| ID7–9 | #59–63 | US7–10 and US17–26 selection, foreground launch, onboarding, lifecycle and configuration ownership. |
| ID10–11 | #59, #64–67, #73 | US11–13 and US27–34 provenance, compatible aggregation, metadata timeline and exports. |
| ID12 | #68–69 | US35–40 continuation, assistance consent, recovery and export. |
| ID13 | #58, #59, #64–66, #79 | US41–46; manual/native R4 limits retained. |
| ID14 | #59, #61, #72 | CLI lifecycle collection before plan/after exit without changing exit facts; scheduler tests. |
| ID15–17 | #70–72 | Enrollment, scheduling, locked-vault and sole-owner tests; native R5 limits retained. |
| ID18–19 | #73–74 | Alert evaluation and notification privacy adapters; actual native delivery deferred. |
| ID20 | #76 | Update consent and validated provider responses. |
| ID21 | #75 | Diagnostic controls and safe bundle tests. |
| ID22 | #77 | Telemetry prerequisite/consent/schema tests. |
| ID23 | #78 | Portable configuration preview/conflict/Pending import tests. |
| ID24 | #59–61, #67, #70–73, #75–78 | B Settings persistence and independent consent; corresponding CLI command tests. |
| ID25 | #59, #61, #65–66, #72, #79 | `TestOverviewStartupBenchmark`, `TestEnrolledServiceIdleResourceBudgetOnLinux` and recorded budget summaries. Historical deleted raw measurements are not reconstructed as new passes. |

| Testing decisions | Evidence pointer / boundary |
| --- | --- |
| TD1–2 | B/A/S real authenticated service, temporary SQLite and deterministic Codex; observable persisted/visible/exported outcomes. |
| TD3 | US1–40 and US47–67 composed journeys. Destructive real-account variants are not implied. |
| TD4 | Adverse provider fixtures, usage/ranking/aggregation regressions and chart/table parity. |
| TD5 | Deterministic scheduler, sole-owner and recording notification tests. |
| TD6 | Actual native lifecycle matrix remains owner-deferred under R5. |
| TD7 | Design and browser evidence; native zoom/spoken reader/manual contrast remain owner-deferred under R4. |
| TD8 | HTTP authorization, redacted diagnostics, consented/encrypted continuation and safe export/import checks. |
| TD9 | Update/telemetry no-network/schema/consent tests; no production backend qualification. |
| TD10 | ID25 and budget summaries; WSL/fixture conditions stay explicit. |
| TD11 | `scripts/verify.mjs`, reviewed candidate and exact-clean-commit/hosted records upon delivery. |
| TD12 | This index and verification summaries; #57 completion remains open under the owner-directed delivery scope. |

| Referenced acceptance IDs | Requirement mapping / boundary |
| --- | --- |
| UX-01–10 | US1–16, US41–46, US68–69 and generated contracts; UX-06 manual qualification remains deferred. |
| PL-01–10 | US7–10 and US17–26 profile/launch/lifecycle/consent assertions. |
| UA-01–10 | US7–13 and US27–34 usage/activity/aggregation/export assertions. |
| CT-01–06, CT-10 | US35–40; continuation HTTP/CLI and `internal/continuation/checkpoint_test.go`. |
| SP-05–09 | Retention/purge, authorization, diagnostics and telemetry boundaries above. |
| SP-10 | Telemetry prerequisite gate only; no production retention/deletion claim. |
| OP-01–06, OP-08 | US47–60; native platform operation remains R5. |
| PB-01–05 | Foreground launch, adapter boundaries, normalized storage/export and metadata privacy. |
| SP-01–04 | Existing vault/store/platform tests for fail-closed encryption, migration integrity and recovery. Native evidence remains distinct. |
| OP-04, OP-07 | Sole-owner tests and `cmd/codex-folio/shell_test.go` shell integration invariants. |
| RG-06 | Stable core remains independent of unavailable experiments. |

Parent scope exclusions remain unchanged: no M6 implementation, live identity
switching, browser terminal/proxy, arbitrary command engine, remote account
mutation, transcript analytics, implicit consent, credential migration, backend
provisioning, self-update or M7 release/signing qualification.
