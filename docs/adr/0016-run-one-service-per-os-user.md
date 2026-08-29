# Run one service per operating-system user

CodexFolio's Identity Profiles, encrypted vault, SQLite history, collection schedule, and notifications are user-scoped. The MVP will therefore permit one service instance per OS user. CLI invocations discover and reuse the healthy instance or start an on-demand one; persistent installation is explicit and reversible. This avoids competing writers and ambiguous notification ownership while preserving terminal-only operation.
