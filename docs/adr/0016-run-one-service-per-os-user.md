# Run one service per operating-system user

**Extended by [ADR 0037](0037-separate-companion-lifetime-from-consent-and-attribution.md)**: automatic on-demand ownership survives Codex exit; persistent OS-login enrollment remains explicit. The sole-writer rationale is unchanged.

CodexFolio's Identity Profiles, encrypted vault, SQLite history, collection schedule, and notifications are user-scoped. The MVP will therefore permit one service instance per OS user. CLI invocations discover and reuse the healthy instance or start an on-demand one; persistent installation is explicit and reversible. This avoids competing writers and ambiguous notification ownership while preserving terminal-only operation.
