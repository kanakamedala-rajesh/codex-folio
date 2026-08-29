# Architecture index

The [system architecture](ARCHITECTURE.md) defines CodexFolio's modular
monolith, dependency direction, feature-owned deep modules, external seams,
state-owner protocol, browser boundary, persistence, and verification model.

Supporting authorities:

- [`CONTEXT.md`](../../CONTEXT.md) — canonical domain vocabulary.
- [`docs/adr/`](../adr/README.md) — accepted decisions and the ADR workflow.
- [Product contract](../product/PRODUCT.md) — user-visible scope and boundaries.
- [Acceptance criteria](../product/ACCEPTANCE-CRITERIA.md) — evidence contract.
- [Dependency policy](../development/DEPENDENCIES.md) — dependency direction,
  review, licensing, and reproducibility obligations.
- [Compatibility policy](../development/COMPATIBILITY.md) — independent product,
  API, and persisted-schema versioning.
- [Stable error codes](../development/ERROR-CODES.md) — identifier ownership and
  lifecycle rules.
- [CLI conventions](../development/CLI-CONVENTIONS.md) — exit status and output
  stream contracts.

Implementation that would weaken a user-visible guarantee must not silently
edit architecture prose. Record or supersede the governing ADR, update affected
acceptance mappings, and obtain review before changing the behavior.
