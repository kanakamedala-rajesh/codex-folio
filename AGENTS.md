# AGENTS.md

## Agent skills

### Issue tracker

Issues are tracked in this repository's GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default five-label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses the single-context layout. See `docs/agents/domain.md`.

### Agent handoff contract

Before spawning a review, fixer, delivery, or milestone agent, read `docs/agents/review-contract.md` and supply its role-specific inputs. These four roles are leaf agents. Keep at most one writer on the ticket's files and freeze writers while review is running. The primary agent owns the two-round remediation budget and final verification.

### Verification

`node scripts/verify.mjs` is the canonical repository verification workflow.

Use pinned toolchains. Before committing, run the checks required for the candidate and identify their exact working-tree content. A dirty candidate can be tested, but its HEAD alone does not identify the tested changes. The pre-commit review consumes this evidence; read-only reviewers do not run the writing verifier.

After the reviewed candidate is committed, the primary agent must run the canonical verifier on that exact clean commit before final completion or delivery. It installs the locked frontend dependencies, runs every Phase 0 check used by CI, and must leave tracked source unchanged. Do not relabel a pre-commit run as verification of a later commit.

See `docs/development/BUILDING.md` for focused checks, expected output, and qualification limits.

### Usage guide maintenance

When a major change alters shipped capabilities, first-run onboarding, commands, prerequisites, safety boundaries, or qualification status, update both `docs/user/USAGE-GUIDE.md` and `docs/user/COMPREHENSIVE-USAGE-GUIDE.md` in the same ticket. Keep the short guide limited to the clearest first-success path and put technical behavior, advanced workflows, and troubleshooting in the comprehensive guide. Update the README capability summary when project progress changes it, and verify documented commands against the implementation and `--help` output.

### Cross-platform compatibility

Write portable-by-default code for Windows and Unix-like systems.

For any path, filesystem, executable, process, shell, environment, or signal change:

* use `filepath` for local paths and `filepath.SplitList` or `os.PathListSeparator` for path lists; normalize paths before comparison and keep displayed paths native;
* treat executable extensions, permission bits, line endings, case sensitivity, home and temporary directories, environment variable names, shell quoting, signals, and exit status as platform-specific behavior;
* use `os/exec` with argument slices and platform-aware fixtures; keep shell scripts and shell-specific commands behind explicit operating-system branches;
* make tests runnable on both Windows and Unix, using native fixture formats such as `.cmd`/`.bat` and POSIX scripts, while asserting semantic values rather than host-specific line endings;
* run the canonical verifier for boundary changes and record any native-runtime or compile-only limitation precisely.

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
* explicit mode `full`, the ticket base SHA, candidate HEAD, and a content identity covering staged, unstaged, and relevant untracked files;
* the complete ticket delta, including any already committed ticket work;
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
   * the required behavioral outcome;
   * the candidate content identity, affected-file ownership, and remediation round 1 or 2.
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

Ask `codexfolio_reviewer` with explicit mode `confirmation`, the previously accepted blocker IDs, prior/current content identities, remediation delta, and remediation round. Do not request another full review.

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
2. required pre-commit verification has passed;
3. the ticket review gate has passed;
4. accepted blocker remediation, if any, has passed confirmation.

Do not include unrelated changes in the ticket commit. Verify that the committed tree matches the reviewed candidate, then complete exact-commit canonical verification. Unexpected source changes invalidate affected review evidence; do not silently reuse the prior PASS.

### Post-commit delivery

When an executable-ticket implementation is committed and the user explicitly asks to push it and verify the hosted pipeline, spawn the `codexfolio_delivery` custom agent exactly once.

Give the delivery agent:

* the executable ticket number and parent specification number;
* the exact commit SHA, current branch, remote, and intended base branch;
* the ticket-review result and canonical local-verification evidence;
* confirmation of the user's push and pipeline-verification authorization and the PR/comment/closure actions delegated by repository policy;
* the reviewed acceptance evidence index, authorized unpublished commit range, expected checks, and a bounded polling deadline or attempt budget.

The delivery agent owns only the authorized push, any pull request required to trigger repository checks, pipeline observation bound to the exact source commit and intended base, and successful GitHub ticket bookkeeping. It must not change files, create or amend commits, repair failures, merge a pull request, or mark a milestone complete.

For PR workflows, record the source head, base, and actual tested checkout SHA. A synthetic merge checkout is acceptable only when its relationship to that exact head and base is proven; label it as merge-result evidence, not a direct head checkout.

If the pipeline fails or does not produce evidence bound to the authorized head and base, leave the executable ticket open and report the failure to the primary agent. Do not begin corrective implementation without a separate user request.

### Milestone completion review

Do not declare a parent milestone specification complete solely because all child tickets are closed.

When the final executable child is reviewed, committed, and verified, and required delivery/exit evidence is available, spawn `codexfolio_milestone_reviewer` with the parent specification, exact candidate commit, child set, and evidence index. A partial audit cannot authorize milestone completion.

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

When the governing requirement explicitly permits a qualification exception, an
exact owner-approved exception may satisfy completion as `EXCEPTED`. Keep that
classification separate from `VERIFIED`: it is completion evidence for the
approved boundary, not proof that the waived behavior was exercised. The
milestone reviewer must report evidence coverage separately from completion
coverage. A broad or inferred waiver, a reviewer-granted exception, an exception
the governing contract does not permit, or an exception used to conceal missing
implementation, a security/privacy defect, or a failed required check remains an
incomplete requirement.

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
* the milestone reviewer reports `MILESTONE PASS - 100%` completion, with every
  requirement classified `VERIFIED` or validly `EXCEPTED` and no `PARTIAL`,
  `MISSING`, or `UNKNOWN` requirement;
* required exit-gate evidence has been recorded;
* canonical repository verification has passed where required;
* no unresolved milestone-level blocker remains.

Evidence coverage may be below 100% when valid `EXCEPTED` qualifications exist.
Record their exact scope and approval in the exit evidence, preserve them as
unvalidated, and never relabel them as manual, native, live-provider, security,
or release passes.

After milestone completion:

1. record the accepted exit-gate evidence on the milestone specification;
2. update issue #9 with the specification, ticket set, evidence, and status;
3. proceed to the next milestone only according to the delivery roadmap.

Do not let milestone review expand the accepted specification or bypass ticket decomposition.
