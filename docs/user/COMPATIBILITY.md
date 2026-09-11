# Compatibility and qualification

Reviewed baseline: `5c6ca1dae6ec4f58dae2aee89616f100db0dbc37`, 2026-09-10.
This is a recorded evidence boundary, not a blanket supported-platform promise.
The [MVP roadmap](../../ROADMAP.md) and issue #9 contain the live milestone record.

| Target | What the milestone evidence establishes | What remains unqualified |
| --- | --- | --- |
| Linux AMD64 | Native hosted deterministic verification and target compilation | Full production-authenticated account and continuation scenarios |
| Windows AMD64 | Native hosted deterministic verification and target compilation | Full production-authenticated account and continuation scenarios |
| macOS ARM64 | Native hosted deterministic verification, including test Keychain setup, and target compilation | Full production-authenticated account and continuation scenarios |
| WSL2 | Intended target, recorded separately from hosted Linux | Do not infer qualification from Ubuntu CI; explicit WSL2 evidence is required |

The Milestone 2 acceptance record has a real-account launch qualification
exception. The Milestone 4 completion record has a production-authenticated
cross-profile continuation exception. Neither exception disappears when a
repository becomes public or a CI badge turns green.

## Installed Codex versions

This preview does not publish a complete, production-qualified Codex version
range. Maintainers must record the exact installed Codex version and adapter
capabilities for each live test before advertising such a range. Unsupported
or unavailable data must remain explicit; do not guess a compatible version.

## Stability expectations

CLI flags, JSON output, local state, checkpoints, and migrations can change
before their stable compatibility contracts are published. A versioned HTTP API
namespace does not, by itself, make every preview response a stable public API.
Do not build production automation on undocumented fields.

Design mockups are not runtime screenshots. Exact Continuation and Shared Work
Home are experimental scope and must be separately version-gated, reversible,
and disabled unless their own evidence requirements are met.
