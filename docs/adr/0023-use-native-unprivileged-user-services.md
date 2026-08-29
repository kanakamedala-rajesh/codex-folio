# Use native unprivileged user services

Periodic snapshots and native notifications belong to the signed-in user and must not require a machine-wide privileged daemon. Explicit service installation will use per-user Task Scheduler on Windows, LaunchAgent on macOS, and `systemd --user` on Linux or systemd-enabled WSL2. Where the native mechanism is unavailable, CodexFolio remains on demand rather than silently installing an alternate autostart path. Service removal is explicit and reversible.
