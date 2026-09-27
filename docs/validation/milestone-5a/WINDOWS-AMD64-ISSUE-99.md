# Issue 99 Windows AMD64 qualification evidence

Date: 2026-09-27
Ticket: #99, parent specification #82
Result: candidate qualification passed; final review, exact-commit verification,
and hosted delivery remain pending.

This is the retained sanitized audit record. It deliberately excludes
credentials, account identifiers, Identity Home paths, raw authentication
streams, private conversation content, bootstrap URLs, browser session values,
and provider session identifiers. Automated fixture evidence is separated from
the user-authorized real-identity journey.

## Candidate identity

- Branch: `milestone/5a-everyday-companion`
- Ticket base: `93fc0370a6e088eff80c3836fc30647cb3cf9c67`
- Candidate HEAD: `2e2ce309ee02da1a745d8aa71c94a6f4cdadf3d8`
- Build classification: unsigned dirty development candidate
- The candidate HEAD contains reviewed issue #88 Windows passphrase migration.
  The complete #99 working-tree source content adds:
  - `cmd/codex-folio/dashboard_browser_test.go`:
    `9ab0ce4e5134497a6c23b211902069111a056861`
  - `cmd/codex-folio/service.go`:
    `1eb4ade76aa09a105a67d678dade0bea3fd2ed65`
  - `cmd/codex-folio/service_port_windows_test.go`:
    `41cc4485134a36540cf7d6540b33d9fd0be75ffe`
- The Windows migration implementation includes native DPAPI reopen, atomic
  passphrase-vault handoff, interruption recovery, provider-failure rollback,
  ordinary-startup immediate launch, and repeat prompt-free startup tests.
  Representative content identities are
  `cmd/codex-folio/passphrase_migration_windows_test.go` =
  `60bb15e0ac544a2952c3c12a5319b284497681c4` and
  `cmd/codex-folio/service_store_windows.go` =
  `c98fad411d1d8ae25c6722f37e6be5a1d02a9e0d`.

The candidate narrows only the deterministic Windows dashboard port range to
20000--39999 and adds its Windows regression test. It also makes the native
Windows browser harness capture the prepared sanitized handoff argument before
the batch fixture, whose `%~1` expansion cannot carry the JSON losslessly; the
native foreground start, wait, and exit lifecycle remains exercised.

## Native environment and prerequisites

- Windows 11 AMD64, interactive user session, Asia/Calcutta time zone
- Go 1.27.0
- Pinned Node.js 24.18.0 and npm 11.16.0, used from an isolated temporary
  toolchain
- OpenAI Codex 0.158.0-alpha.2.1, native Windows AMD64 executable. The initial
  journey used an isolated exact-version installation; the reboot repetition
  used the installed exact-version executable.
- User-scoped Windows DPAPI was exercised successfully; routine startup and
  restart required no CodexFolio passphrase or separate vault unlock
- Smart App Control was restored before final validation and remained on across
  the qualifying Windows reboot (`VerifiedAndReputablePolicyState=1`). Windows
  Defender and Windows Firewall also remained enabled.

The earlier isolated Codex installation, temporary trusted dashboard root
certificate, and temporary CodexFolio PATH entry were removed and verified
absent. The qualifying reboot used the installed exact-version Codex executable.
The portable Node toolchain and pinned browser used for final verification are
temporary and will be removed after hosted delivery. The isolated real-auth
validation state is retained because deletion was not authorized.

## Automated and fixture evidence

Fixture-backed results below do not satisfy the real-authentication gate.

| Check | Result |
| --- | --- |
| `git diff --check` | PASS |
| `go test ./cmd/codex-folio -run '^(TestDashboardPortAvoidsWindowsDynamicRange|TestServiceStartInheritsRememberedVaultMode)$' -count=20` | PASS, twenty consecutive repetitions |
| Deep authenticated browser journey | PASS in 97.42 s, including accessibility, handoff, and native Windows lifecycle; warm native launch 156 ms |
| Windows migration-focused suite | PASS: ordinary startup to real DPAPI and immediate launch; retained-state reopen; midpoint promotion recovery; destination failure rollback |
| `node scripts/verify.mjs` | PASS on the complete dirty candidate |

The canonical verifier installed locked frontend dependencies and completed
all Phase 0 checks, all Go tests, native Windows dirty-build identity checks,
cross-target compilation, archive and SBOM validation, DCO/governance checks,
frontend format/lint/type/test/build, pinned-browser install, native browser
smoke, deep browser journeys, and the startup benchmark. The startup benchmark
reported a 238.6 ms median and 492.6 ms maximum. The verifier confirmed tracked
source and lockfile immutability and ended without remote mutation.

## User-authorized real-identity journey

