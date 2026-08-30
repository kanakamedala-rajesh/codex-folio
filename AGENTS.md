# AGENTS.md

## Agent skills

### Issue tracker

Issues are tracked in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default five-label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses the single-context layout. See `docs/agents/domain.md`.

### Verification

`node scripts/verify.mjs` is the canonical repository verification workflow.

Run it from a clean checkout with the pinned toolchains before claiming a change or milestone is complete. It installs the locked frontend dependencies, runs every Phase 0 check used by CI, and must leave tracked source unchanged.

See `docs/development/BUILDING.md` for focused checks, expected output, and qualification limits.

### Delivery roadmap

GitHub issue #9 is the MVP Roadmap and coordination index.

Before implementation, read:

1. the roadmap;
2. the current milestone specification;
3. the selected executable ticket.

Never implement the roadmap or parent specification as an additional ticket.

The selected executable ticket is the implementation scope boundary. Parent specifications, ADRs, architecture documents, security requirements, privacy requirements, compatibility requirements, and repository instructions constrain that implementation. They do not independently authorize unrelated work.

When completing a milestone:

1. Record its exit-gate evidence on the milestone specification.
2. Update issue #9 with the specification, ticket set, evidence, and status.
3. Create the next milestone specification with `to-spec`.
4. Decompose it with `to-tickets` only after the specification is approved.

Closing every ticket is necessary but does not complete a milestone without accepted exit evidence.

Milestone 6 experiments must not block the stable-core path.

### Executable ticket scope

Treat the selected executable GitHub ticket as the immutable scope boundary during implementation and review.

Before editing, understand:

* the ticket's requested behavior;
* its acceptance criteria;
* applicable requirements from the parent specification;
* relevant ADR and architecture constraints;
* security, privacy, compatibility, and platform requirements;
* the verification required to prove the ticket complete.

Do not expand the ticket because an implementer or reviewer discovers a potentially useful improvement.

Additional work is justified only when it is necessary to satisfy:

* the executable ticket;
* an applicable existing contract;
* correctness;
* security or privacy;
* compatibility;
* required verification;
* a regression introduced by the current implementation.

Otherwise leave it out and report it separately when useful.

### Ticket implementation review

For every executable ticket implemented in this repository:

1. Complete the requested implementation and its tests.
2. Run the narrowest relevant verification.
3. Run broader repository verification when required by the ticket, changed dependency graph, or repository policy.
4. Before committing the completed implementation, spawn the `codexfolio_reviewer` custom agent exactly once for a full ticket review.

Give the reviewer:

* the executable ticket number and contents;
* its applicable parent specification;
* the complete working-tree and staged changes;
* relevant ADR, architecture, security, privacy, compatibility, and platform constraints;
* available verification evidence.

The reviewer must inspect the complete ticket delta, including:

* `git status --short`;
* `git diff HEAD`;
* `git diff --cached`;
* relevant untracked files.

The reviewer is read-only and must not edit source files, Git state, GitHub issues, commits, or remote state.

### Review findings

The `codexfolio_reviewer` may classify observations as:

* `BLOCKER`
* `ADVISORY`

A `BLOCKER` is actionable only when it demonstrates a violation of an existing requirement.

Every actionable blocker must map to at least one concrete source:

* executable-ticket acceptance criterion;
* applicable parent-specification requirement;
* existing ADR or architecture constraint;
* existing security, privacy, compatibility, or platform requirement;
* regression introduced by the current ticket implementation;
* required test or verification failure.

Reviewer comments are not new requirements.

Do not implement `ADVISORY` findings as part of the current ticket.

Do not turn reviewer preferences, alternative designs, speculative hardening, cleanup opportunities, hypothetical edge cases, or unrelated pre-existing defects into ticket work.

### Review remediation

When `codexfolio_reviewer` reports one or more valid `BLOCKER` findings:

1. Keep the original executable ticket as the source of truth.
2. Validate that each proposed blocker maps to an existing governing requirement.
3. Reject or defer findings that do not meet the blocker criteria.
4. Remediate only accepted blocker IDs.
5. Prefer one blocker or one tightly related blocker group at a time.
6. Spawn `codexfolio_fixer` for the remediation instead of reopening unrestricted implementation.
7. Give the fixer only:

   * the executable ticket;
   * the accepted blocker ID or IDs;
   * the violated contract;
   * concise evidence;
   * the required behavioral outcome.
