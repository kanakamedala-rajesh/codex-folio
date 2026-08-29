# Build the Phase 0 scaffold

The repository currently contains the non-production Phase 0 skeleton. It does
not authenticate with Codex, launch Codex, access provider data, persist local
state, or start a service.

## Pinned prerequisites

The supported development toolchain is:

| Tool | Version |
| --- | --- |
| Go | `1.27.0` |
| Node.js | `24.18.0` |
| npm | `11.16.0` |

The versions are recorded in `.tool-versions`, `.nvmrc`, `go.mod`, and
`web/package.json`. The frontend workspace uses `engine-strict=true` and its
`check:tooling` command fails with an actionable message when the pinned Node
or npm version is unavailable.

## Install and verify

From the repository root, install the exact dependency lock state once:

```sh
npm --prefix web ci
```

Then run the scaffold verification entry point:

```sh
node scripts/verify.mjs
```

The verification command runs Go formatting, vet, unit tests, and a native
build; frontend formatting, linting, type-checking, tests, and build; and a
self-contained frontend smoke check. It uses the same checked-in package lock
and fails if tracked source changes during verification. Build output is
written under ignored `build/` and `web/dist/` directories.

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
Linux AMD64, Windows AMD64, and macOS ARM64 target matrix, OpenAPI generation,
release archive dry runs, governance checks, and the secure local foundation
are separate Phase 0 tickets or later milestones. Compile evidence here does
not claim native runtime qualification on another operating system.
