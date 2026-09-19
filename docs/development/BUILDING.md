# Build the development preview

Core CLI milestones through Safe Continuation have been implemented. This is
not a stable release. Read [the user guide](../user/GETTING-STARTED.md) and
[qualification limits](../user/COMPATIBILITY.md) before using real identities.
The commands below describe the local service foundation used by those
workflows; individual historical gates retain their original qualifications.
The service resolves platform app-local paths, initializes the versioned
SQLite store, protects sensitive fields through the platform vault boundary,
and can run a foreground owner through the CLI:

```sh
codex-folio service status [--state-root PATH] [--json]
codex-folio service start [--state-root PATH] [--vault-mode MODE] [--json]
codex-folio service install [--state-root PATH] [--vault-mode MODE] [--json]
codex-folio service uninstall [--state-root PATH] [--json]
codex-folio vault unlock [--state-root PATH] [--json]
```

The owner lock, descriptor, SQLite database, vault, recovery artifacts, and
diagnostic aggregates are runtime foundation artifacts. Profile, launch,
collection, analytics, and Safe Continuation CLI workflows now build on this
foundation. The authorized Overview now displays live capacity and scope;
later dashboard journeys and operational work remain pending. The service
is the only composed process path that opens the durable SQLite store or runs
its migrations. A passphrase-backed owner starts locked and publishes safe
health without opening storage; `vault unlock` privately supplies the
passphrase over the command-authenticated loopback transport and activates the
composed workflows once.

## Pinned prerequisites

The supported development toolchain is:

| Tool    | Version   |
| ------- | --------- |
| Go      | `1.27.0`  |
| Node.js | `24.18.0` |
| npm     | `11.16.0` |

The versions are recorded in `.tool-versions`, `.nvmrc`, `go.mod`, and
`web/package.json`. The frontend workspace uses `engine-strict=true` and its
`check:tooling` command fails with an actionable message when the pinned Node
or npm version is unavailable.

## Verify a clean checkout

From the repository root, run the single canonical verification command:

```sh
node scripts/verify.mjs
```

The verifier installs the exact frontend dependency lock state with `npm ci`;
no separate dependency-install command is required. It does not require an
Identity Profile, Codex authentication, application credentials, provider
access, signing material, telemetry infrastructure, or production
configuration.

The versioned browser contract is generated from `api/openapi.json`. The
canonical version value is `internal/buildinfo/version.txt`; the generator
requires the OpenAPI document's required `info.version` declaration to match
that value, while the API namespace remains `v1` and can evolve independently
when compatibility policy requires it. The generator is the repository-owned
`codex-folio` OpenAPI generator at version `1.0.0`; its input and checked-in
outputs are fixed by the script, and it uses the pinned Go toolchain's `gofmt`
for the Go representation.

To regenerate the contract artifacts after changing the source contract:

```sh
node scripts/generate-openapi.mjs
```

The generated Go transport representation is written to
`internal/httpapi/openapi.gen.go`; the generated TypeScript client and types
are written to `web/src/generated/openapi.ts`. Check for drift without
writing files with:

```sh
node scripts/generate-openapi.mjs --check
node --test scripts/generate-openapi.test.mjs
```

The verification command runs the OpenAPI drift check and generator tests;
architecture and stable error-code checks with focused fixture tests; Go
formatting, vet, unit tests, a native development build, and compile-only Linux
AMD64, Windows AMD64, and macOS ARM64 target builds; frontend formatting,
linting, type-checking, tests, and build; a self-contained frontend smoke check;
native browser-to-service smoke, deep Overview journeys with fake Codex,
automated accessibility checks, and a repeated startup benchmark; and the repository governance and local
documentation-link check. It uses the
same checked-in package lock and fails if tracked source changes during
verification. Build output is written under ignored `build/` and `web/dist/`
directories.

Successful output ends with a repository verification summary that identifies each completed
gate with `[PASS]` and each intentionally unavailable qualification with
`[SKIP]`. The expected skips are native runtime qualification for the
compile-only target builds and stable signing, attestation, notarization, and
release eligibility. The verifier executes the native development binary and
checks its JSON version, revision, build classification, and working-tree
identity before reporting the build gate as passed.

The default command runs the complete verification contract. CI partitions that
same contract explicitly:

