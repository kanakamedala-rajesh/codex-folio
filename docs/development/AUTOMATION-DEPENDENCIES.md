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

## `actions/setup-go`

- **Production need:** the repository verification job needs the Go version
  pinned by `go.mod` and a Go build cache keyed by that module state.
- **Reviewed revision:** `924ae3a1cded613372ab5595356fb5720e22ba16`
  (`v6` as resolved on 2026-08-29), pinned immutably in the workflow.
- **License:** MIT, compatible with this repository's Apache-2.0 distribution.
  The pinned lockfile and checked-in `.licenses` inventory cover transitive
  0BSD, Apache-2.0, BSD, BlueOak-1.0.0, CC-BY-4.0, CC0-1.0, ISC, MIT, and
  Python-2.0 terms. They impose no incompatible reciprocal obligation; the
  action runs only in CI and is not redistributed with CodexFolio.
- **Maintenance:** maintained in the active `actions/setup-go` repository under
  the GitHub Actions organization. At review time the repository was not
  archived, had activity on 2026-08-19, and had published six releases from
  2025-12-16 through 2026-07-16. The repository had no published security
  advisories; the organization directs vulnerability reports to GitHub's
  HackerOne security bounty, providing a private response path.
- **Security and privacy:** the action installs the declared public Go
  toolchain and caches compiler data from the checked-in module state. The job
  grants only `contents: read` and receives no application secrets.
- **Cost:** one JavaScript action plus the pinned toolchain and build cache; it
  adds no runtime dependency to CodexFolio.
- **Alternatives:** manually downloading and caching Go would duplicate version,
  integrity, and cache handling in repository-specific shell code.
- **Exit strategy:** replace it with a reviewed GitHub-supported successor or a
  preinstalled pinned toolchain if the action becomes unavailable.

## `actions/setup-node`

- **Production need:** the repository verification job needs the Node.js version
  pinned by `.nvmrc` and an npm cache keyed by `web/package-lock.json`.
- **Reviewed revision:** `249970729cb0ef3589644e2896645e5dc5ba9c38`
  (`v6` as resolved on 2026-08-29), pinned immutably in the workflow.
- **License:** MIT, compatible with this repository's Apache-2.0 distribution.
  The pinned lockfile and checked-in `.licenses` inventory cover transitive
  0BSD, Apache-2.0, BSD, CC-BY-4.0, CC0-1.0, ISC, MIT, and Python-2.0 terms.
  They impose no incompatible reciprocal obligation; the action runs only in
  CI and is not redistributed with CodexFolio.
- **Maintenance:** maintained in the active `actions/setup-node` repository
  under the GitHub Actions organization. At review time the repository was not
  archived, had activity on 2026-08-25, and had published six releases from
  2025-12-03 through 2026-07-14. The repository had no published security
  advisories; the organization directs vulnerability reports to GitHub's
  HackerOne security bounty, providing a private response path.
- **Security and privacy:** the action installs the declared public Node.js
  toolchain and caches npm download data from the checked-in lockfile. The job
  grants only `contents: read` and receives no application secrets.
- **Cost:** one JavaScript action plus the pinned toolchain and package cache; it
  adds no browser or executable runtime dependency.
- **Alternatives:** manually downloading Node.js and caching npm would duplicate
  version, integrity, and cache handling in repository-specific shell code.
- **Exit strategy:** replace it with a reviewed GitHub-supported successor or a
  preinstalled pinned toolchain if the action becomes unavailable.

Review evidence was obtained from the official
[`actions/setup-go`](https://github.com/actions/setup-go) and
[`actions/setup-node`](https://github.com/actions/setup-node) repository
metadata and their declared licenses.
