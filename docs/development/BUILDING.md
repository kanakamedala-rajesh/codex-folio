# Build the Phase 0 scaffold

The repository currently contains the non-production Phase 0 skeleton. It does
not authenticate with Codex, launch Codex, access provider data, persist local
state, or start a service.

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

## Install and verify

From the repository root, install the exact dependency lock state once:

```sh
npm --prefix web ci
```

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

Then run the scaffold verification entry point:

```sh
node scripts/verify.mjs
```

The verification command runs the OpenAPI drift check and generator tests;
architecture and stable error-code checks with focused fixture tests; Go
formatting, vet, unit tests, and a native build; frontend formatting, linting,
type-checking, tests, and build; a self-contained frontend smoke check; and the
repository governance and local documentation-link check. It uses the
same checked-in package lock and fails if tracked source changes during
verification. Build output is written under ignored `build/` and `web/dist/`
directories.

## Focused checks

Go:

```sh
gofmt -l cmd/codex-folio/main.go cmd/codex-folio/main_test.go internal/buildinfo/version.go internal/buildinfo/version_test.go
go vet ./cmd/... ./internal/...
env GOCACHE=/tmp/codex-folio-go-cache go test ./cmd/... ./internal/...
node scripts/build.mjs --build-class development
```

Frontend:

```sh
npm --prefix web run check:tooling
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

Phase 0 proves the native build contract on the host running the command. The
Linux AMD64, Windows AMD64, and macOS ARM64 target matrix, release archive dry
runs, and the secure local foundation are separate Phase 0 tickets or later
milestones. Compile evidence here does not claim native runtime qualification
on another operating system.
