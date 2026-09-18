# Milestone 5 periodic collection evidence

This evidence belongs to executable ticket #72. The source-controlled version
of this file is bound to the ticket's reviewed commit; review and delivery
records identify that exact commit and its complete content identity.

## Resource and write bounds

Recorded on 2026-09-18 under WSL2 Linux 6.6.87.2, x86-64, on an Intel Core
Ultra 7 155H allocation with 12 logical CPUs and 8,329,052,160 bytes of memory.
The command was:

```sh
go test ./cmd/codex-folio -run '^TestEnrolledServiceIdleResourceBudgetOnLinux$' -count=1 -v
```

The test composed the real SQLite store, unlocked memory vault, enrolled
scheduler, operational workflows, and authenticated HTTP API with no profiles.
After a 100 ms settling interval, `/proc/self/stat` and `/proc/self/statm` were
sampled across 2.001 seconds. Resident memory was 18,026,496 bytes and sustained
CPU was 0.000%, below the milestone budgets of 75 MiB and 1%.

`TestSchedulerDoesNoCollectionOrStateWriteBeforePersistedAttemptIsDue` advances
an injected clock through repeated not-due ticks and records zero collector calls
and zero schedule-state writes. The scheduler evaluates at most 64 targets and
collects at most four profiles per tick, stores only one future attempt per
profile, coalesces an overlapping tick, and does not enumerate or replay missed
downtime intervals.

## Behavioral evidence

- `TestSchedulerCollectsThroughRealUsageServiceAndSQLiteStore` runs a scheduled
  refresh through the real usage service and normalized SQLite persistence with
  a deterministic collector.
- Scheduler tests cover active and idle defaults, configured floor enforcement,
  known and unknown reset metadata, persisted restart state, exponential
  backoff, bounded jitter, recovery, and overlap coalescing without real waits.
- Store migration tests preserve an existing schema-v18 usage snapshot and then
  accept the new periodic trigger contract.
- Service lifecycle tests prove that on-demand composition has no scheduler,
  explicit enrollment does, and passphrase-backed workflows do not compose or
  activate before a successful unlock.
- The deep Playwright journey uses the generated browser client and real
  authenticated loopback service to save and read back the cadence, while
  proving that the on-demand fixture remains unenrolled and retaining the
  existing accessibility and responsive checks.

## Qualification limits

The collector uses deterministic fake provider output; this is not a live
provider timing or rate-limit qualification. The resource sample is one native
WSL2 reference condition and not a universal performance promise. Windows and
macOS compile/runtime evidence remains governed by repository CI and milestone
native-platform qualification. The five-minute provider floor is a conservative
local policy because the supported App Server contract exposes rate-limit reads,
updates, durations, and reset timestamps but publishes no polling cadence.