| Suite                                    | CI placement                            | Coverage                                                                                                                                 |
| ---------------------------------------- | --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `node scripts/verify.mjs --suite native` | Linux AMD64, Windows AMD64, macOS ARM64 | Native Go/CLI/service/vault/process tests, builds and archives, embedded asset build/smoke, and a small real browser-to-service journey. |
| `node scripts/verify.mjs --suite web`    | One `ubuntu-24.04` job                  | Frontend format/lint/type checks, reproducible build/offline checks, deep UI states/layout/accessibility, and the startup benchmark.     |

Both suites retain pinned tools, locked dependencies, generated-contract checks,
architecture/error-code guards, governance and tracked-source immutability.
Native jobs also compile-check the Tier 1 targets; those cross-builds retain
compile-only qualification. CI requires both suites and DCO. A passing native
suite alone does not establish deep UI or performance acceptance. Unknown suite
arguments fail before any installation or check can be skipped.

CI sets `GOCACHE` for both `setup-go` and the verifier/build subprocesses, so
restored compilation results are actually reused. Native and web cache inputs
remain separate to avoid concurrent jobs saving different coverage under one
immutable key; Go caches remain OS/architecture-specific. npm's download cache
is retained, with locked installation on each runner. The web reproducibility
test already builds twice and leaves validated assets for the browser checks;
the verifier does not build them a third time. Jobs remain parallel because the
small frontend build does not justify a shared-artifact dependency wait.

The native browser smoke proves bootstrap/authenticated loading, CSRF rejection,
single-profile selection persisted for CLI use, running-launch preservation,
service re-entry and unavailable-service guidance. It has normal functional
timeouts, not a performance-budget assertion. Deep UI scenarios run once in the
shared job rather than repeating on each operating system.

No application identity, credential, provider endpoint, signing secret,
telemetry endpoint, or production service is available to any job.

## Focused checks

Go:

```sh
gofmt -l $(find cmd internal scripts -name '*.go' -type f -print)
go vet ./cmd/... ./internal/...
env GOCACHE=/tmp/codex-folio-go-cache go test ./cmd/... ./internal/...
node scripts/build.mjs --build-class development
node --test scripts/build-targets.test.mjs
```

The target-build test invokes the repository-owned build command for every
Phase 0 target. Each result names the target, product version, source revision,
native or cross-compiled mode, and its compile-only qualification. To inspect a
single target directly:

```sh
node scripts/build.mjs --build-class development --target linux-amd64
node scripts/build.mjs --build-class development --target windows-amd64
node scripts/build.mjs --build-class development --target macos-arm64
```

Explicit target output is written below `build/targets/<target>/`. These builds
prove the portable compilation contract only; they do not qualify native
process, vault, service, installer, or runtime behavior on the target platform.

Archive dry run:

```sh
node scripts/release.mjs --dry-run --build-class development
node --test scripts/release.test.mjs
```

The dry run builds one archive for each Tier 1 target below
`build/releases/<version>-<build-class>/`:

| Target        | Archive                                                  |
| ------------- | -------------------------------------------------------- |
| Linux AMD64   | `codex-folio-<version>-<build-class>-linux-amd64.tar.gz` |
| Windows AMD64 | `codex-folio-<version>-<build-class>-windows-amd64.zip`  |
| macOS ARM64   | `codex-folio-<version>-<build-class>-macos-arm64.tar.gz` |

Development and prerelease names include their build classification. A stable
name would omit that suffix, but stable archives fail closed until the
mandatory signing, attestation, and platform notarization gates exist. Every
archive contains its executable, the applicable inspectable installer helper,
`BUILD-INFO.json`, `INSTALL.md`, `LICENSE`, and `NOTICE`. The output directory
also contains `SHA256SUMS`, a deterministic dependency-license inventory,
CycloneDX SBOM, a machine-readable provenance placeholder, a signing-status
record, and a release manifest.

The current Go module includes the reviewed pure-Go SQLite dependency and its
transitive modules. If another Go dependency is added without reviewed license
metadata, release generation fails closed instead of silently omitting it from
the inventory.

Archive bytes are reproducible for the same source and lock state: entries are
sorted, archive timestamps and ownership are fixed, build timestamps are not
recorded, and the Go build uses `-trimpath`. The manifest reports
`compile-only; no native runtime qualification`; an archive over 50 MB is
explicitly marked for review. The dry run never executes an installer, creates
a GitHub release, uploads an artifact, or contacts a signing/provenance
service.

