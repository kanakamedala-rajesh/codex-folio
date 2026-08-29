# Store only allowlisted normalized metrics

CodexFolio will transform supported source responses into a reviewed normalized schema and discard unrecognized fields rather than archive raw payloads. Observations retain only the profile, source, provenance, metric identity, value and unit where applicable, quota-window boundaries, capture time, freshness, and availability state. This reduces accidental retention when a provider adds sensitive fields and makes schema changes deliberate and testable.
