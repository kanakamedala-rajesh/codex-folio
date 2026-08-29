# Use reauthentication instead of portable credential backups

CodexFolio will support portable non-secret configuration but will not export authentication, vault keys, or a cross-device credential archive in the MVP. A new device imports profile definitions and invokes Codex-owned authentication. This preserves the security assumptions of DPAPI, Keychain, Secret Service, and passphrase-backed local vaults rather than creating a universal high-value credential package whose protection and revocation semantics would be difficult to guarantee.
