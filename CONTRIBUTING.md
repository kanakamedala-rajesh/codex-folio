# Contributing to VenkataSudha CodexFolio

Thank you for contributing. CodexFolio is currently building its risk-ordered
MVP; start with an approved, executable GitHub ticket and preserve the domain
terms in [`CONTEXT.md`](CONTEXT.md).

## Before opening a change

- Discuss unapproved product or architecture changes in an issue first.
- Read the nearest `AGENTS.md`, the selected ticket, its milestone
  specification, and relevant [architecture decisions](docs/adr/README.md).
- Keep changes bounded to the ticket. Security reports follow
  [`SECURITY.md`](SECURITY.md), not the public issue tracker.
- Explain any production dependency using the
  [dependency review policy](docs/development/DEPENDENCIES.md).

## Developer Certificate of Origin

Every commit must certify the [Developer Certificate of Origin 1.1](DCO) with
a `Signed-off-by` trailer matching the commit author's real name and email.
Create it automatically with:

```sh
git commit --signoff
```

No CLA is required or accepted in place of DCO sign-off. The pull-request DCO
check validates every commit in the proposed range.

To correct the newest local commit, run `git commit --amend --signoff`. To fix
several unpublished commits, use an interactive rebase and amend each affected
commit with `git commit --amend --signoff`. Rebase only your own unpublished
work; coordinate with maintainers before rewriting a shared branch.

## Build and verification

Install the pinned toolchains described in
[`docs/development/BUILDING.md`](docs/development/BUILDING.md), then run the
single verification command:

```sh
node scripts/verify.mjs
```

The verification command installs the locked frontend dependencies itself. It
must pass without application secrets and must leave tracked source unchanged.
Include focused tests for behavior changed at an accepted public seam. Do not
bypass a failed check or commit generated drift.

## Pull requests

Describe the ticket, behavior, verification evidence, compatibility impact,
and any remaining limitation. Keep commits reviewable and signed off. By
submitting a contribution, you agree that it is licensed under Apache-2.0 and
that your public contribution record is retained as described by the DCO.

All contributors must follow the [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## A first contribution

Choose a small, approved issue with named files and acceptance criteria. A CLI
help correction, a sanitized example, or a focused regression test is a better
first task than authentication, vault, migration, or checkpoint privacy work.
For a documentation-only fix, begin with the affected guide and the issue;
maintainers can point you to any additional design material that is relevant.

Create a branch, make the bounded change, run the relevant focused checks and
`node scripts/verify.mjs`, then sign off your own commits and open a pull request.
State what you actually tested. Do not claim independent audits or platform
qualification from a generated summary alone.

Your name, email in Git commits and DCO trailers, issue content, and PR comments
are public contribution records. Configure your preferred GitHub no-reply
address before committing when appropriate. Do not rewrite other people's
attribution. Automation-created commits still need an approved provenance and
DCO policy; do not add a blanket bot exemption just to make a check pass.

## Sensitive reports and workflow changes

Never include real credentials, complete Codex home directories, private
transcripts, customer code, or unreviewed diagnostic bundles. Use minimal
synthetic fixtures. Report vulnerabilities through `SECURITY.md`.

Maintainers must inspect changes to workflows and verification scripts before
approving external workflow runs. The DCO workflow uses the base revision's
checker, but a workflow file change still needs human review. No public PR
should be granted production credentials or signing material.
