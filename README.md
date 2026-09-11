# VenkataSudha CodexFolio

A local-first CLI companion for choosing Codex profiles, inspecting usage
evidence, and preparing repository checkpoints for interrupted work.

**Development preview.** Core CLI milestones are implemented. The local
dashboard, operational companion work, and production release qualification
are still pending. Making this repository public is not a stable release.
CodexFolio is independent and is not affiliated with or endorsed by OpenAI.

## What is available

| Capability | Current position |
| --- | --- |
| Isolated identity profiles and installed-Codex launch | Implemented; real-account platform qualification has documented exceptions |
| Read-only usage evidence and local analytics | Implemented; provider-reported, derived, estimated, stale, and unavailable values must remain distinguishable |
| Repository-first Safe Continuation | Implemented checkpoint workflow; not a guarantee of exact conversation or hidden-state transfer |
| Local dashboard and background operations | Authorized Overview, Profiles onboarding, and terminal-owned Project Identity launches are implemented; other operational destinations remain in development |
| Exact Continuation and Shared Work Home | Experimental scope; not prerequisites for stable-core delivery |
| Signed, supported end-user release | Not available yet |

This summary reflects the milestone record reviewed on 2026-09-10. Follow the
[MVP roadmap](ROADMAP.md) and [issue #9](https://github.com/kanakamedala-rajesh/codex-folio/issues/9)
for live status. Read the [compatibility qualifications](docs/user/COMPATIBILITY.md)
before using development builds with real accounts.

## Try the development CLI

There is no supported binary installation channel yet. Developers can build
from source using the pinned toolchain and canonical verification command:

```sh
node scripts/verify.mjs
./build/bin/codex-folio version --json
./build/bin/codex-folio --help
```

On Windows, use `.\build\bin\codex-folio.exe` for the executable commands.
The [getting-started guide](docs/user/GETTING-STARTED.md) explains prerequisites,
profile setup, expected boundaries, and how to avoid changing existing Codex
state accidentally. A source build is for evaluation, not production assurance.

New testers could follow the short [Usage guide](docs/user/USAGE-GUIDE.md) as a
first-run tutorial. The [Comprehensive usage guide](docs/user/COMPREHENSIVE-USAGE-GUIDE.md)
covers the complete implemented command set, dashboard behavior, lifecycle
rules, technical boundaries, and troubleshooting.

## Safety and privacy

CodexFolio complements your installed Codex executable; it is not a replacement
Codex client or a way to bypass account, workspace, or billing restrictions.
Only use identities and repositories you are authorized to access.

Read [privacy and data handling](docs/user/PRIVACY.md), particularly retention,
local exports, and the boundary between repository checkpoints and optional
transcript-assisted recovery. Local-first does not mean that the installed
Codex application cannot contact its provider or consume your account usage.

Never attach authentication files, complete Identity Homes, private transcripts,
or unreviewed diagnostics to public issues. Report vulnerabilities through
[Security](SECURITY.md), not public bug reports.

## Feedback and contribution

Use [GitHub issues](https://github.com/kanakamedala-rajesh/codex-folio/issues)
for reproducible, sanitized bugs and proposed improvements. Include the source
revision, platform, installed Codex version, and expected versus actual result.
Start with [Contributing](CONTRIBUTING.md) before changing implementation scope.

## Project documents

The [domain glossary](CONTEXT.md), [product contract](docs/product/PRODUCT.md),
[implementation plan](docs/product/IMPLEMENTATION-PLAN.md),
[acceptance criteria](docs/product/ACCEPTANCE-CRITERIA.md),
[architecture](docs/architecture/ARCHITECTURE.md), and
[architecture decisions](docs/adr/) explain the intended design. They include
future scope and should not be interpreted as a list of shipped features.

The [build guide](docs/development/BUILDING.md) covers contributor tooling.
[Support](SUPPORT.md) describes preview support. Maintainers should use the
[publication checklist](docs/release/PUBLICATION-CHECKLIST.md) before exposing
history and the [first-release checklist](docs/release/FIRST-RELEASE-CHECKLIST.md)
before distributing qualified binaries.

## License

Source and documentation are licensed under [Apache-2.0](LICENSE).
The license does not grant rights to the VenkataSudha CodexFolio name or
branding; see [NOTICE](NOTICE). Contributions require DCO sign-off and follow
[the code of conduct](CODE_OF_CONDUCT.md).
