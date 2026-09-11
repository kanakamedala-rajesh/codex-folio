# Authorized Overview — issue #59

Implementation evidence for [#59](https://github.com/kanakamedala-rajesh/codex-folio/issues/59)
under [#57](https://github.com/kanakamedala-rajesh/codex-folio/issues/57).
This is the Overview slice; it does not complete the milestone.

## Reference and implementation

The accepted [SR-v1 contract](../signal-rail/v1/README.md) remains the design
authority. Styling uses Tailwind utilities exclusively; the source stylesheet
contains only Tailwind import, theme and variant directives. The following
comparisons use the native reference, not the generated shaping concepts.

| Reference                                     | Implemented comparison                                                                                                                                                                           |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `SR-v1/overview/wide`                         | [Wide](evidence/overview-wide.png): 216px labeled rail, capacity first, adjacent alternatives, four actions, subordinate trace/table.                                                            |
| `SR-v1/overview-medium`                       | [Medium](evidence/overview-medium.png): 80px icon rail, accessible labels and hover/focus text, visible profile/status bar, stacked content.                                                     |
| `SR-v1/overview/narrow`                       | [Narrow](evidence/overview-narrow.png) and [actual viewport](evidence/overview-narrow-viewport.png): paired capacity, actions before alternatives/history, bottom Overview/Profiles/Alerts/More. |
| `SR-v1/settings-light`, forced-color contract | [Light Settings](evidence/settings-light.png), [forced-color Overview](evidence/overview-forced.png): complete palettes, named controls, labels and solid/dashed series.                         |
| `SR-v1/stale/wide`, `SR-v1/running/wide`      | [Stale](evidence/stale-wide.png), [running](evidence/running-wide.png): retained original age, no stale recommendation, distinct Selected/Launch Profiles.                                       |

System font, 16px body, 44px controls, flat ruled regions, paired instruments,
semantic tables and visible focus follow the reference. Narrow records use
named timestamp disclosures with readable labeled values. Retained gauges say
Last-known remaining, and a conflict in one window does not hide another
window’s valid evidence. Navigation focuses the
new heading and closes More. The real provider projection names windows
Primary/Secondary instead of hardcoding a duration. Actual fixture values,
profile aliases, timestamps and eligibility replace illustrative comp values.
Credits are explicitly unsupported by the current source. The supporting trace
shows the last twelve raw captures per profile, with actual timestamp spacing
and no filling failure gaps; the later Analytics journey owns long history.
Later destinations explain their availability without claiming implemented
workflows. Launch/handoff guidance uses existing foreground terminal commands.

Full-page narrow captures temporarily replace the fixed navigation utility with
`static` so the capture does not obscure evidence. The separate viewport image
shows its actual fixed position. Captures contain isolated fixture data only.

## Runnable acceptance evidence

[Browser results](evidence/results.json) and [re-entry results](evidence/reentry-results.json)
record Chromium, hardware, viewport sizes and executed checks. The recorded
Tailwind build used Chromium 149.0.7827.55 and Playwright 1.57.0 on WSL2/Linux
x64 (Core Ultra 7 155H, 12 logical CPUs, approximately 8 GiB RAM). Cached evidence
became visible in **114.38 ms**, measured from navigation start through the
Current capacity heading after authenticated data loading. This is one local
engineering-budget measurement with two profiles and a short fixture history,
not a live-provider or native-platform performance qualification.

`TestOverviewBrowser` starts the real loopback HTTP service, generated browser
client, temporary SQLite store and fixture vault. Only installed-Codex collection
is substituted. It covers bootstrap removal/replay/expiry, fresh reused-service
entry, CSRF rejection, persisted single selection, explicit combined scope,
running-launch preservation, supported/stale/partial/missing/unknown-field/
contradictory/zero/offline/reauthentication states, refresh failure ages,
trace/table and keyboard sample parity, route focus, themes, 720px reflow and
absence of runtime errors or external asset requests. Axe reported zero
WCAG 2 A/AA, 2.1 AA and 2.2 AA tagged violations on the tested Overview state.

The canonical verifier installs the locked browser tooling and runs this gate
after building embedded assets. See [BUILDING](../../development/BUILDING.md)
for the focused command and optional installed-Chromium path. Backend tests also
cover command authorization/re-entry, the twelve-capture bound with raw gaps,
and chronological ordering across differing fractional timestamp precision.
The latter reproduced an intermittent older-evidence projection before the fix;
three consecutive complete browser journeys passed after correction.
Source and capture identity is the reviewed Git tree and resulting ticket commit;
no redundant checksum manifest is used.

## Qualification still required

Automation and rendered inspection do not establish full WCAG acceptance.
Spoken screen-reader announcements, actual browser 200% zoom, manual keyboard
acceptance and native OS high-contrast behavior remain unqualified. The 720px
capture is a reflow check, not actual browser zoom. Firefox/Safari, live provider,
and native Windows/macOS runtime evidence are not claimed. The prior #58 design
acceptance does not waive #59 implementation requirements.

Parent #57 Testing Decision 7 requires recorded manual accessibility evidence.
On 2026-09-11, the user explicitly accepted a #59 exception for unverified
manual keyboard testing, spoken screen-reader testing, actual 200% browser zoom,
and native high-contrast testing. Those four qualifications remain unqualified;
the exception is not WCAG or native-platform evidence and is not a blanket #57
milestone waiver.

Pre-commit canonical verification passed all repository gates for staged tree
`aa46c2f1588dd4062e38f396d4b1e2f46df63d95`, and the standards review passed.
The full ticket review reported only R1. Confirmation review and exact-commit
canonical verification remain pending. No hosted delivery is claimed.
