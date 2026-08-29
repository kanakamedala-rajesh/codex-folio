# Security policy

## Supported versions

CodexFolio is in Phase 0 and has no stable release. No version currently
receives a production security-support guarantee. This policy still applies to
the repository, development artifacts, build tooling, and documentation.

## Report a vulnerability privately

Use GitHub's private vulnerability-reporting form for this repository:

<https://github.com/kanakamedala-rajesh/codex-folio/security/advisories/new>

Do not open a public issue, discussion, or pull request for a suspected
vulnerability. If the private form is unavailable, contact the repository
owner through a private channel and reference this policy without including
exploit details publicly.

Include the affected revision or artifact, impact, reproduction conditions,
and a minimal proof of concept when safe. Remove credentials, Identity Home
contents, tokens, cookies, private keys, personal data, Codex transcripts, and
other sensitive reproduction data. Never send a real secret; use a redacted or
synthetic value.

Maintainers will acknowledge the report, establish a private coordination
channel, assess affected versions, and agree on disclosure timing. Please do
not disclose the issue before that coordination is complete.

## Scope

Security-sensitive areas include credential and Identity Home handling,
loopback authorization, persistence and migration, process launching,
continuation exports, update or telemetry transport, release tooling, and
supply-chain metadata. Product questions and non-sensitive defects belong in
the [public support path](SUPPORT.md).
