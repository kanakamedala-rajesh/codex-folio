# Make onboarding resumable and fail closed

**Extended by [ADR 0037](0037-separate-companion-lifetime-from-consent-and-attribution.md)** for existing-home registration, terminal completion detection, and separate history-import consent. Pending/readiness and independent-consent requirements remain unchanged.

First-run setup spans local discovery, Codex-owned authentication, profile/home registration, and several independent consent choices that may be interrupted. CodexFolio will commit completed stages, verify them on resume, and keep incomplete profiles Pending and unselectable. Interactive setup defaults to isolated homes and does not promote experimental features. Non-interactive setup requires every consent-bearing choice explicitly and fails closed when one is missing. This preserves automation without turning absence of input into authorization.
