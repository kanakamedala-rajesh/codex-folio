# Grill-with-docs status

## Status

The user-level design frontier is empty as of 2026-08-29. Decisions Q1–Q122 establish the product, companion boundary, domain language, security/privacy posture, switching and continuation guarantees, dashboard direction, distribution model, architecture, quality budgets, delivery order, and stable-release gate.

Implementation must not begin until the user confirms this shared understanding, as required by the grilling workflow.

## Design tree

- **Product and scope — settled**
  - Local-first Codex companion, not a replacement.
  - Go CLI/service with embedded React dashboard.
  - Open-source individual-developer MVP; enterprise modules later.
- **Identity and launch — settled**
  - Identity Profiles, Managed/Referenced/Shared homes, Selected/Dashboard/Launch states.
  - Codex-owned authentication and transparent foreground launch.
  - Persistent selection shared by dashboard and interactive CLI without mutating running processes.
- **Continuation — settled**
  - Guaranteed repository-first new-session continuation across authenticated profiles/workspaces.
  - No live hot-swap guarantee; exact continuation is explicit, experimental, reversible, and non-blocking.
- **Usage and analytics — settled**
  - Supported read-only sources, normalized allowlisted storage, provenance and availability.
  - Honest combined semantics, bounded retention, JSON/CSV export, metadata-only activity.
- **Security and privacy — settled**
  - Tiered vault, field encryption, authorized loopback, no raw transcripts/provider payloads.
  - Redacted local diagnostics, opt-in allowlisted telemetry, explicit purge/recovery.
- **Dashboard and UX — settled**
  - Signal Rail comp-first direction, six routes, glance-first hierarchy, wide/narrow behavior.
  - Complete light/dark themes, one accessible density, WCAG 2.2 AA, UI/UX skill-gated implementation.
- **Operations and distribution — settled**
  - One user service, native opt-in lifecycle, archive-first releases, signed stable artifacts.
  - Update notification only, capability-level Codex compatibility, portable non-secret configuration.
- **Architecture and quality — settled**
  - Modular monolith, deep modules, external seams, generated OpenAPI browser client, one state owner.
  - Layered tests, stricter experimental qualification, regression budgets, production stable-release gate.

## Next action after confirmation

Create the consolidated implementation plan with milestone work breakdown, dependencies, acceptance criteria, test evidence, risk register, and explicit deferred scope. Then stop for plan approval before scaffolding the new repository.
