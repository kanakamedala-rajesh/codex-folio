# Milestone 1 secure local foundation exit-gate evidence

Status: evidence recorded for specification [#10](https://github.com/kanakamedala-rajesh/codex-folio/issues/10) and executable ticket [#20](https://github.com/kanakamedala-rajesh/codex-folio/issues/20).

This record qualifies the secure local foundation only. It does not claim
Identity Profile onboarding, Codex authentication, launching, collection,
analytics, Safe Continuation, a production dashboard, telemetry, signing, or
release readiness.

## Source and commands

The implementation under native qualification is revision
`26203ccb87b7b5d0a8a7ab78aa44a1203e2ecd25` (`feat: enforce bounded diagnostics
(#19)`). The working tree was clean when the canonical verifier ran.

The exact pinned toolchains were Go `1.27.0`, Node.js `24.18.0`, and npm
`11.16.0`. The following checks passed locally on Linux AMD64:

```text
node scripts/verify.mjs
go test -count=1 ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
npm --prefix web run typecheck
```

The canonical verifier passed all Phase 0 gates, including generated-contract
drift, architecture and stable error-code checks, Go tests, native executable
identity, Tier 1 target builds, archive dry run, governance/DCO checks,
frontend checks, offline assets, and tracked-source immutability.

The hosted [Verify run 33541539492](https://github.com/kanakamedala-rajesh/codex-folio/actions/runs/33541539492)
checked out the same revision and passed the canonical command on all Tier 1
runners:

| Runner | Job | Qualification |
| --- | --- | --- |
| Linux AMD64 (`ubuntu-latest`) | [99968737182](https://github.com/kanakamedala-rajesh/codex-folio/actions/runs/33541539492/job/99968737182) | native service, storage, vault, HTTP, diagnostics, and build checks |
| Windows AMD64 (`windows-latest`) | [99968737299](https://github.com/kanakamedala-rajesh/codex-folio/actions/runs/33541539492/job/99968737299) | native Windows service, DPAPI, storage, HTTP, diagnostics, and build checks |
| macOS ARM64 (`macos-15`) | [99968737041](https://github.com/kanakamedala-rajesh/codex-folio/actions/runs/33541539492/job/99968737041) | native macOS service, Keychain, storage, HTTP, diagnostics, and build checks |

The macOS job provisioned an ephemeral user Keychain before running tests.
Linux Secret Service round-trip coverage is conditional on `secret-tool` and
an active user session; unavailable or locked Secret Service behavior is tested
as a fail-closed path. The explicit passphrase-backed headless path is tested
independently.

## Requirement evidence

| Exit-gate requirement | Evidence |
| --- | --- |
| Canonical verifier and deterministic generated/embedded output | `node scripts/verify.mjs`; `scripts/generate-openapi.test.mjs`; `web/scripts/test-build.mjs`; `web/scripts/smoke.mjs`; tracked-source immutability gate |
| Native Tier 1 service/security suite | Hosted Verify run above; `go test ./cmd/... ./internal/...` executes the composed command, HTTP, platform, store, vault, and diagnostics packages on each runner |
| Platform defaults, absolute overrides, permissions, second writer, and loopback | `internal/platform/paths_test.go`; `internal/platform/owner_test.go`; `cmd/codex-folio/main_test.go`; `internal/httpapi/server_test.go` |
| Native vault paths and fail-closed behavior | `internal/platform/*_test.go`; `cmd/codex-folio/service_store_linux_test.go`; `cmd/codex-folio/service_store_windows_test.go`; `cmd/codex-folio/service_store_darwin_test.go`; `internal/vault/vault_test.go` |
| Browser and threat-model attacks | `TestBootstrapExchangeAuthorizesMetadataAndCannotReplay`; `TestBootstrapExpiryIsSingleUseAndSafe`; `TestSessionExpiryAndRestartInvalidation`; `TestHostOriginAndCSRFChecksFailClosedWithoutCORS`; `TestHTTPAuthorizationFailuresEmitStableRedactedDiagnostics` |
| Migration, corruption, backup rotation, candidate validation, restore, and rollback | `internal/store/recovery_test.go`; `TestOpenRejectsIntegrityFailureBeforeAdmittingWrites`; `TestOpenClassifiesMalformedDatabaseAsIntegrityFailure`; `TestMigrationFailureRollsBackTheCandidateSchema` |
| Envelope encryption, tamper detection, key separation, and no plaintext downgrade | `internal/vault/vault_test.go`; `internal/store/secure_test.go`; `TestRecoveryBackupsRetainCiphertextWithoutVaultMaterial`; platform-native service-store restart tests |
| Bounded, redacted diagnostics and safe errors | `internal/diagnostics/diagnostics_test.go`; `internal/store/diagnostics_test.go`; `cmd/codex-folio/main_test.go`; `internal/httpapi/server_test.go` |
| No later-milestone behavior | The implementation remains limited to `cmd/codex-folio`, `internal/platform`, `internal/store`, `internal/vault`, `internal/httpapi`, `internal/diagnostics`, and foundation composition; no profile, launch, collection, continuation, production dashboard, telemetry, or remote-service workflow is claimed |

The test names and fixtures cover the required failure cases: unbootstrapped
browser, bootstrap replay, expired session, hostile Host/Origin, forged CSRF,
missing or locked vault, tampered ciphertext, failed migration, corrupt active
database, corrupt backup candidate, failed backup/restore, and activation
rollback. Sentinel tests cover credentials, authorization material, vault key
material, encrypted values, canonical paths, Codex/user/repository content,
and raw provider-payload-shaped data.

The recovery fixtures and injected-boundary outcomes are:

- `TestRecoveryRotatesThreeValidatedCandidates` calls `CreateCheckpoint` four
  times with `RecoveryBackupCount == 3`; only `backup-1` through `backup-3`
  remain, each validates at `CurrentSchemaVersion`, and the oldest snapshot is
  rotated out.
- `TestRecoveryRotationFailureRetainsSparseKnownGoodCandidate` removes
  `backup-1` and `backup-2`, then makes `failingRenameFileSystem` return
  `errors.New("injected rotation failure")` for the rotation rename(s) (one on
  Unix, two on Windows). The result is `CF_STORE_BACKUP_FAILED`; the remaining
  valid `backup-3` bytes are unchanged.
- `TestRecoveryRestoresAnIntentionalCandidateAndPreservesActiveState` creates
  `checkpoint-first` and `checkpoint-second`, restores `backup-2`, and observes
  a non-empty preserved-database ID, the first checkpoint present, the second
  absent (`sql.ErrNoRows`), and an ordered known-loss window.
- `TestRecoveryListsCorruptCandidatesAndRestoresOverCorruptActiveState` writes
  `corrupt backup` and `corrupt active` fixtures. The first is listed and
  rejected as `CF_STORE_RECOVERY_CANDIDATE_INVALID` without changing active
  bytes; the second yields `CF_STORE_INTEGRITY_FAILED`, then restores the
  validated candidate while preserving the damaged active database as
  `damaged-*`.
- `TestRecoveryFailureDuringBackupOrRestoreLeavesKnownGoodStateAvailable`
  injects `AfterBackupCopy` and `BeforeRestoreActivation` failures using
  `errors.New("injected recovery boundary failure")`. They return
  `CF_STORE_BACKUP_FAILED` and `CF_STORE_RECOVERY_RESTORE_FAILED`, respectively;
  the previous backup and active database remain byte-identical.
- `TestRecoveryRollsBackAfterActivationFailure` inserts the `active-only`
  diagnostic sentinel and injects `AfterRestoreActivation` with
  `errors.New("injected post-activation failure")`. It returns
  `CF_STORE_RECOVERY_RESTORE_FAILED` and restores the active bytes exactly.
- `TestMigrationBackupFailureStopsBeforeSchemaMutation` injects `BeforeBackup`
  with `errors.New("injected backup failure")`; it returns
  `CF_STORE_BACKUP_FAILED` with zero tables and zero candidates.
- `TestMigrationFailureRetainsThePreMigrationRecoveryPoint` injects
  `MigrationHooks.After` with `errors.New("injected migration failure")`; it
  returns `CF_STORE_MIGRATION_FAILED` with one valid schema-zero candidate and
  no migrated tables. `TestMigrationFailureAfterCommitStillExposesTheOriginalCandidate`
  applies the same assertion to `AfterMigrationBeforeActivation` with
  `errors.New("injected activation boundary failure")`.

## Qualification limits

- Non-host Tier 1 target builds are compile-only; they are not native runtime,
  filesystem, service, vault, or loopback qualification.
- Secret Service availability depends on the runner's desktop session. The
  fail-closed unavailable/locked behavior is qualified when the service is not
  available; an active desktop Secret Service round trip is not claimed unless
  the runner provides it.
- Development archives remain unsigned dry-run artifacts. Stable signing,
  attestation, notarization, publishing, and production release qualification
  remain outside Milestone 1.
- No application identity, provider access, production secret, remote
  CodexFolio endpoint, or remote mutation is required by these checks.
