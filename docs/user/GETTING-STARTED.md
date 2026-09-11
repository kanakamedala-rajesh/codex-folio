# Getting started with the development CLI

This is a source-build preview, not a supported end-user release. The current
version family is `0.0.1-alpha`. There is no published binary channel to install
from at the reviewed baseline. Do not substitute an unverified third-party
package with a similar name.

## Prerequisites

Use the pinned Go, Node.js, and npm versions in the [build guide](../development/BUILDING.md).
Install Codex separately through its official distribution. CodexFolio launches
that executable and delegates authentication to it. Do not give passwords or
API keys to an issue form or a website claiming to configure CodexFolio.

Before adding a real account, read the [compatibility qualifications](COMPATIBILITY.md)
and [privacy notes](PRIVACY.md). Start with an account and a non-sensitive test
repository that you are authorized to use. Back up existing local state through
a trusted, private process. Do not put credential backups inside this repository.

## Build and inspect

From a clean checkout:

```sh
node scripts/verify.mjs
./build/bin/codex-folio version --json
./build/bin/codex-folio --help
./build/bin/codex-folio codex discover --json
```

On Windows, use `.\build\bin\codex-folio.exe` in place of
`./build/bin/codex-folio`. The verification command installs the locked frontend
dependencies, runs the repository gates, and creates the native development
binary. A successful build is not evidence that live authentication has been
qualified on your platform.

The version JSON identifies the revision, build class, and working-tree state.
Codex discovery should identify an installed executable or return a diagnostic.
Record those versions when reporting a compatibility issue. Do not claim support
for an untested Codex version simply because discovery finds it.

## Add an isolated profile and inspect usage

These commands are taken from the implemented CLI contract. They have not been
independently live-account-qualified by the public-readiness review:

```sh
./build/bin/codex-folio profile add personal --browser
./build/bin/codex-folio profile list --json
./build/bin/codex-folio usage refresh personal --json
./build/bin/codex-folio usage show --json
```

The installed Codex application owns the sign-in flow. The alias `personal` is
only an example. Do not point an Identity Home at an unrelated existing directory
without understanding the ownership and import behavior. Use the documented
platform vault mode when required. On WSL or headless Linux, start the owner with
`./build/bin/codex-folio service start --vault-mode passphrase` and keep it
running while using other commands. Passphrase input currently has no prompt
and remains visible while typed, so do not use a shared or recorded terminal or
reuse another password; see the [Usage guide](USAGE-GUIDE.md). A missing Linux
Secret Service is not evidence that the application should silently store
credentials without protection.

Unavailable provider data is not zero usage. Check provenance, availability, and
observation time before interpreting a reading. Do not calculate subscription
credit balances from token estimates without a documented provider conversion.

## Launch deliberately

Run this only when you intend to start Codex under the chosen profile:

```sh
./build/bin/codex-folio launch personal --
```

CodexFolio does not make installed Codex usage free. Actions you take in that
process may use the account's included allowance or enabled paid usage. The
companion is not a billing-control guarantee or a method of evading restrictions.

For interrupted work, inspect `checkpoint` and `handoff` in CLI help. Safe
Continuation prepares repository context for a new installed Codex process.
It does not promise to transfer every conversation turn, hidden model state,
or permissions from another workspace. Review any material before sending it
to a different identity or workspace.

## Stop, remove, or report a problem

Do not use recursive deletion on your normal Codex home. Profile removal,
restoration, and purge are distinct local operations with documented confirmation
and quarantine behavior. They do not delete your remote OpenAI account. Inspect
the command help and preview before using a destructive operation.

The preview does not promise arbitrary downgrade compatibility. Preserve a
private backup before testing migrations. A future binary installer must document
uninstall, shell-wrapper removal, and optional local-state deletion separately.

Use [Support](../../SUPPORT.md) for non-sensitive issues and
[Security](../../SECURITY.md) for vulnerabilities. Provide a minimal synthetic
reproduction, not a complete Codex home, session archive, or authentication file.
