# Build the Phase 0 scaffold and local foundation

The repository currently contains the completed non-production Phase 0
scaffold and the first Milestone 1 local-foundation slices. It does not
authenticate with Codex, launch Codex, access provider data, or expose product
storage workflows. The service can resolve platform app-local paths, initialize
the allowlisted versioned SQLite foundation, and run a foreground owner through
the CLI:

```sh
codex-folio service status [--state-root PATH] [--json]
codex-folio service start [--state-root PATH] [--json]
```

The owner lock, descriptor, and SQLite database are runtime foundation
artifacts; vaults, diagnostics, and other product workflows remain future
Milestone 1 tickets. The service is the only composed process path that opens
the durable SQLite store or runs its migrations.

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
and the repository governance and local documentation-link check. It uses the
same checked-in package lock and fails if tracked source changes during
verification. Build output is written under ignored `build/` and `web/dist/`
directories.

Successful output ends with a Phase 0 summary that identifies each completed
gate with `[PASS]` and each intentionally unavailable qualification with
`[SKIP]`. The expected skips are native runtime qualification for the
compile-only target builds and stable signing, attestation, notarization, and
release eligibility. The verifier executes the native development binary and
checks its JSON version, revision, build classification, and working-tree
identity before reporting the build gate as passed.

Continuous integration runs this same command for pull requests and changes to
`main` on Linux AMD64, Windows AMD64, and macOS ARM64 hosted runners. Every job
also compile-checks the other Tier 1 targets, records native versus
cross-compiled build mode, and retains the compile-only qualification. Its Go
and npm caches use the pinned tool versions and checked-in module or lock state.
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

## Current limitations

Phase 0 proves a native development build on the host, compile-only builds for
Linux AMD64, Windows AMD64, and macOS ARM64, and an unsigned archive dry run
for those targets. The secure local foundation is a separate milestone.
Compile and archive evidence here does not claim native runtime qualification,
stable signing, notarization, or production release readiness.
