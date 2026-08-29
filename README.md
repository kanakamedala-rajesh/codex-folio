# VenkataSudha CodexFolio

VenkataSudha CodexFolio is a local-first companion for the installed Codex application. It provides Identity Profile launching, read-only usage evidence, a responsive local dashboard, and repository-first Safe Continuation. It is an independent project and is not affiliated with or endorsed by OpenAI.

The project is in Phase 0: the product and architecture baseline has been promoted, but application implementation has not started.

## Project documents

- [`CONTEXT.md`](CONTEXT.md): canonical domain glossary.
- [`docs/adr/`](docs/adr/): accepted architecture decisions.
- [`docs/product/PRODUCT.md`](docs/product/PRODUCT.md): product purpose and boundaries.
- [`docs/product/IMPLEMENTATION-PLAN.md`](docs/product/IMPLEMENTATION-PLAN.md): risk-ordered delivery plan.
- [`docs/product/ACCEPTANCE-CRITERIA.md`](docs/product/ACCEPTANCE-CRITERIA.md): testable evidence contract.
- [`docs/architecture/ARCHITECTURE.md`](docs/architecture/ARCHITECTURE.md): modular-monolith architecture.
- [`docs/design/DASHBOARD-BRIEF.md`](docs/design/DASHBOARD-BRIEF.md): comp-first dashboard direction.
- [`docs/research/`](docs/research/): supporting technical research.
- [`ROADMAP.md`](ROADMAP.md): MVP delivery status and tracker link.
- [`CONTRIBUTING.md`](CONTRIBUTING.md): contribution and DCO requirements.
- [`SECURITY.md`](SECURITY.md): private vulnerability reporting.
- [`SUPPORT.md`](SUPPORT.md): current support boundaries.

## License and community

Source and documentation are licensed under [Apache-2.0](LICENSE). The license
does not grant rights to the VenkataSudha CodexFolio name or branding; see
[`NOTICE`](NOTICE). Contributions require DCO sign-off and follow the
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## Development

The Phase 0 scaffold uses pinned Go 1.27.0, Node.js 24.18.0, and npm 11.16.0
toolchains. See [`docs/development/BUILDING.md`](docs/development/BUILDING.md)
for setup, focused checks, version output, common failures, and the canonical
clean-checkout verification command:

```sh
node scripts/verify.mjs
```
