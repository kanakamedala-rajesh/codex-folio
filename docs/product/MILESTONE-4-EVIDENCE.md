# Milestone 4 Safe Continuation evidence index

Status: candidate evidence for specification [#47](https://github.com/kanakamedala-rajesh/codex-folio/issues/47) and executable ticket [#55](https://github.com/kanakamedala-rajesh/codex-folio/issues/55). Milestone completion still requires the separate read-only milestone review and accepted live-evidence outcome or qualification exception.

## Source

The Milestone 4 implementation is the contiguous ticket series below. The #55
candidate is the commit containing this record; its exact SHA, review result,
clean-commit verification, and hosted results must be recorded on #55 before
delivery.

| Ticket | Revision | Scope |
| --- | --- | --- |
| #48 | `0e746ed96bd4e441c55eb95fee68a863d105b19e` | repository-first capture |
| #49 | `5b8a918d74786d5936ab7025f5302d0c3044d543` | review, edit, approval, and cancellation |
| #50 | `9d1cf762a1732d54612172c8297cc64283eda058` | fresh target-profile handoff |
| #51 | `21b046f65f5670e03c836fada02a20f875d859d9` | quota-exit offer and eligibility ranking |
| #52 | `dd066002d2f10bf40ffd0b550b320cb65fe91311` | consented transcript assistance and repository fallback |
| #53 | `59748629e447ba2f94ae383bc81120ddcaa71202` | retention, expiry, recovery, and scoped purge |
| #54 | `8075f74c06a2a211e638b9ff0a829a7fc44f1d18` | encrypted and expressly acknowledged plaintext export |
| #55 | commit containing this record | integrated acceptance and evidence index |

## Acceptance evidence

| Requirement | Automated evidence |
| --- | --- |
| CT-01 | `TestLaunchCLIAutomaticallyOffersAndRunsApprovedSafeContinuation`; `TestPrepareHandoffRejectsUnapprovedExpiredChangedOrUnstoppedState` |
| CT-02 | `TestLaunchCLIAutomaticallyOffersAndRunsApprovedSafeContinuation`; `TestHandoffCLIReviewsApprovedContextAndLaunchesFreshTargetInSourceRepository` |
| CT-03 | `TestCapturePersistsSanitizedRepositoryFirstDraft`; `TestPartialValidationUsesExplicitUnknowns` |
| CT-04 | `TestInspectorUsesMetadataOnlyGitCommands`; `TestInspectorDisablesConfiguredHelpers` |
| CT-05 | `TestHandoffCLIUsesConsentedHistoryOnlyAfterSanitizedApproval`; `TestTranscriptAssistancePersistsOnlyTheApprovedSanitizedRevisionForSevenDays` |
| CT-06 | `TestCheckpointCLICapturesAndShowsThroughServiceAndEncryptedStore`; `TestPortableCheckpointExportAuthenticatedRoundTrip` |
| PB-01–05, PL-10 | the integrated CLI tests use the installed-executable boundary, isolated profiles, foreground launches, and a fresh target process; Exact Continuation and remote mutation are absent |
| UA-06/09 | `TestLaunchExitOffersSafeContinuationFromSupportedQuotaEvidence`; integrated diagnostics and export assertions exclude unusable profiles, canonical paths, and repository content |
| SP-01–08 | `TestContinuationCheckpointMetadataUsesEncryptedCheckpointStorage`; authorized HTTP checkpoint tests; sentinel assertions in CLI, store, and export tests |
| OP-04/08 | canonical verifier native runs; platform-specific boot-session, foreground-process, editor, filesystem, vault, and path fixtures |
| RG-06 | canonical verifier, focused race-free Go tests, and the separate milestone review gate |

## Deliverable and failure-path evidence

| Behavior | Automated evidence |
| --- | --- |
| quota observation, confirmed exit, offer, edit, approval, fresh different-profile process | `TestLaunchCLIAutomaticallyOffersAndRunsApprovedSafeContinuation` |
| cancellation; active or uncertain source; unavailable target; changed approval; failed start | `TestHandoffCLIReviewsApprovedContextAndLaunchesFreshTargetInSourceRepository`; `TestEditSanitizesDraftAndApprovalRequiresTheReviewedRevision` |
| interrupted start and retry only after boot-session change | `TestReconcilePendingHandoffRecoversOnlyAfterBootSessionChanges` |
| expiry, completed retention, and checkpoint-only purge | `TestCheckpointRetentionControlsOriginExpiryAndExpiredLifecycle`; `TestHandoffLaunchReservesApprovedCheckpointAndCompletesItOnStart`; `TestPurgeProjectActivityRollbackRestartAndLifecycleIsolation` |
| encrypted export, tamper rejection, and plaintext-specific consent | `TestCheckpointCLICapturesAndShowsThroughServiceAndEncryptedStore`; `TestPortableCheckpointExportAuthenticatedRoundTrip`; `TestPlaintextCheckpointExportRequiresCompletePreview` |
| no repository artifacts, Identity Home or credential mutation, raw-history persistence, excluded target input, path disclosure, or content-bearing diagnostics | integrated CLI sentinel and tree-snapshot assertions in `launch_test.go`, `handoff_test.go`, and `checkpoint_test.go` |

## Commands and platform qualification

Record each result against the exact #55 commit; do not substitute compile-only
target builds for native runtime evidence.

| Environment | Command | Current result |
| --- | --- | --- |
| local WSL2 AMD64 (Ubuntu 24.04.3, kernel `6.6.87.2-microsoft-standard-WSL2`) | `go test -count=1 ./cmd/codex-folio ./internal/adapters/codex ./internal/adapters/git ./internal/continuation ./internal/httpapi ./internal/store` | PASS on the dirty #55 candidate |
| local WSL2 AMD64 | `node scripts/verify.mjs` | PASS on the dirty #55 candidate with pinned Go 1.27.0; exact-clean-commit rerun pending |
| hosted Linux AMD64 | `node scripts/verify.mjs` | pending #55 delivery |
| native Windows AMD64 | `scripts/verify-windows.ps1 -ExpectedRevision <sha>` | pending user-run evidence on the exact #55 commit |
| hosted Windows AMD64 | `node scripts/verify.mjs` | pending #55 delivery; corroborating CI evidence only |
| hosted macOS ARM64 | `node scripts/verify.mjs` | pending #55 delivery |
| production-authenticated cross-profile Codex | live journey | not run; owner accepted a qualification exception on 2026-09-10; no live claim |

Deterministic fake-Codex tests prove application orchestration without using
credentials or provider access. They do not qualify production-authenticated
cross-profile continuation. Non-host builds remain compile-only. Stable
signing, publishing, Exact Continuation, Shared Work Home, and dashboard work
remain outside Milestone 4.
