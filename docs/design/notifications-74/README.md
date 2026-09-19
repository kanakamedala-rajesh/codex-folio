# Native operational notification evidence for #74

Ticket #74 extends the accepted Signal Rail Alerts and Settings surfaces. The
production UI keeps notification privacy as a flat operational preference:
generic text is selected by default, detailed identity/quota text requires a
separate per-device choice, and delivery health remains visible without
displacing active conditions or thresholds.

## Behavioral evidence contract

- The existing alert evaluator remains the single manual, scheduled, and
  dashboard evaluation path. Native delivery is composed only for an explicitly
  enrolled service; no second daemon or remote push channel exists.
- A versioned SQLite migration defaults notification detail to false and adds a
  durable per-alert delivery lifecycle. New and reopened conditions become
  pending; repeated observations preserve delivery state. A durable claim
  precedes the OS call so recovery cannot create a duplicate flood.
- Recording-adapter tests verify exact generic and detailed projections,
  revocation, deduplication, bounded retries, failure isolation, and delivery
  health. Platform-runner tests verify direct argument-slice invocation for
  Windows toast, macOS Notification Center, and Linux desktop notifications;
  notification values are never interpolated into script source.
- Authenticated loopback and generated-client tests verify CSRF/session
  enforcement, default-off state, enablement and revocation. The real-service
  browser journey exercises the same controls in Alerts and Settings at the
  narrow layout and includes automated accessibility checks.

## Platform qualification ledger

| Environment | Implemented adapter | Current evidence and limit |
| --- | --- | --- |
| Windows AMD64 | PowerShell-hosted Windows Runtime toast in the enrolled user session | Argument construction and Windows compilation are automated. Hosted/headless runners do not establish an interactive notification surface, lock-screen rendering, or user permission. |
| macOS ARM64 | `osascript` Notification Center delivery in the enrolled user session | Argument construction and macOS compilation are automated. Hosted/headless runners do not establish Notification Center permission, visible delivery, or lock-screen rendering. |
| Linux AMD64 desktop | `notify-send` over the current desktop session | The adapter fails closed when `notify-send` or a desktop session is absent. Recording-runner behavior and Linux compilation are automated; a headless runner is not desktop-delivery evidence. |
| WSL2 Linux AMD64 | No substitute Windows or Linux desktop channel | The development host is identified separately as WSL2. Absent `notify-send`/desktop facilities are reported unavailable while dashboard alerts continue. |

### Accepted ticket-scoped exception

On 2026-09-19, the repository owner accepted an exception for ticket #74 only:
actual visible delivery, notification permission behavior, and locked-screen
behavior on Windows AMD64, macOS ARM64, and non-WSL Linux AMD64 remain
unqualified because interactive native facilities were unavailable. The WSL2
host is also not native Linux evidence and lacked `notify-send`. Automated
adapter and privacy tests, recording-runner evidence, Tier-1 compilation,
browser checks, and canonical verification are retained as non-native evidence.
This exception establishes neither native runtime nor native WCAG
qualification.

The automated suite does not relabel recording adapters, cross-compilation, or
headless CI as native visual or locked-screen success. Actual native permission,
visible delivery, and locked-screen behavior remain qualified only when the
corresponding interactive platform evidence is recorded. This limitation does
not weaken the default-generic privacy invariant or the safe unavailable state.
