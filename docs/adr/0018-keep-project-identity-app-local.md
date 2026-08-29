# Keep Project Identity app-local

Usage-by-project requires stable local recognition without modifying user repositories. CodexFolio will maintain an encrypted app-local mapping from canonical repository location to a user-editable Project Alias. Browser responses and ordinary exports use aliases and basenames; full paths require explicit inclusion and remain excluded from diagnostics and telemetry. Repository moves are an app-local reconciliation concern, never a reason to create metadata files in the repository.
