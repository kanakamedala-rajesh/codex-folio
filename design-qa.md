# Design QA — Issue #66 Analytics tabs

Source target: `docs/design/signal-rail/v1/concepts/analytics-corrected.png` and the accepted Signal Rail v1 capacity/compare evidence.

Rendered evidence: Playwright captures under the temporary `codex-folio-overview-browser` evidence directory for Tokens, Projects, Models, and Activity at 1440 × 1000 and 390 × 844. The same authenticated Chromium journey exercises native disclosure and alias controls by keyboard, checks the accessibility tree's table and live-status roles, emulates forced colors, and applies Chromium's page-scale factor at 200% (640px layout viewport, 320px visual viewport) while checking for essential horizontal overflow.

## Comparison

- Signal Rail hierarchy, flat ruled sections, six Analytics tabs, cyan scoped values, compact native SVG charts, and semantic tables match the accepted direction.
- Wide views retain the labeled rail and narrow views retain the scope/status header and bottom navigation.
- Real fixture values replace illustrative reference values. Unsupported dimensions are written explicitly rather than fabricated.
- Project views show only aliases and basenames; no canonical paths or repository-local UI artifacts are rendered.

## Findings

- P2: chart labels initially inherited the browser's default SVG text color because the fill referenced a nonexistent variable. Fixed by using the established `--text` token.
- No remaining P0, P1, or P2 visual defects were observed.
- Headless Chromium page scale at 200%, forced-colors emulation, keyboard operation, semantic table equivalence, and accessibility-tree/live-status evidence are qualified by the deep browser journey. Exact spoken screen-reader, Firefox/Safari, native OS high-contrast, and browser-chrome/native-OS zoom remain outside this environment and are not claimed. On 2026-09-13, the repository owner explicitly accepted those remaining manual-validation exceptions for issue #66 so delivery could proceed without upgrading their qualification status.

final result: passed