8. Do not pass unrelated reviewer discussion or advisories to the fixer.
9. Make the smallest coherent change needed to resolve the accepted blocker.
10. Run the narrowest verification that proves the blocker correction.
11. Run broader repository verification only when required by repository policy or the changed dependency graph.

During remediation, do not:

* reinterpret or reimplement the whole ticket;
* redesign surrounding architecture;
* perform unrelated refactoring;
* add speculative abstractions;
* add generalized utilities;
* add unrelated validation or fallback behavior;
* add speculative security hardening;
* expand documentation without a current requirement;
* broaden test coverage beyond what proves the blocker correction;
* modify unrelated files.

The primary implementation agent remains responsible for integrating the fix and for final verification.

### Confirmation review

After remediation, do not run another unrestricted ticket review.

Ask `codexfolio_reviewer` for a confirmation review of only the previously accepted blocker IDs.

The confirmation review must answer whether each accepted blocker is:

* `RESOLVED`
* `UNRESOLVED`

It must not restart general review, search for new improvements, or reinterpret the executable ticket.

A new blocker may be introduced during confirmation only when the remediation itself caused a direct regression of the executable ticket or an existing governing contract.

Allow at most two remediation rounds for a ticket.

If an accepted blocker remains unresolved after two remediation rounds:

1. stop autonomous editing;
2. report the unresolved blocker and available evidence;
3. do not continue a review-fix-review loop.

### Ticket completion

A ticket is complete when:

* its acceptance criteria are satisfied;
* required verification passes;
* no accepted reviewer `BLOCKER` remains unresolved.

`REVIEW PASS` or `CONFIRMATION PASS` is evidence for the review gate. It is not authorization for cleanup, refactoring, additional features, broader testing, documentation expansion, or architectural improvement.

Once the executable ticket is complete, stop.

Do not push as part of the implementation or review workflow unless the user explicitly requests a push.

### Commit boundary

Commit only after:

1. the executable ticket implementation is complete;
2. required verification has passed;
3. the ticket review gate has passed;
4. accepted blocker remediation, if any, has passed confirmation.

Do not include unrelated changes in the ticket commit.

### Milestone completion review

Do not declare a parent milestone specification complete solely because all child tickets are closed.

When the final executable child ticket of a milestone has completed its ticket review gate, spawn `codexfolio_milestone_reviewer` against the parent specification.

The milestone reviewer is a read-only auditor.

Its purpose is to determine whether the already-accepted milestone specification has been satisfied by the completed ticket set and recorded evidence.

It must not directly trigger implementation changes.

The milestone reviewer should trace:

* parent specification requirements;
* executable child tickets;
* implementation evidence;
* automated tests;
* manual or platform evidence where required;
* milestone exit-gate evidence.

Issue state, checked boxes, implementation summaries, or comments claiming success are pointers to evidence, not proof by themselves.

### Milestone gaps

If `codexfolio_milestone_reviewer` reports an incomplete requirement:

1. identify the exact parent-specification requirement;
2. determine whether an existing executable ticket was left incomplete;
3. if so, reopen or continue that ticket through the normal ticket workflow;
4. if the accepted specification requires work that was never decomposed into an executable ticket, create a new bounded executable ticket before implementation;
5. do not ask the milestone reviewer to implement or fix the gap directly.

All implementation must continue through executable tickets.

The milestone reviewer must not become an alternate source of implementation scope.

### Milestone completion gate

A milestone may be marked complete only when:

* all executable child tickets are complete;
* the milestone reviewer reports `MILESTONE PASS - 100%`;
* required exit-gate evidence has been recorded;
* canonical repository verification has passed where required;
* no unresolved milestone-level blocker remains.

After milestone completion:

1. record the accepted exit-gate evidence on the milestone specification;
2. update issue #9 with the specification, ticket set, evidence, and status;
3. proceed to the next milestone only according to the delivery roadmap.

Do not let milestone review expand the accepted specification or bypass ticket decomposition.
