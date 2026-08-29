# Distinguish managed and referenced Identity Homes

CodexFolio must support existing Codex installations without duplicating their storage or assuming destructive authority over them. Identity Homes created inside app-local storage are Managed Identity Homes whose backup, quarantine, restoration, and purge lifecycle belongs to CodexFolio. Existing homes registered in place are Referenced Identity Homes: CodexFolio may launch and observe them, but profile removal deletes only its registry reference. This ownership distinction also governs migration, UI warnings, and recovery behavior.
