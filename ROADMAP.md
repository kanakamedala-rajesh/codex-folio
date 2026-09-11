# MVP roadmap

GitHub issue #9 is the canonical [MVP Roadmap and coordination
index](https://github.com/kanakamedala-rajesh/codex-folio/issues/9). It records
the current milestone specification, approved executable tickets, status, and
accepted exit-gate evidence.

The roadmap follows the risk-ordered sequence in the
[implementation plan](docs/product/IMPLEMENTATION-PLAN.md): Phase 0, secure
local foundation, profiles and launch, supported analytics, Safe Continuation,
dashboard and operations, isolated experiments, then production hardening.
Experiments do not block stable-core delivery.

GitHub is authoritative for live delivery status. The committed
[product contract](docs/product/PRODUCT.md), [acceptance evidence
contract](docs/product/ACCEPTANCE-CRITERIA.md),
[architecture](docs/architecture/README.md), and [ADRs](docs/adr/README.md)
remain authoritative for requirements and decisions; the roadmap does not
replace them.

A milestone is complete only after its exit evidence is accepted and recorded,
not merely when its implementation tickets close. Later specifications and
tickets are created just in time after preceding evidence makes their
interfaces concrete.

## Development preview status

Snapshot reviewed on 2026-09-10, with source at `5c6ca1d`:

| Work | Recorded status |
| --- | --- |
| Phase 0 and Milestone 1, secure foundation | Complete |
| Milestone 2, profiles and launch | Complete with an accepted live-authentication qualification exception |
| Milestone 3, supported collection and analytics | Complete |
| Milestone 4, Safe Continuation | Complete with an accepted production-authenticated continuation exception |
| Milestone 5, dashboard and operations | Next specification frontier |
| Milestone 6, isolated experiments | Pending; outside the stable-core critical path |
| Milestone 7, production hardening | Pending |

These are milestone statuses, not a weighted percentage or an end-user support
claim. GitHub issue #9 remains authoritative for changes after this snapshot.
Publishing the source and distributing a supported binary are separate gates.

## Milestone 1 historical evidence

The implementation and native Tier 1 exit-gate evidence are recorded in
[`docs/product/MILESTONE-1-EVIDENCE.md`](docs/product/MILESTONE-1-EVIDENCE.md).
Live acceptance remains tracked by [specification #10](https://github.com/kanakamedala-rajesh/codex-folio/issues/10)
and [roadmap #9](https://github.com/kanakamedala-rajesh/codex-folio/issues/9).
The scope ends at the secure local foundation; all profile, launch, collection,
continuation, production dashboard, telemetry, and release behavior remains
outside this milestone.
