# Concurrent dashboard loading — issue #106

The isolated reproduction uses three synthetic profiles with fourteen usage
captures each (twelve retained in the recent projection), including encrypted
login/workspace scope and a failed capture. No real Identity Home or credential
is inspected.

## Diagnosis and correction

Both analytics recent history and alert evidence opened two encrypted scope
fields per capture. `EnvelopeVault` consults its key provider on each operation;
the WSL provider serializes native helper calls. Neither consumer uses these
historical identity fields. Latest identity scope remains necessary for ranking
and aggregate deduplication.

Measured on WSL2 Linux AMD64 with a synthetic 1 ms delay per vault decryption:

| Three-profile read | Before | After |
| --- | --- | --- |
| Recent dashboard metrics | 72 decryptions, 108 ms | 0 decryptions, 9 ms |
| Alert evidence | 72 decryptions, 108 ms | 0 decryptions, 14 ms |

These timings demonstrate the unnecessary native-helper amplification, not a
measurement of the original user's protected data. SQL queries and normalized
metric evidence remain equivalent. Notification delivery is disabled in this
fixture; no native notification timing or improvement is claimed. Existing
notification delivery budgets remain unchanged.

The first overlapping HTTPS requests in the 806-session browser fixture were
also timed inside the server. For the baseline, only the two history callers
were temporarily switched back to the full-scope reader. Both runs used the
same synthetic one-millisecond delay on every vault decryption; the timing
probes and baseline switch were removed afterward. `SQLite read/scan` is the
elapsed time around the recent-snapshot SQL queries and row scans, including
any connection wait. It excludes other route queries and response work.

| First overlapping request | Full-scope baseline | Metric projection |
| --- | --- | --- |
| Analytics route | 105.7 ms; SQLite read/scan 15.0 ms; vault 46 decryptions, 62.5 ms | 49.7 ms; SQLite read/scan 19.3 ms; vault 2 decryptions, 2.2 ms |
| Alerts route | 98.5 ms; SQLite read/scan 19.7 ms; vault 44 decryptions, 57.9 ms | 40.7 ms; SQLite read/scan 21.8 ms; vault 0 decryptions, 0 ms |
| Browser completion, analytics / alerts | 127 / 107 ms | 65 / 48 ms |

SQLite's single-connection pool did wait during both overlaps. Per-history-call
`DB.Stats` deltas reached 7.2 ms in the baseline and 13.9 ms with the
projection. These shared-pool deltas can overlap between simultaneous calls
and must not be added across calls or to the route timings. The measured vault
time was the largest baseline component; removing historical scope decryptions
shortened both routes despite the remaining SQLite work. The fixture creates
the alert service without delivery options, so `deliver` returns before any
notification adapter call; `DeliveryHealth` reports `not_enrolled` without
calling an adapter. Notification delivery is therefore absent from this
reproduced path, rather than an unmeasured contributor.

Sanitized measurement logs are
`/tmp/folio-106/baseline-wait-measurement.log` and
`/tmp/folio-106/current-wait-measurement.log`. This is a controlled diagnosis
of representative retained history, not a claim that the original WSL2 user's
exact server-side wait point was observed.

The metric-only history projection omits those decryptions while preserving
full-history callers and latest-snapshot identity scope. No keys are cached.
A ten-second request budget for analytics and GET alerts returns the existing
safe JSON `CF_HTTPAPI_SERVICE_UNAVAILABLE` response with HTTP 503 before the
server's fifteen-second write deadline. Cancellation reaches alert thresholds
and delivery-health reads as well. Late writes remain confined to the timeout
handler. Authentication and CSRF checks still precede these handlers.

## Browser and regression evidence

On 2026-09-29, Chromium 149.0.7827.55 ran on WSL2 kernel
6.6.87.2-microsoft-standard-WSL2, Linux AMD64. The real local HTTPS service held
806 synthetic retained sessions and fourteen historical scoped usage captures
per profile for two profiles. Concurrent browser fetches returned HTTP 200:
analytics in 61 ms and alerts in 48 ms. The browser trusted only the generated
fixture certificate's SPKI using the existing test convention; application TLS
verification and browser authorization were not disabled.

The `startup-partial` browser phase checks:

- available profiles and service health while both optional requests remain pending;
- network analytics failure and HTTP 503 alerts failure independently;
- accessible retry feedback and independent recovery without service restart;
- genuine authorization expiry still requiring a fresh launcher link;
- desktop and narrow layouts, axe accessibility checks, and no runtime errors
  or remote assets.

The canonical deep browser suite includes this phase. A focused invocation is:

```sh
CODEX_FOLIO_BROWSER_TEST=1 CODEX_FOLIO_BROWSER_SUITE=startup-partial go test ./cmd/codex-folio -run '^TestOverviewBrowser$' -count=1 -v -timeout=5m
```

Use the documented `CODEX_FOLIO_CHROMIUM` override for an installed browser.
Focused store regressions assert identical normalized evidence, bounded history,
failed-capture gaps, zero historical scope decryptions, and preserved latest
scope. HTTP regressions exercise simultaneous timeout responses over TLS and
safe cancellation. This evidence uses fake Codex and isolated state, not live
provider access, native Linux/macOS qualification, or release qualification.

The full verifier also exposed a pre-existing trust-fixture cleanup stall in
this Chromium environment: grant and cross-origin isolation checks completed,
but the secondary test HTTP server waited for a remaining browser connection
on close. Temporary stage instrumentation identified that wait and was removed.
Closing that test server's connections after closing its listener resolved the
stall; the complete trust/restart smoke suite then passed in 6.26 seconds. This
cleanup changes test infrastructure only and retains the cross-origin and
impostor-certificate assertions.
