# Signal Rail reference set v1 — issue #58

**Status: design accepted on 2026-09-11.** The user accepted the complete
reference set and native SVG + semantic table approach, including the recorded
qualification limits. Production dashboard work follows completion of #58.

Start with the [reference gallery](index.html), then open the responsive
[route/state document](reference.html) and [chart experiment](benchmark.html).
The gallery links every wide/narrow capture. Serve this directory locally to run
the experiment's JavaScript module; static references also open as local files.

```sh
python3 -m http.server 8058 --bind 127.0.0.1 --directory docs/design/signal-rail/v1
```

Open `http://127.0.0.1:8058/`. This is a disposable design document using synthetic
fixtures. It has no backend, browser authorization, production routes, profile
storage, or external runtime resources. Its navigation and disclosures work;
product-action links demonstrate the destination specimen without doing the
operation. Form values are specimens, not a functional onboarding editor.

## Scope and authority

- Executable [#58](https://github.com/kanakamedala-rajesh/codex-folio/issues/58),
  parent [#57](https://github.com/kanakamedala-rajesh/codex-folio/issues/57),
  roadmap [#9](https://github.com/kanakamedala-rajesh/codex-folio/issues/9).
- The [dashboard brief](../../DASHBOARD-BRIEF.md),
  [ADR-0031](../../../adr/0031-make-signal-rail-comps-the-frontend-acceptance-reference.md),
  [domain glossary](../../../../CONTEXT.md), and
  [acceptance criteria](../../../product/ACCEPTANCE-CRITERIA.md) govern meaning.
- Original Signal Rail is the accepted visual direction. This **expanded set
  was accepted on 2026-09-11**. Capacity comes first, eligible alternatives second,
  historical detail afterward. Exactly six destinations remain: Overview,
  Profiles, Sessions, Analytics, Alerts, Settings. Handoff is contextual.
- All identities, values, times, rankings, and checkpoint text are illustrative.
  Example states are independent scenarios, not one synchronized application.
  Actual eligibility gates precede ranking; recommendations require fresh
  provider-reported evidence and expire after ten minutes.
- Accepted targets are the code-native reference document, its capture
  set, this contract, and the chart decision below. `concepts/` and
  [prompts.json](prompts.json) preserve image-generation shaping inputs, **not
  literal production acceptance targets**. Known image-generation errors are
  explicitly corrected in the native references.

## Reference inventory

Stable identifiers use `SR-v1/<screen>/<wide|narrow>`. Source and raster
identities are preserved by the reviewed Git tree and resulting ticket commit. Captures use
Chromium 149.0.7827.55, 1440 × 1000 wide and 390 × 844 narrow CSS viewports;
full-page image height grows with content. Do not shrink an entire tall capture
to judge typography: view it at native width and scroll.

| Screen identifier | Required state / interaction guidance |
| --- | --- |
| `overview` | Readiness, two distinct provider windows, exact values/resets, eligible alternatives, four primary actions, subordinate trace/table |
| `profiles` | Safe inventory, alias, auth health, ownership, pack assignment, refresh age, selection; narrow disclosure rows |
| `onboarding` | Saved Pending Profile, managed home default, explicit referenced home, installed-Codex browser/device authentication, optional pack |
| `sessions` | Filters and separately labeled Managed Launch / Observed Session timeline |
| `session-detail` | Immutable Launch Profile, metadata only, correlation confidence, exact provenance labels |
| `capacity` | Six Analytics tabs, 13-month chart with gaps, identical semantic table and narrow disclosures |
| `compare` | Explicit Combined Identity View; unchanged Selected Profile; separate per-profile windows and API units |
| `alerts` | Active/history navigation, acknowledgement specimen, thresholds, generic notification default, delivery failure |
| `settings` | System/light/dark, locale, service, retention, vault, recovery, local diagnostics (`Enabled · 14 days · 50 MB`) with `Preview diagnostic export`, updates, unavailable telemetry, portability, disabled experiments |
| `handoff` | Running source blocks approval; six fields, save/cancel, separate assistance consent |
| `handoff-ready` | Stopped source, eligible target, revision-bound approval, edit/cancel and revalidation before launch |
| `empty` | No Selected Profile, no capacity/history or alternatives, explicit setup action |
| `stale` | Retained layout, original 18-minute age and failure reason, Recommended withheld |
| `reauth` | Codex-owned recovery, no credential form, excluded target and stale last-known value |
| `offline` | Provider unreachable; last-known evidence and local workflows remain visible |
| `recovery` | Preserved database, stopped writes, explicit recovery choice, non-authorizing uncertain process state |
| `unavailable` | No value versus unsupported source; no zero-filled gauge |
| `partial` | Available window retained beside missing window; no invented reset or total |
| `contradictory` | Both observations and sources retained; conflict does not become a recommendation; unknown fields excluded |
| `expired` | Browser authorization expired; local launcher recovery, existing Codex work unaffected |
| `locked` | Passphrase service locked after reboot, terminal unlock guidance, no unattended secret |
| `running` | Selected Profile differs from running Launch Profile; future launches only |
| `refresh` | In-progress status, old capture age retained, completion announcement without focus theft |
| `launch` | Prepared versus actually started; foreground terminal handoff, no browser terminal or exact-thread promise |
| `removal` | Typed alias, running-launch block, replacement selection, managed quarantine versus referenced non-ownership |
| `export` | Field/count preview, explicit paths, separate export classes, scoped purge confirmation |

Each row has a wide and narrow reference in the gallery. Full-page narrow captures place the bottom bar at the end using a capture-only
style so it cannot obscure content. The separate `overview-narrow-viewport` and
`navigation-more` images show its actual fixed placement. Additional identifiers:
`overview-medium`, `overview-light`, `settings-light`, `handoff-forced`,
`overview-zoom-reflow`, and `navigation-more`. The last is a focused keyboard
disclosure capture. The 720-CSS-pixel reflow capture is **not** a claim that
native browser zoom or assistive technology was qualified.

## Visual and interaction contract

Use an operational, calm instrument surface. Flat regions, fine rules, open
rows, and consistent 44px controls replace nested panels. Body/control type is
16px with 1.5 line height; short captions are 14px. The system UI stack is Segoe
UI / Helvetica Neue / Arial / sans-serif; no font network request is needed.
Headings scale from 29px to 44px. Numbers use tabular figures. Normal density
does not have compact/comfortable variants. Essential labels may wrap.

| Role | Dark | Light | Meaning beyond color |
| --- | --- | --- | --- |
| Background | `#101719` | `#f5f8f9` | Canvas |
| Surface | `#151f22` | `#ffffff` | Controls / selected rows |
| Text | `#edf3f3` | `#17252b` | Readable content |
| Secondary text | `#adc0c5` | `#435861` | Supporting evidence |
| Rule | `#586c73` | `#a0afb5` | Structural separation; essential controls remain bounded |
| Cyan | `#50d6e6` | `#007584` | Selected scope, primary action, solid five-hour trace |
| Lime | `#b6df73` | `#466614` | Eligible / ready, also written as text |
| Amber | `#f1bb5c` | `#845300` | Warning / stale, also written as text |
| Magenta | `#ed91c7` | `#99326e` | Context / dashed weekly trace, explicitly labeled |

Measured foreground/background contrast: dark primary 16.15:1, secondary 9.60:1,
signals 8.18–11.90:1; light primary 14.73:1, secondary 7.01:1, signals
5.07–6.48:1. These token measurements do not establish full WCAG conformance.
Decoration may be subtle; essential boundaries and focus must meet 3:1.

- **Responsive navigation:** ≥1100px labeled rail; 700–1099px icon rail with
  accessible names, hover titles, and retained profile/status bar; <700px
  profile/status bar plus Overview/Profiles/Alerts/More bottom navigation.
  More is a native keyboard-operable disclosure containing Sessions, Analytics,
  Settings. The proposed production implementation must show equivalent focus
  tooltips for medium icons, identify current destination, and avoid covering
  focused content with the bottom bar.
- **Narrow content:** capacity and actions precede alternatives, actionable
  alerts, then optional history. Dense tables become named disclosure rows with
  labeled values. Chart-equivalent records remain available without horizontal
  page scrolling. Long handoff fields stack vertically; controls keep their
  labels and cancel path.
- **Theme:** System is the default and follows preference changes; explicit
  light/dark are complete translations. Forced colors use Canvas, CanvasText,
  LinkText, ButtonText and Highlight; no essential meaning is a fill alone.
  High-contrast preference strengthens rules. Solid/dashed traces, labels,
  availability text, table headers and values survive monochrome rendering.
- **Keyboard/focus:** logical DOM order, a skip link, named controls, native
  Enter/Space disclosures, 3px visible focus with 4px offset, and no positive
  tabindex. A production modal, if required by a sensitive operation, must
  contain focus, support cancellation, then restore focus to its trigger.
  Navigation to a new production route moves focus to its heading; refresh and
  ordinary status changes do not. The static document's anchor navigation does
  not stand in for those production semantics.
- **Status:** use polite, atomic announcements for refresh completion and
  changed sample values. Announce blocking authentication/recovery once, and
  keep a visible explanation and recovery action. Do not announce every plotted
  point, animate gauges as if new evidence arrived, or silently clear old data.
- **Motion:** the references and benchmark have no chart animation. Production
  feedback may use a restrained ≤120ms color/opacity transition. Reduced motion
  removes animation, smooth scroll, animated gauge counting, and moving traces.
- **200% zoom:** keep the same information order and named actions, wrap labels,
  stack fields and disclosure rows, and reserve space for fixed navigation.
  Required later manual acceptance: browser zoom at 200% on a 1440px window,
  then complete profile selection, alert acknowledgement, and handoff review
  without essential horizontal scrolling or obscured keyboard focus.

## Truthful evidence, text, and locale

Use exactly **Provider reported**, **Locally derived**, **Estimated**, and
**Observed during session**, with text equivalents, not a provenance color key
alone. Provider-window percentages are neither elapsed hours nor one combined
quota. Missing, unsupported, stale, partial, contradictory and reauthentication
states remain distinct from a real 0%. Preserve original capture age after
failure. Hard eligibility gates precede capacity ranking; no model/task/tool
suitability is promised. API credit/spend has separate currency units. Canonical
project paths require an explicit disclosure/export choice.

Externalize English strings in the production frontend (including accessible
names, statuses, errors, plural forms, table headers and provenance labels).
Use message placeholders rather than concatenating translated sentence
fragments. These English static reference documents and the disposable
experiment are design content, not a production localization implementation.
Use `Intl.NumberFormat` / `Intl.DateTimeFormat` with the OS locale by default;
persist UTC instants and show the local timezone. Calendar week starts come from
locale, not hardcoded Sunday/Monday. Locale changes must not reinterpret provider
reset boundaries, deduplication keys, or stored historical bucket timezones.

Recorded formatting examples for the same instant/value are in benchmark
results: US `Aug 7, 2025, 11:30 PM`, UK `7 Aug 2025, 23:30`, German
`07.08.2025, 23:30` / `55 %`, Hindi `7 अग॰ 2025, 11:30 pm`. The experiment uses
an explicitly labeled UTC bucket/display zone to keep comparisons reproducible.
Production defaults to the actual OS zone. Include long labels, locale date
order, localized numbers, plural counts, different week starts and text growth
in later browser acceptance; no shipped translation set is approved here.

## Accepted chart decision

**Use native SVG with a shared semantic table. Add no chart package.** The user
accepted this choice on 2026-09-11. The library-independent contract below
remains binding if a later measured requirement justifies changing the renderer.

| Evaluated option | Result / tradeoff |
| --- | --- |
| Native HTML text, table and meter | Sufficient for exact current capacity and accessible comparison. A table alone does not provide a historical trace; retained as the primary nonvisual equivalent. |
| Existing dependencies | React/React DOM and the current build tools contain no installed chart layer. No dependency added for the experiment. |
| Native SVG + table | Responsive vector geometry, CSS/system-color treatment, solid/dashed encoding; passed the bounded history/interaction checks. Accepted choice. |
| Native Canvas + same table | Similar measured update time; needs pixel-ratio and redraw handling and its own color treatment. No measured benefit sufficient to choose it here. |
| Additional chart package | Not installed or benchmarked: native options already satisfy this bounded experiment. No unsupported claim about another library's speed or accessibility. |

The fixture contains 19,008 half-hour captures per profile, two metric positions
per capture, across 2025-08-01 through 2026-09-01 (exclusive), including true zero
and missing intervals. Default presentation explicitly selects the final sample
of each UTC day (396 rows); the raw history remains available. Daily selection
does not sum quotas or look backward to conceal a missing final sample. Chart,
table and sample inspection consume the same displayed records. Production may
use another explicitly labeled aggregation only when its metric semantics and
table representation agree. Do not copy this synthetic fixture formula into
the product.

The [benchmark evidence](evidence/results.json) records the host, browser,
timings, bundle bytes, locale examples, checks and screenshot identifiers.
On the recorded WSL2 Ubuntu 24.04 x64 environment (Core Ultra 7 155H, 12 logical CPUs, 8 GiB,
headless Chromium 149), ten warm full-history updates after two warmups measured
SVG median **31.90ms**, p95 **43.20ms**; Canvas median **52.65ms**, p95
**55.40ms**. Timing includes two animation-frame callbacks, so frame scheduling
and host load contribute to these results. The earlier run before the crash
had similar ~32ms medians for both renderers; its temporary evidence was lost
and is not the current recorded result. It is not a claim that SVG is faster, nor a
production refresh/startup, native-platform, or provider benchmark.

The complete disposable experiment JavaScript (both renderers, data generator,
controls and table) is **8,013 bytes raw / 2,811 bytes gzip**, with **zero new
chart dependencies**. This is not an isolated SVG-library size or production
bundle delta. The checked-in source and manifest identify the measured bytes.

### Library-independent visualization contract

1. Receive normalized, scoped records: profile/workspace identity, metric key,
   exact value or unavailable state, unit, provider window/bounds, capture
   instant, source/version/provenance, historical bucket zone and aggregation
   semantics. Never receive raw provider responses, credentials or Codex text.
2. Keep scope, window, units, source and capture age attached. Aggregate only
   compatible absolute metrics after existing deduplication. Never sum unrelated
   percentages, subscription windows and currency.
3. Drive plot, semantic table, selection summary and export preview from the
   identical filtered dataset. Explicitly label any sampling/aggregation.
   Gaps remain gaps; zero remains zero. No extrapolated future recommendation.
4. The visual plot may be hidden from assistive technology when its nearby
   named table and status provide every decision-relevant value. The table has
   a caption, column headers, units, provenance, availability, scope and zone.
   Pagination is keyboard accessible and gives access to every record.
5. Sample controls support keyboard inspection without thousands of tab stops.
   Announce selected timestamp, values, availability and provenance without
   moving focus. Table and selected sample stay synchronized through profile,
   date-range and resolution changes. Do not make hover the only way to read.
6. Resize without clipped controls or pixelated text; provide non-color series
   encoding and real forced-color treatment. Animation is optional and disabled
   by reduced-motion preference. Geometry does not own domain ranking policy.
7. Keep the renderer replaceable at the existing visualization boundary. This
   document does not request a new interface hierarchy, adapters, chart package,
   data store, analytics engine, or production component implementation.

### Reproduce the bounded experiment

Use an existing Playwright package and installed Chromium. The runner accepts
their local paths, starts an ephemeral loopback-only static server, then closes
the server/browser. It does not install dependencies or touch application state.
Use `index.mjs` from the Playwright package as its first argument.

```sh
node docs/design/signal-rail/v1/browser-check.mjs PLAYWRIGHT_ENTRY CHROMIUM_EXECUTABLE OUTPUT_DIRECTORY all
```

The recorded run used Playwright 1.57.0 from the local tool cache and Chromium
149.0.7827.55. Browser plugin was unavailable; the installed Playwright fallback
was used. The assertions exercise visible controls/table values and the public
browser accessibility tree. They distinguish a real zero from unavailable,
traverse the full history, switch scope/range/resolution/locale, compare render
cost, capture themes/viewports, and check browser errors and remote requests.
No application credentials, real Identity Homes, or external provider are used.

### Qualification limits and manual acceptance script

Keyboard interaction, semantic accessibility-tree exposure, system/light/dark
rendering, forced-color emulation, reduced-motion emulation, responsive layout,
record synchronization, and absence of external requests were exercised. The
accessibility tree contains the named slider, status, table and column headers;
its recorded artifact is [accessibility-tree.json](evidence/accessibility-tree.json).
This is **not spoken screen-reader evidence** or a WCAG 2.2 AA certification.
No screen reader is installed in this environment. Actual browser 200% zoom,
screen-reader speech, native OS contrast, and Firefox/Safari remain unqualified.

Before production acceptance, run NVDA/Firefox or VoiceOver/Safari (record exact
versions), choose a profile/range in the experiment, navigate its named table,
inspect a sample with arrows/Home/End, move between pages, and verify that the
timestamp, zero/unavailable state, unit and provenance are spoken once without
focus theft. Repeat after changing the profile and daily/raw resolution. Record
the spoken result, not only the accessibility tree. Test 200% browser zoom and
the core layout/actions as described above. These limitations must stay visible
when accepting the chart approach; they do not count as completed M5 browser or
native-platform qualification.

## Fidelity and shaping ledger

Used impeccable for the operational hierarchy and accessibility guidance;
image-to-code plus frontend-app-builder for fresh primary-route/narrow concepts
and native translation; frontend-testing-debugging for rendered checks.
Consulted gpt-taste; its cinematic AIDA, random layout, remote imagery and GSAP
requirements conflict with the accepted calm, offline Signal Rail contract and
were not adopted. No competing product/design system was created: the existing
product record is `docs/product/PRODUCT.md`.

| Comparison | Concept evidence | Reference decision / correction |
| --- | --- | --- |
| Capacity hierarchy | Original and `concepts/overview.png` | Large paired scoped instruments and adjacent eligible alternatives retained; four actions belong to scoped capacity. |
| Container model | Original rail and ruled instrument regions | Flat rules/open rows, no nested card grid, one consistent density. |
| Typography and controls | UI sans, large values, compact evidence | Native 16px body/controls, 44px targets, visible focus; no unreadable tiny telemetry-only labels. |
| Palette | Charcoal/cyan with amber/lime/magenta signals | Explicit light translation and forced-color mode; secondary text contrast increased. |
| Responsive structure | `concepts/overview-narrow.png` | Profile/status retained; bottom Overview/Profiles/Alerts/More; actions precede alternatives, alerts precede history. |
| Onboarding copy | `concepts/onboarding.png` | Removed invented `codex --profile` example; pack ownership remains Profiles. Pending setup cannot select/launch. |
| Analytics/Compare truth | `concepts/analytics-corrected.png`, `concepts/compare.png` | Restored all six navigation destinations and exact Analytics tabs; removed hours-used quota fiction and invented provider; separate API credit units. Plot/table values now come from the same reference samples. |
| Handoff consent | `concepts/handoff.png`, `concepts/handoff-narrow.png` | Six fields retained; assistance stays off with a review action, not a preselected consent toggle; source-running blocks approval. |
| First browser pass | Initial Overview/benchmark captures | Corrected rail selector overflow, skip-link capture artifact and action placement. Dense raw history now has an explicit daily-final view; raw remains inspectable. |
| Visual-review correction | Full-page narrow, reauthentication, benchmark captures | Capture-only footer placement preserves all content; actual fixed navigation remains separately captured. Reauthentication excludes Research from eligible alternatives. Narrow benchmark now includes focused chart/control/table captures. |

Above-the-fold copy is the native reference text, not unchecked raster OCR.
The user accepted the native corrections listed here; generated concepts are
shaping inputs rather than literal production targets.
Future production comparisons should use the accepted native captures and this
contract together; copying a raster's incorrect wording is not fidelity.

The independent visual confirmation inspected all **64/64** regenerated
captures and reported no invalid captures or direct visual regressions. It
scored V1 (unobscured narrow exports with separate fixed-navigation evidence),
V2 (Research excluded while reauthentication is required), and V3 (narrow
chart/control/table evidence) **RESOLVED**. This is bounded visual confirmation,
not user acceptance or the repository ticket-review gate.

## Repository verification

On 2026-09-11, `node scripts/verify.mjs` passed every repository gate on
`6206e5d43d4df2d23c4404a1bd24ebf9bf9654bd` with this reference directory
untracked. Tracked source and lockfiles remained unchanged. This is a
pre-approval working-tree run, not verification of a committed ticket.
The verifier covered pinned toolchains, locked dependencies, generation drift,
architecture, Go checks, native build identity, cross-build/archive dry runs,
governance, and frontend format/lint/typecheck/tests/build/offline assets.
Tier 1 target builds remain compile-only; signing and release qualification
are outside the Phase 0 verifier.

The gallery was separately checked at 1440px and 390px: all 26 entries were
present, dark Settings captures loaded, image aspect ratios were preserved,
and the page had no horizontal overflow. Final acceptance bookkeeping, removal of the redundant checksum inventory,
and the gallery image-sizing correction followed the canonical run; neither
changes the reference or benchmark behavior measured above. Run the required
candidate checks before commit and canonical verification again on the
exact clean ticket commit.

## Acceptance record

**Accepted on 2026-09-11.** Asked whether they accepted the complete `SR-v1`
set and native SVG + semantic-table approach, including the documented
verification limits, the user replied: **“looks good proceed”**.

The acceptance covers all 26 route/state specimens, their 64 captures, this
contract and the chart approach. Spoken screen-reader output and actual 200%
browser zoom remain unverified; acceptance does not convert these limits into
completed platform or WCAG qualification. Stable `SR-v1` identifiers and the
reviewed Git tree preserve the targets for later visual comparison.

The user subsequently requested removal of the redundant checksum file; Git
provides the content history. This changes no accepted reference or benchmark
behavior. Complete the repository ticket review gate, commit the reviewed
candidate, and verify that exact clean commit with `node scripts/verify.mjs`.
Production ticket #59 remains gated until #58 is actually complete. No push or
GitHub issue mutation is part of this request.
