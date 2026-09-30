# Encrypt sensitive SQLite fields through the vault

**Preserved by [ADR 0035](0035-qualify-prompt-free-secure-storage.md)**: prompt-free protection and migration do not remove sensitive-field encryption or place credentials in SQLite.

CodexFolio will keep normalized non-secret usage facts in a pure-Go SQLite database protected by user-only filesystem permissions. Sensitive values such as canonical project paths, sanitized checkpoints, and recovery metadata are envelope-encrypted through the established tiered vault, and credentials never enter SQLite. This preserves query performance and cross-compilation while avoiding the native-library burden of whole-database SQLCipher. Schema design must ensure encrypted values are not accidentally indexed, logged, or returned to the browser without an authorized projection.
