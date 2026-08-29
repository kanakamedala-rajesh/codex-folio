## Agent skills

### Issue tracker

Issues are tracked in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default five-label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses the single-context layout. See `docs/agents/domain.md`.

### Verification

`node scripts/verify.mjs` is the canonical repository verification workflow.
Run it from a clean checkout with the pinned toolchains before claiming a
change or milestone is complete. It installs the locked frontend dependencies,
runs every Phase 0 check used by CI, and must leave tracked source unchanged.
See `docs/development/BUILDING.md` for focused checks, expected output, and
qualification limits.

### Delivery roadmap

GitHub issue #9 is the MVP Roadmap and coordination index.

Before implementation, read the roadmap, the current milestone specification, and the selected executable ticket. Never implement the roadmap or parent specification as an additional ticket.

When completing a milestone:

1. Record its exit-gate evidence on the milestone specification.
2. Update issue #9 with the specification, ticket set, evidence, and status.
3. Create the next milestone specification with `to-spec`.
4. Decompose it with `to-tickets` only after the specification is approved.

Closing every ticket is necessary but does not complete a milestone without accepted exit evidence. Milestone 6 experiments must not block the stable-core path.
