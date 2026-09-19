# Portable configuration evidence

Issue #78 adds schema-v1, preview-first export and import for an explicit
nonsecret allowlist. The service/store layer owns validation, conflict
resolution, digest binding, and the atomic apply. The authenticated loopback
API and generated clients expose that same owner to the CLI and dashboard.

The Signal Rail Settings surface keeps export and import in adjacent flat
sections. It reports included fields, counts, always-excluded data, and every
local conflict before enabling download or apply. Project Aliases are an
explicit export option and carry only an unambiguous repository basename.

The real-service browser journey covers reviewed download, exclusion checks,
conflict cancellation, exact apply, Pending imported state, local Identity Home
selection, and fake-Codex authentication. Store, HTTP, CLI, generated-contract,
frontend, accessibility, reflow, and canonical repository verification provide
the remaining executable evidence. Native non-WSL qualification is outside the
available environment and is not claimed.