| Required behavior | Sanitized result |
| --- | --- |
| Plain guided setup | PASS. Native DPAPI was selected without a passphrase. Background metadata collection was offered separately and declined. The one-time PATH flow completed. |
| Two identity additions | PASS. Two distinct Managed Identity Profiles, `work` and `personal`, completed browser authentication, completion detection, readiness validation, and selection. |
| Consented history handling | PASS for consent boundaries. Empty managed-source import was accepted once; a separate existing-history offer was explicitly declined. No source file was changed and no raw history was retained in this report. |
| Distinct live-provider evidence | PASS. Each profile refreshed a different primary-window result through Codex app-server 0.158.0-alpha.2.1. Secondary-window data was reported unsupported rather than zero. |
| Real foreground launch | PASS. Each profile launched the installed native Codex executable, returned an exact validation marker, and exited with status 0. Activity lifecycle records also reached exited status 0. |
| Terminal behavior | PASS. A real interactive foreground Codex TUI was attached to the invoking terminal and exited without the companion terminating it. |
| Post-child companion lifetime | PASS. The companion remained ready, unlocked, and database-ready after completed Codex launches. |
| Ordinary no-browser launch | PASS. Repeated profile launches did not reopen authentication or dashboard bootstrap flows. |
| Trusted dashboard reopening | PASS. After explicit local-browser trust, a fresh Edge tab opened the ordinary non-bootstrap loopback URL. After companion stop/start, the same ordinary URL renewed a short dashboard session and retained both profiles. |
| Environment restart repetition | PASS. After a real Windows reboot, the first CodexFolio action was the plain command. It started the on-demand companion, reopened DPAPI state, and displayed both retained Ready profiles without a passphrase, vault unlock, authentication browser, or specialist service start. Both profiles then launched Codex 0.158.0-alpha.2.1 through ordinary launch commands and exited 0. |
| Explicit stop | PASS. An idle companion accepted explicit stop and reached stopped state. |
| Deferred stop | PASS. With one real foreground launch active, stop returned `pending` with one active launch; the companion remained ready, Codex was not terminated, and the companion stopped automatically only after the foreground process exited. |

## Qualification boundaries

### Required retained-state migration: implemented and verified

Ticket #99 requires retained-state migration on the qualified Windows
configuration, and EC-01 requires existing passphrase users to migrate without
losing profiles, history, or other retained state. The candidate now supports
that path through ordinary startup:

- Windows can privately unlock the old passphrase vault once and re-protect
  allowlisted retained fields through current-user DPAPI;
- the old passphrase vault and validated database backup remain isolated until
  destination reopen and protected-state verification succeed;
- wrong passphrases, destination failure, interruption before or during vault
  promotion, and restart restore or preserve the old protected generation; and
- production-path tests exercise ordinary startup, real DPAPI, immediate
  foreground launch, retained profile/Identity Home reopen, and prompt-free
  repeat selection. Shared tests cover the complete retained protected-field
  allowlist, cancellation, explicit passphrase continuation, and rollback.

The implementation is committed as `2e2ce309ee02da1a745d8aa71c94a6f4cdadf3d8`
after a full #88 `REVIEW PASS`. Documentation covers Windows, Linux, and WSL
without treating WSL fixture evidence as native Windows evidence.

### Optional OS-login enrollment: unavailable in this session

On-demand companion startup and restart passed. Optional per-user Task
Scheduler enrollment did not: `service install` returned
`CF_PLATFORM_SERVICE_UNAVAILABLE`, and the underlying shipped
`schtasks.exe /Create ... /RL LIMITED` operation returned `Access is denied`
from the medium-integrity user token. The Task Scheduler service itself was
running. No alternate startup mechanism was installed, and no elevated task
was left behind. This report does not claim Windows OS-login enrollment.

### Full machine restart: exercised

Windows reported a boot time of `2026-09-27T18:57:33.5000000+05:30`. The first
CodexFolio action after that boot was the plain command against the retained
state root. It reached the picker and started the owner without a separate
service command. The companion then reported ready/unlocked/database-ready,
and both retained profiles launched the installed Codex executable with exit 0.
The interactive Work TUI also reached Codex before its external workspace
routing request returned 401; that provider-session result is kept distinct
from the successful CodexFolio retention, DPAPI, process-launch, and no-browser
evidence.

## Reproduction boundary

Re-run the canonical verifier with `node scripts/verify.mjs` on the exact
candidate content above. Real-auth steps require user-controlled identities and
must be observed only through sanitized readiness, usage, launch, activity,
trust-renewal, and lifecycle outcomes. Do not open Identity Homes during review,
record raw login output, preserve bootstrap URLs, or substitute fake-provider
results for the native journey.