Frontend:

```sh
npm --prefix web run check:tooling
npm --prefix web ci
npm --prefix web run format:check
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

The browser gate installs Playwright Chromium when needed (including Linux CI
system dependencies). To use an already installed Chromium, set
`CODEX_FOLIO_CHROMIUM` to its executable path. This is test tooling only; the
embedded dashboard never downloads browser assets. A focused run after building
frontend assets is:

```sh
CODEX_FOLIO_BROWSER_TEST=1 go test ./cmd/codex-folio -run '^TestOverviewBrowser$' -count=1 -v -timeout=5m
CODEX_FOLIO_BROWSER_TEST=1 CODEX_FOLIO_BROWSER_SUITE=smoke go test ./cmd/codex-folio -run '^TestOverviewBrowser$' -count=1 -v -timeout=5m
CODEX_FOLIO_BROWSER_TEST=1 go test ./cmd/codex-folio -run '^TestOverviewStartupBenchmark$' -count=1 -v -timeout=5m
```

On Windows, set the environment variable using the native shell. Captures and
results go to the OS temporary directory's `codex-folio-overview-browser`, or
`CODEX_FOLIO_BROWSER_OUTPUT` when set. The fixture starts the real authenticated
loopback service over isolated SQLite/vault state and substitutes only Codex
collection. It requires no installed Codex or identity credentials. See the
[Overview evidence](../design/overview-59/README.md) for qualifications.
`service start` on a running owner issues a fresh one-time dashboard link.

The startup budget is evaluated separately: one warm-up, then five serial
measurements, each with a fresh browser and isolated SQLite/vault/service
fixture (two profiles, one cached capture per profile). Timing runs from browser
navigation through visible authenticated Current capacity; browser/process and
fixture setup are outside the measurement. Viewport is 1440 × 1000, locale
`en-US`, timezone UTC and dark appearance. CI pins Ubuntu 24.04 and uses the
Chromium revision locked by Playwright 1.57.0; it does not use an executable
override. The median must be **less than 1,000 ms**. Every measured sample and
the maximum are reported; failures are not retried or discarded. The single
warm-up is explicitly excluded, not conditionally chosen after seeing results.

`startup-benchmark.json` records the sample set, median, maximum, budget,
browser, OS/kernel, architecture, CPU/memory, runner image and CI checkout
revision. CI publishes it in the job summary even when the budget fails.
Hosted hardware remains shared and is recorded, not claimed to be dedicated or
identical between runs. This is a repeatable engineering benchmark on the
reference environment, not proof of a one-second guarantee on every machine.
Native performance qualification and manual accessibility limits remain separate.

Governance:

```sh
node scripts/check-governance.mjs
node --test scripts/check-dco.test.mjs
```

Architecture and compatibility guardrails:

```sh
node scripts/check-architecture.mjs
node --test scripts/check-architecture.test.mjs
node scripts/check-error-codes.mjs
node --test scripts/check-error-codes.test.mjs
```

Pull-request commits are checked separately for DCO sign-off using the same
`scripts/check-dco.mjs` command as CI.

`npm run test` builds twice, checks the observable application shell and local
asset references, and compares the two output trees byte-for-byte. No hosted
font, CDN, or runtime package download is required by the resulting assets.

## Common failures

- A pinned-toolchain failure reports the required and detected Go, Node.js, or
  npm version. Install the versions from `.tool-versions` and `.nvmrc`, then
  rerun the canonical command.
- An `npm ci` failure means the checked-in lock state cannot be installed. Do
  not replace it with an unlocked install; resolve the package/lock mismatch or
  registry connectivity problem and rerun verification.
- An OpenAPI drift failure names the generated artifact that differs. Run
  `node scripts/generate-openapi.mjs`, review the contract change, and commit
  both generated outputs together.
- A tracked-source or lockfile immutability failure means a verification step
  changed a tracked file. Inspect `git status --short`; generated build output
  must remain in the ignored `build/` or `web/dist/` paths.
- A target build is compile-only even when its target matches the host. Do not
  interpret that evidence as native process, vault, service, installer, or
  runtime qualification.

## Version identity

The canonical product version is `internal/buildinfo/version.txt`, consumed by
both the Go executable and Vite. The executable reports the version in human
and machine-readable forms:

```sh
./build/bin/codex-folio version
./build/bin/codex-folio version --json
```

Build tooling injects the source revision, build classification, and working
tree state. Development builds may be dirty. A stable build fails unless the
revision is known and the working tree is clean. Unknown VCS state is reported
as `unknown`, never as clean.

The JSON contract contains `product`, `command`, `version`,
`source_revision`, `build_class`, and `dirty` fields. The `dirty` field is one
of `clean`, `dirty`, or `unknown`.

## Analytics retention and scoped purge

The state-owning service provides these CLI operations and the generated
`POST /api/v1/analytics/history` contract:

```sh
codex-folio analytics retention
codex-folio analytics retention 30
codex-folio analytics retention unlimited
codex-folio analytics retention 13-months --run
codex-folio analytics purge --profile '*' --project '*' --from all --to all --classes usage,aggregates --dry-run --json
codex-folio analytics aggregates --profile '*' --project '*' --from all --to all --json
```

All commands accept `--state-root PATH`, `--vault-mode MODE`, and `--json`.
Retention defaults to thirteen calendar months (clamped at month end), with
explicit day counts of at least thirty or `unlimited`. A successful collection
or `retention --run` processes one transaction with at most 100 detail records
per class; `more: true` means another run can continue. No periodic scheduler
is installed. Settings changes leave diagnostics and checkpoint expiry alone.

Expired usage detail is compacted into distinct normalized readings. Each
aggregate retains its value, metric/unit, provenance, source/version, encrypted
source scope, availability, assumptions, observation/capture range, and bucket.
`samples` counts repeated readings, never additive quota or usage. Provider
windows remain source windows; only unwindowed observations receive calendar
days in their recorded timezone or UTC. Stored bucket zones never change.
Aggregates remain until explicitly purged. The latest snapshot status and
decisive authentication evidence remain as bounded current-state records;
last-known values can be projected from aggregates. Expired activity metadata
is deleted; running/pending launches and referenced launch records are preserved.

Purge requires every scope dimension: `--profile ID|'*'`,
`--project ID|'*'|none`, `--from RFC3339|all`, `--to RFC3339|all`, and an explicit
comma-separated `--classes` selection from `usage`, `aggregates`,
`observed_sessions`, `managed_launches`, and `checkpoints`. Profile IDs are
available from `profile list --json`. Dates are inclusive at `from`, exclusive
at `to`; whole activity intervals and aggregate buckets must fit inside the
range. Provider usage has no Project Identity, so a specific project excludes
it. Checkpoints have no profile attribution and require `--profile '*'`.

`--dry-run` reports scope, each affected record class, counts, the atomic limit,
and a scope-bound confirmation token. Normal purge prompts for that token;
automation must supply `--non-interactive --confirm TOKEN` with the same full
scope. A purge touching more than 1000 records is rejected before deletion;
narrow its scope using the preview. Aggregation and deletion commit together;
purge commits all selected classes together. Reference cleanup is included in
the counts. Purge never deletes profiles, homes, authentication, configuration
packs, quarantine, vault keys, projects, or checkpoints outside the explicit
checkpoint scope. Aggregate listing returns at most 1000 buckets; narrow dates
or profile scope for larger histories.

## Analytics export

Preview or write normalized usage, availability, aggregate, or activity data
through the same generated analytics-history contract:

```sh
codex-folio analytics export --format json --datasets usage,availability,activity --dry-run --json
codex-folio analytics export --format json --datasets usage,aggregates --output analytics.json
codex-folio analytics export --format csv --datasets activity --output activity.csv
```

The default scope is the Selected Profile with all projects and dates. Combined
Identity View requires `--scope combined_identity --profile '*'`; CSV accepts
one dataset. Add `--project`, `--from`, or `--to` to narrow the result. The
preview reports the exact fields and record counts. Exports contain normalized
evidence only and use Project Alias and basename by default; `--include-paths`
explicitly adds canonical project paths. Identity Home paths, credentials, raw
source responses, Codex content, commands, tool payloads, diffs, transcripts,
vault material, and diagnostics are never export fields. Destination creation
is exclusive: an existing file is preserved and the export fails safely.

## Current limitations

Phase 0 proves a native development build on the host, compile-only builds for
Linux AMD64, Windows AMD64, and macOS ARM64, and an unsigned archive dry run
for those targets. The secure local foundation is a separate milestone.
Compile and archive evidence here does not claim native runtime qualification,
stable signing, notarization, or production release readiness.
