# Preserve and recover SQLite instead of silently resetting

CodexFolio will keep three rotating encryption-aware app-local database backups around migrations and successful recovery checkpoints. If integrity verification or migration fails, the service stops writes, preserves the damaged database for investigation, and offers explicit verification, rollback, or redacted export. It never resolves corruption by silently creating an empty database, because that would erase analytics, profile metadata, retention state, and encrypted checkpoint references without informed consent.
