# First downloadable preview and stable release

Source publication does not require a binary release. Do not create a release
only to make the repository look complete. Keep the current `0.0.1-alpha` family
until the canonical version, OpenAPI metadata, frontend metadata, tests, and
release naming are updated together. A later `v0.0.1-alpha.1` is a proposal, not
an existing distribution channel.

## Downloadable preview gate

| Check | Evidence required |
| --- | --- |
| Build identity | Tag, source revision, clean tree, build class, dependency lock state |
| Supply chain | Checksums, inspectable license inventory, SBOM, accurate signing and provenance status |
| Linux AMD64 | Fresh-machine install and smoke test; native vault/service behavior |
| Windows AMD64 | Fresh-machine install, paths with spaces, executable discovery, process and cleanup behavior |
| macOS ARM64 | Fresh-machine install, Keychain behavior, signing/notarization status stated honestly |
| WSL2 | Separate test of discovery, vault availability, process launch, and applicable filesystem boundaries |
| Identity flows | Real-account setup, reauthentication, launch, remove/restore/purge with authorized test accounts |
| Usage failures | Offline, expired auth, unsupported provider data, stale and unavailable evidence |
| Continuation | Checkpoint consent and review; failure recovery; no promise of exact hidden-state transfer |
| Upgrade/uninstall | Documented migrations, backup, rollback limitations, shell integration cleanup, preservation of unrelated Codex state |
| Privacy | Minimal metadata-only reports, redacted diagnostics, explicit export fields and retention semantics |
| Accessibility | CLI usability and, when shipped, keyboard, screen-reader, zoom, chart alternatives, and reduced-motion evidence |

Download and test the actual archive, not only the build directory. Record the
installed Codex version for each platform test. Do not run real credentials in
ordinary public pull-request CI. Do not advise users to disable OS protections
as a substitute for correct release qualification.

The current `scripts/release.mjs --dry-run` deliberately creates unsigned,
compile-qualified artifacts and a provenance placeholder. A checksum identifies
bytes; it does not establish publisher identity. A placeholder is not an
attestation. Keep those limitations in preview release notes.

## Stable release gate

Retain the accepted production-hardening requirements, including signing,
attestation, applicable notarization, qualified installers, recovery evidence,
compatibility policy, known limitations, and support expectations. Do not remove
the fail-closed stable gate to obtain a green release job. Exact Continuation and
Shared Work Home stay separately experimental and do not hold up stable core.

Schedule dependency and advisory review against the actual Go and npm lock state.
Do not mass-upgrade dependencies as part of a visibility-only change. Review
license obligations against distributed artifacts, including transitive assets.
