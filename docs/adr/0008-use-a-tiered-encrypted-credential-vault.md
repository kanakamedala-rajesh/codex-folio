# Use a tiered encrypted credential vault

**Partially superseded by [ADR 0035](0035-qualify-prompt-free-secure-storage.md)** for qualified prompt-free storage, WSL protection, migration, and removal of plaintext fallback. The original decision and rationale are retained below.

Experimental Shared Work Home mode requires CodexFolio to reactivate multiple persistent Codex credentials without routine OpenAI login. Profile records will therefore be encrypted with an application key protected by DPAPI, Keychain, or supported Linux Secret Service, with a passphrase-encrypted fallback for WSL and headless Linux; plaintext storage is an explicit compatibility fallback and never a silent downgrade. A passphrase-backed headless service starts locked after reboot, collects no sensitive information while locked, and resumes only after an explicit per-session `codex-folio vault unlock`.
