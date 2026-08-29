# Architecture decision records

ADRs record consequential decisions that shape CodexFolio's product guarantees,
security boundaries, compatibility, or architecture. Read the records relevant
to a ticket before changing its implementation.

## Workflow

1. Copy the established concise format into the next zero-padded file named
   `NNNN-imperative-decision-title.md`.
2. State the decision and its reason, including rejected alternatives and
   consequences when they are necessary to review the tradeoff.
3. Link the ADR from this index and update affected product, architecture,
   acceptance, risk, or traceability documents in the same change.
4. Obtain review before implementation depends on the new decision.

Do not rewrite an accepted ADR to hide history. Small clarifications that do
not alter the decision may be edited in place. A material change uses a new ADR
and names the record it supersedes.

## Superseding a decision

When implementation evidence conflicts with an accepted assumption, write a
new ADR that identifies the evidence, the old decision, compatibility and
migration impact, and the updated choice. Mark the old record with a prominent
link to its successor, but retain its original rationale.

Any change that would weaken a user-visible guarantee must also update the
acceptance criteria and traceability map and receive explicit review before the
implementation changes. An ADR documents approval; it does not silently grant
permission for release, deployment, credential use, or remote mutation.

## Decision index

- [0001 — Build a companion application](0001-build-a-companion-application.md)
- [0002 — Let Codex own profile authentication](0002-let-codex-own-profile-authentication.md)
- [0003 — Separate safe and exact continuation](0003-separate-safe-and-exact-continuation.md)
- [0004 — Use a Go core with an embedded web UI](0004-use-a-go-core-with-an-embedded-web-ui.md)
- [0005 — Keep the MVP loopback-only](0005-keep-the-mvp-loopback-only.md)
- [0006 — Support isolated and experimental shared state modes](0006-support-two-state-modes.md)
- [0007 — Make remote telemetry opt-in](0007-make-remote-telemetry-opt-in.md)
- [0008 — Use a tiered encrypted credential vault](0008-use-a-tiered-encrypted-credential-vault.md)
- [0009 — Make Safe Continuation repository-first](0009-make-safe-continuation-repository-first.md)
- [0010 — Share declarative configuration by projection](0010-share-declarative-configuration-by-projection.md)
- [0011 — Gate handoffs before capacity ranking](0011-gate-handoffs-before-capacity-ranking.md)
- [0012 — Make background collection explicit and privacy-first](0012-make-background-collection-explicit-and-privacy-first.md)
- [0013 — Remain a Codex companion](0013-remain-a-codex-companion.md)
- [0014 — Use only supported read-only data sources](0014-use-only-supported-read-only-data-sources.md)
- [0015 — Authorize the loopback dashboard](0015-authorize-the-loopback-dashboard.md)
- [0016 — Run one service per operating-system user](0016-run-one-service-per-os-user.md)
- [0017 — Store only allowlisted normalized metrics](0017-store-only-allowlisted-normalized-metrics.md)
- [0018 — Keep Project Identity app-local](0018-keep-project-identity-app-local.md)
- [0019 — Use transparent foreground Codex launches](0019-use-transparent-foreground-codex-launches.md)
- [0020 — Bundle experimental compatibility rules with releases](0020-bundle-experimental-compatibility-rules-with-releases.md)
- [0021 — Separate Selected Profile from running launches](0021-separate-selected-profile-from-running-launches.md)
- [0022 — Distinguish managed and referenced Identity Homes](0022-distinguish-managed-and-referenced-identity-homes.md)
- [0023 — Use native unprivileged user services](0023-use-native-unprivileged-user-services.md)
- [0024 — Ship archive-first native releases](0024-ship-archive-first-native-releases.md)
- [0025 — Encrypt sensitive SQLite fields through the vault](0025-encrypt-sensitive-sqlite-fields-through-the-vault.md)
- [0026 — Ship an offline embedded dashboard](0026-ship-an-offline-embedded-dashboard.md)
- [0027 — Use a modular monolith with deep external seams](0027-use-a-modular-monolith-with-deep-external-seams.md)
- [0028 — Use reauthentication instead of portable credential backups](0028-use-reauthentication-instead-of-portable-credential-backups.md)
- [0029 — Preserve and recover SQLite instead of silently resetting](0029-preserve-and-recover-sqlite-instead-of-silently-resetting.md)
- [0030 — Make onboarding resumable and fail closed](0030-make-onboarding-resumable-and-fail-closed.md)
- [0031 — Make Signal Rail comps the frontend acceptance reference](0031-make-signal-rail-comps-the-frontend-acceptance-reference.md)
- [0032 — License under Apache-2.0 with DCO](0032-license-under-apache-2-0-with-dco.md)
- [0033 — Export continuation checkpoints encrypted by default](0033-export-continuation-checkpoints-encrypted-by-default.md)
- [0034 — Keep experiments off the stable-core critical path](0034-keep-experiments-off-the-stable-core-critical-path.md)
