# Milestone 5 native service evidence

Status: ticket [#71](https://github.com/kanakamedala-rajesh/codex-folio/issues/71)
candidate evidence. The exact ticket commit is the commit containing this record;
its clean-commit verification remains part of the repository completion gate.

## WSL2 systemd-user lifecycle

Validated on 2026-09-18 using Ubuntu 24.04.3 LTS, x86_64, kernel
`6.6.87.2-microsoft-standard-WSL2`, against the dirty candidate based on
`89e61305f9353d1ebfe44d825531f616374f0e35`.

The validation used the built `codex-folio` executable, a fresh state root
under `/tmp`, passphrase vault mode, and the host's real `systemd --user`
manager. It did not use a fake command runner.

| Check | Result |
| --- | --- |
| first `service install --json` | PASS: `systemd-user`, active, installed, available, changed |
| `service status --json` | PASS: running and enrolled active; vault/service locked; database not checked |
| on-demand `service start --json` reuse | PASS: reused the single owner and returned a fresh dashboard URL |
| repeated `service install --json` | PASS: remained active and reported `changed: false` |
| enrolled journal safety | PASS: zero `bootstrap=` or `dashboard:` credential-bearing entries |
| `service uninstall --json` | PASS: not installed, inactive, available, changed |
| post-uninstall status | PASS: stopped and not installed |
| retained state | PASS: pre-existing marker remained after uninstall |
| orderly systemd stop | PASS: owner and client runtime descriptors were absent after SIGTERM shutdown |

The first status attempt occurred while the newly started process was still
publishing its runtime metadata; the second attempt succeeded approximately
0.2 seconds later. No fallback enrollment mechanism was used.

## Platform qualification exceptions

The repository owner explicitly accepted platform exceptions on 2026-09-18
because this development environment is WSL2. Consequently, this ticket does
not claim native lifecycle qualification for Windows AMD64, macOS ARM64, or
non-WSL Linux AMD64 from this run. Their adapters are covered by argument-vector,
quoting, status, idempotence, failure, and unavailable-mechanism fixtures, but
those fixtures and compile checks are not represented as native enrollment
evidence.

| Environment | Native lifecycle result |
| --- | --- |
| WSL2 x86_64 with systemd-user | PASS, as recorded above |
| Windows AMD64 Task Scheduler | qualification exception accepted; not run |
| macOS ARM64 LaunchAgent | qualification exception accepted; not run |
| non-WSL Linux AMD64 systemd-user | qualification exception accepted; not run |
