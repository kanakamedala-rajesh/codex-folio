# Support

VenkataSudha CodexFolio is an independent open-source project and is not affiliated with or endorsed by OpenAI. The project cannot provide support for
OpenAI accounts, subscriptions, workspaces, billing, quotas, or the installed
Codex application itself.

## Current support level

The repository is a development preview with implemented core CLI workflows,
not a supported end-user release. Best-effort help covers reproducible
CodexFolio problems on the documented development targets. Compilation and
fake-Codex tests do not establish production-authenticated qualification.
See the [compatibility record](docs/user/COMPATIBILITY.md) and
[getting-started guide](docs/user/GETTING-STARTED.md).

Commands, JSON output, local schemas, and migrations may change before a
stable compatibility policy exists. Do not assume that downgrading a binary
can read state written by a later build.

## Where to ask

- Search existing [GitHub issues](https://github.com/kanakamedala-rajesh/codex-folio/issues).
- Open a public issue for reproducible, non-sensitive CodexFolio bugs or
  approved feature discussion. Include the revision, platform, command, and
  sanitized output.
- Follow the [build guide](docs/development/BUILDING.md) for toolchain and
  verification failures.
- Follow [Security](SECURITY.md) for vulnerabilities or any report containing
  sensitive details; private reports go to
  [codexfolio-support@venkatasudha.com](mailto:codexfolio-support@venkatasudha.com).
  Never post credentials or private reproduction data.

The [MVP roadmap](ROADMAP.md) describes delivery status. Planned capabilities
are not available merely because they appear in product documentation.
