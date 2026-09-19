# Operational alerts implementation evidence — issue #73

This document records the bounded production evidence for executable issue
[#73](https://github.com/kanakamedala-rajesh/codex-folio/issues/73), under parent
specification [#57](https://github.com/kanakamedala-rajesh/codex-folio/issues/57).

## Implemented contract

- The Alerts surface derives capacity warning, critical, exhausted, approaching
  reset and supported credit-expiry conditions only from normalized provider
  evidence. Missing, partial and contradictory evidence does not manufacture a
  capacity alert.
- Warning and critical remaining-capacity thresholds default to 20% and 10%
  and can be changed per profile and provider window. Manual and scheduled
  refreshes invoke the same evaluator with an injectable clock.
- Reauthentication, stale evidence, repeated collection failure and provider
  compatibility changes produce explicit conditions with recovery guidance.
- SQLite persists active conditions, acknowledgments and bounded history.
  Stable condition keys deduplicate repeated observations and preserve first and
  last observation times.
- The authenticated generated browser client is the only browser API path.
  Mutations require session authorization and CSRF protection. The initial
  delivery-health projection reports native enrollment state without enrolling
  or adding a second background service.
- Alerts are informational only. This issue adds no automated rule, action or
  experiment execution.

## Browser evidence

The repository browser journey runs against the real loopback HTTP service,
generated client, CSRF/session authorization, SQLite store and embedded
production assets. A deterministic provider fixture opens Alerts at a narrow
viewport, changes the selected profile/window thresholds, observes the resulting
warning, acknowledges it, verifies its history entry and checks delivery-health
guidance. It then verifies the scoped Overview notice from the same service data.

The journey records the `alerts-narrow.png` capture in its temporary evidence
directory and reports zero automated Axe violations on the Alerts surface. The
unit and store suites separately cover reset proximity, supported credit expiry,
reauthentication, stale evidence, repeated failures, compatibility changes,
false-positive suppression, deduplication, reopening, acknowledgment and history
pruning.

## Qualification limits

The evidence uses a deterministic fake Codex provider, not a live account.
Headless-browser accessibility inspection and Axe are not spoken screen-reader
evidence. Native notification delivery is intentionally not implemented in this
ticket; the surface reports whether native enrollment is configured and gives
explicit terminal guidance. Non-Linux browser runtimes remain compile-only or
unqualified here. The canonical repository verifier remains the release gate for
the exact commit.

The repository owner explicitly accepted a ticket-specific qualification
exception on 2026-09-19 for the missing manual spoken screen-reader and actual
browser 200% zoom evidence. The automated accessibility, keyboard, semantic,
and responsive checks remain recorded above. This exception does not claim
WCAG 2.2 AA or native assistive-technology qualification, and it does not waive
the parent milestone's accessibility requirements for other tickets.
