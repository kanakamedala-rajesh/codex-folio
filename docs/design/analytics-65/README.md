# Analytics implementation evidence — issue #65

This document records the bounded production evidence for executable issue
[#65](https://github.com/kanakamedala-rajesh/codex-folio/issues/65), under parent
specification [#57](https://github.com/kanakamedala-rajesh/codex-folio/issues/57).
The accepted visual and interaction targets are `SR-v1/capacity/{wide,narrow}`
and `SR-v1/compare/{wide,narrow}` in the
[Signal Rail reference set](../signal-rail/v1/README.md).

## Implemented contract

- Analytics has a functional Capacity surface and explicit Compare surface.
  Later Tokens, Projects, Models, and Activity slices are visibly unavailable.
- Capacity reads the generated client's authenticated history endpoint. Scope,
  range, and provider-window controls preserve stored profile, metric unit,
  source, provenance, availability, capture time, provider window bounds, and
  bucket timezone. Missing months remain gaps rather than zeroes.
- Native SVG traces and the semantic table consume the same filtered samples.
  Primary and secondary provider windows use solid and dashed traces as well as
  text labels. Narrow layouts expose the same values through native details.
- Compare is explicitly labeled Combined Identity View, leaves Selected Profile
  unchanged, and renders each profile independently. API credit/spend accepts
  only compatible credit or currency metrics; subscription percentages and
  tokens are not converted or relabeled.
- All new interface copy is centralized in the frontend copy module. Formatting
  uses the host locale and displays the resolved local timezone while stored
  bucket boundaries remain unchanged.
- No chart or other runtime dependency was added. The production bundle changed
  from 282,272 to 299,943 raw bytes and from 81,157 to 85,124 gzip bytes;
  embedded CSS changed from 28,038 to 28,533 raw bytes and from 6,320 to 6,417
  gzip bytes.

## Browser evidence

The repository browser journey runs against the real loopback HTTP service,
generated client, CSRF/session authorization, SQLite store, retention compaction,
and embedded production assets. Its deterministic provider fixture retains 13
representative months, a missing month, both provider windows, multiple profiles,
provider-reported, locally-derived, estimated, stale/last-known and contradictory
facts, overlapping windows, shared private evidence identity, and current
evidence. The private shared identity/workspace sentinels are asserted absent
from the browser projection. Existing store coverage also exercises overlapping
buckets and deduplication.

Recorded environment: WSL2 Linux x64, headless Chromium 143.0.7499.4, Intel Core
Ultra 7 155H (12 logical CPUs), 8 GiB memory. The measured 13-month navigation,
request, native SVG and table response took 62.71 ms on this host. Timing starts
before keyboard navigation and stops after the response, multi-point chart paths,
13 table rows, and two animation frames are present. Later assertions and
screenshots are excluded. This is bounded evidence, not a cross-platform
performance guarantee.

The browser journey records these Analytics captures in its temporary evidence
directory:

- `analytics-capacity-wide.png` and `analytics-capacity-narrow.png`
- `analytics-capacity-light.png` and `analytics-capacity-forced.png`
- `analytics-capacity-zoom-reflow.png` (720 CSS pixels, the 200%-equivalent
  layout width for a 1440-pixel viewport; not native browser-zoom evidence)
- `analytics-compare-wide.png` and `analytics-compare-narrow.png`

Assertions cover a real 13-month service response, an explicit unavailable gap,
chart paths with multiple points, chart/table equivalence, range and
provider-window filters, safe response projection, unchanged Selected Profile,
separate identities and units, keyboard Enter activation for route/tab/disclosure,
narrow and 720px disclosure/reflow, no horizontal overflow, an exposed table and
status in Chromium's accessibility tree, and zero automated Axe violations on
both surfaces. The broader dashboard journey retains missing, partial, future,
zero, offline, and reauthentication scenarios.

## Qualification limits

The evidence uses a deterministic fake Codex provider, not a live account.
Headless-browser accessibility inspection and Axe are not spoken screen-reader
evidence. Native 200% browser zoom, native OS contrast modes, Firefox, Safari,
live-provider behavior, and non-Linux runtimes remain unqualified here. The
canonical repository verifier remains the release gate for the exact commit.
