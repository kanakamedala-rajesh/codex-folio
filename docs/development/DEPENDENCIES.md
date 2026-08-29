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

## Removal and inventory

Remove unused dependencies and their lock entries in the same change that
makes them unnecessary. Release tooling will build a deterministic license
inventory from the reviewed lock state; adding a dependency without usable
license metadata blocks release qualification.
