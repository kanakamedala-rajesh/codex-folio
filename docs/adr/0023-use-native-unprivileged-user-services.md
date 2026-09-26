# Use native unprivileged user services

**Clarified by [ADR 0037](0037-separate-companion-lifetime-from-consent-and-attribution.md)**: this enrollment decision does not require OS-login installation for automatic on-demand service lifetime or authorize collection consent.

Periodic snapshots and native notifications belong to the signed-in user and must not require a machine-wide privileged daemon. Explicit service installation will use per-user Task Scheduler on Windows, LaunchAgent on macOS, and `systemd --user` on Linux or systemd-enabled WSL2. Where the native mechanism is unavailable, CodexFolio remains on demand rather than silently installing an alternate autostart path. Service removal is explicit and reversible.
