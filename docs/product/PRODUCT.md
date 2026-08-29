# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

- Native Go executable containing the CLI, application service, loopback HTTP API, SQLite persistence, and embedded frontend assets.
- React, TypeScript, and Vite frontend compiled into versioned static assets embedded by Go.
- A tested charting library hidden behind internal visualization components so domain and UI contracts do not depend on it.
- Browser-hosted MVP with transport-independent UI boundaries for a possible later native shell.
- Comp-first visual development: approve a high-fidelity dashboard concept before UI implementation.

## Users

The primary user is an individual developer who uses multiple Codex login identities, workspaces, subscriptions, or API-key contexts on one machine. They need to choose a Launch Profile, understand current and historical usage, and continue repository work when that profile reaches a quota limit.

Enterprise administrators and centrally managed deployments are not MVP users. Future enterprise integrations may add separately permissioned policy and analytics modules.

## Product Purpose

VS CodexFolio is an independent, local-first Codex companion combining a CLI identity launcher with a responsive usage dashboard. It reduces routine reauthentication, makes quota and local usage understandable across Identity Profiles, and provides a safe path for continuing interrupted repository work.

CodexFolio always launches and complements the user's installed Codex executable. It is not a Codex client, terminal UI, session engine, model router, repository manager, or replacement authentication implementation. Codex continues to own those behaviors and may evolve independently; CodexFolio integrates through documented or explicitly experimental, version-gated adapter seams.

The overview's primary outcome is to answer: **“Can I continue working, and which identity should I use?”** It shows the current Dashboard Scope's immediate limits first, eligible alternatives second, and trends and detailed analytics afterward.

The dashboard is glance-first with progressive disclosure. Detailed analytics remain one navigation level deeper rather than competing with the interruption workflow.

## Positioning

CodexFolio unifies three capabilities that neighboring account switchers or usage dashboards generally treat separately:

1. Persistent multi-identity Codex launch without routine browser login.
2. Source-attributed usage and health information for the selected Dashboard Scope with an explicit combined view.
3. Guaranteed repository-first continuation plus compatibility-gated experimental exact session continuation.

## Operating Context

- Runs locally on Windows AMD64, Linux AMD64, WSL2, and macOS ARM64 as tier-one MVP platforms.
- Works without GUI access through the `codex-folio` CLI.
- Opens an embedded responsive SPA in the system browser when a GUI is available.
- Supports ChatGPT subscription identities, effective workspace contexts, API-key identities, and later supported credential sources as Identity Profiles.
- Uses Codex-managed persistent authentication for isolated profiles and a tiered encrypted vault only for experimental Shared Work Home switching.
- Collects on demand by default; an explicitly installed user service may collect periodic snapshots and deliver limit alerts.

## Capabilities and Constraints

- Isolated Identity Homes are the recommended default; experimental Shared Work Home mode serializes identity use while sharing sessions, configuration, skills, plugins, and agents.
- Dashboard metrics default to the active Identity Profile. Combined analytics is an explicit alternate scope.
- Provider-reported, locally derived, estimated, and observed-during-session metrics are visibly distinguished.
- The dashboard may refresh, filter, compare, export, launch, hand off, diagnose, and manage local Identity Profiles.
- Launch and handoff actions ultimately invoke the installed Codex application; CodexFolio does not execute prompts, tools, or model turns itself.
- It cannot buy or consume credits, alter remote user/workspace/billing information, or change managed Codex policy.
- Profile deletion uses typed confirmation and seven-day local quarantine; it does not delete a remote OpenAI identity.
- Analytics stores normalized metadata and aggregates for 13 months by default, configurable from 30 days to unlimited.
- Prompts, responses, commands, raw diffs, tool output, raw session records, and credentials are excluded from analytics and browser responses.
- Safe Continuation is deterministic and repository-first. Transcript-assisted recovery requires explicit consent, review, editing, and approval.
- Safe Continuation across authenticated profiles and workspaces launches a new installed Codex process in the same working directory with an approved checkpoint; it does not hot-switch a running Codex process.
- Exact Continuation is experimental, version-gated, transactional, reversible, non-guaranteed across teams, and falls back safely.
- The MVP binds strictly to loopback. Future multi-device access requires a separately designed transport and authorization boundary.
- Remote CodexFolio telemetry is opt-in. Redacted local diagnostics are enabled by default and independently configurable.

## Brand Commitments

- Working product name: **VS CodexFolio**.
- CLI command: `codex-folio`.
- Description: “Switch local Codex identities and see usage in one place.”
- Public materials must state that CodexFolio is independent and is not affiliated with or endorsed by OpenAI.
- The name remains subject to formal release-time package, domain, and trademark clearance.

## Evidence on Hand

- `codex-as-go` demonstrates CLI identity selection, isolated login setup, Codex process wrapping, and cross-platform Go primitives, but its credential swapping and process locking are not production-safe.
- `vs-codexscope` demonstrates embedded React delivery, local session aggregation, typed Go services, loopback hardening, diagnostics, and safe export, but it is single-profile and uses undocumented online endpoints.
- Official Codex App Server documentation provides typed account, rate-limit, reset-credit, usage, thread-read, resume, and fork surfaces.
- No production user research, usability study, benchmark, testimonial, logo, or approved brand asset exists yet. Future work must not fabricate these.

## Product Principles

1. **Continue safely:** repository work must remain recoverable even when exact session transfer is unavailable.
2. **Provenance over precision theater:** every metric states where it came from and whether it is exact, derived, estimated, or merely observed.
3. **Local-first trust:** credentials and sensitive Codex state stay out of the browser, telemetry, logs, and remote services.
4. **CLI parity:** essential identity, recovery, and diagnostic workflows remain usable without a GUI.
5. **Experimental means reversible:** unsupported Codex seams are capability-gated, fail closed, and retain a tested fallback.
6. **Companion, never replacement:** prefer environment selection, process launch, and read-only adapters; do not duplicate Codex product behavior or assume authority over its workflow.

## Accessibility & Inclusion

- Meet WCAG 2.2 AA.
- Provide complete keyboard operation, visible focus, semantic landmarks, and screen-reader status announcements.
- Pair every chart with an accessible table or equivalent textual summary.
- Respect reduced-motion preferences and remain usable in high-contrast modes and at 200% zoom.
- Ship English first while externalizing all UI text.
- Format dates, times, numbers, timezones, durations, and week boundaries with the operating-system locale.
- Preserve essential overview, profile selection, alerts, and handoff workflows at narrow widths; replace dense tables with accessible drill-down rows rather than horizontal page scrolling.
