# Dependency review policy

CodexFolio aims to remain a small, inspectable single executable with pinned,
offline-capable frontend assets. A new production dependency is exceptional,
not a default implementation shortcut.

## Required review

Every proposed production dependency must document:

1. the concrete production need and why existing platform or standard-library
   capabilities are insufficient;
2. the license and redistribution obligations, including transitive licenses;
3. maintenance health, ownership, release cadence, vulnerability response, and
   signs of abandonment;
4. security and privacy impact, including network, filesystem, process,
   credential, build-script, and generated-code behavior;
5. binary, frontend, build-time, and support cost; and
6. the alternatives considered and an exit or replacement strategy.

Reviewers must reject a dependency whose license is incompatible with
Apache-2.0 distribution, whose maintenance risk is unacceptable, or whose
behavior weakens an accepted privacy or security boundary. A consequential
tradeoff requires an ADR.

## Reproducibility

Pin direct dependency and tool versions using the repository's established
mechanism. Commit every applicable lockfile update in the same change and
explain unexpected transitive changes. Generated dependency outputs must be
deterministic and pass drift checks. CI caches may improve speed but never
replace or modify authoritative lock state.

Development-only dependencies receive the same license and maintenance review
when they execute code in CI, generate committed artifacts, or affect the
release supply chain. GitHub Actions and other automation dependencies should
be pinned to a reviewed immutable revision where practical.

Accepted CI dependencies and their immutable revisions are recorded in the
[automation dependency reviews](AUTOMATION-DEPENDENCIES.md).

## Reviewed Go runtime dependency

Issue #13 adds `modernc.org/sqlite` at `v1.57.0` because the foundation needs a
cross-platform SQLite adapter without cgo or a native SQLite library. The
driver and its transitive modules are pinned in `go.mod`; their reviewed
licenses are recorded in
[`GO-DEPENDENCY-LICENSES.json`](GO-DEPENDENCY-LICENSES.json). The licenses are
BSD-3-Clause or MIT and are compatible with Apache-2.0 distribution.

The driver is a maintained pure-Go implementation and runs no network,
credential, or provider code. It adds compile and archive size cost, but keeps
the native build contract available on Linux, Windows, and macOS. A future
replacement must preserve the pure-Go, cgo-free contract and update the
license review and release inventory together.

## Reviewed Go cryptography dependency

Issue #17 adds `golang.org/x/crypto` at `v0.41.0` for its maintained Argon2id
implementation. The headless Linux vault needs a memory-hard password-based
key derivation function; the Go standard library does not provide one, and a
local implementation would add security and maintenance risk. The module is
BSD-3-Clause licensed and is compatible with Apache-2.0 distribution. It runs
locally without network or credential access and is used only to derive the
passphrase wrapping key. Its version and checksum are pinned in `go.mod` and
`go.sum`, and its license is included in the release inventory. The upstream
Go project maintains the module as part of the Go cryptography subrepository;
upgrades remain an explicit security review because KDF behavior affects the
on-disk vault format. The dependency adds only the Argon2id implementation to
the native binary and has no frontend, build-script, or generated-code role.
Alternatives considered were a standard-library KDF (none is memory-hard) and
a local implementation (rejected for security and maintenance risk). A future
replacement must preserve the versioned vault format or provide an explicit
migration.

## Linux Secret Service runtime

The Linux desktop adapter invokes the distro-provided `secret-tool` client,
which talks to the current user's Secret Service collection. It passes the
protected record over standard input and never places key material in command
arguments or environment variables. If the client, user session, collection,
or unlock capability is unavailable, the adapter returns a stable vault error;
the composition root requires explicit passphrase mode for WSL/headless use.
Native Secret Service tests run when the client and session are available and
record the unavailable/locked result otherwise. This keeps cross-compilation
and desktop-facility availability separate from runtime qualification.

## Removal and inventory

Remove unused dependencies and their lock entries in the same change that
makes them unnecessary. Release tooling will build a deterministic license
inventory from the reviewed lock state; adding a dependency without usable
license metadata blocks release qualification.
