# Automation dependency reviews

This inventory records third-party automation that executes in CI. Reviews
follow the [dependency policy](DEPENDENCIES.md); an immutable revision change
requires a new review in the same pull request.

## `actions/checkout`

- **Production need:** the DCO pull-request job needs the proposed Git history,
  including base and head commits, so the repository-owned checker can validate
  every commit in the range. GitHub-hosted jobs do not contain that checkout by
  default.
- **Reviewed revision:** `11d5960a326750d5838078e36cf38b85af677262`
  (`v4` as resolved on 2026-08-29), pinned immutably in the workflow.
- **License:** MIT, compatible with this repository's Apache-2.0 distribution.
  The action runs in CI and is not redistributed in CodexFolio artifacts.
- **Maintenance:** maintained in the active `actions/checkout` repository under
  the GitHub Actions organization. At review time the repository was not
  archived and had activity on 2026-08-10. Re-review activity and security
  advisories before updating the pin.
- **Security and privacy:** the job grants only `contents: read`, checks out the
  pull-request history, persists the default job token only for the job, and
  sends no CodexFolio application data or credentials. No application secret is
  available to the pull-request workflow.
- **Cost:** one JavaScript action and a full history fetch in the small DCO job;
  it adds no binary or frontend runtime weight.
- **Alternatives:** manually reproducing authenticated checkout logic would
  increase credential and maintenance risk; a hosted DCO app would add an
  external service and split behavior from the local checker.
- **Exit strategy:** replace the action with a reviewed GitHub-supported
  successor or a platform-provided checkout if this repository becomes
  unavailable or unmaintained.

Review evidence was obtained from the official
[`actions/checkout`](https://github.com/actions/checkout) repository metadata
and its declared license.
