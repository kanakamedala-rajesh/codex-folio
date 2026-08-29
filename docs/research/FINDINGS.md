# Findings

## Official Codex surface

- Codex supports ChatGPT subscription authentication and API-key authentication for local work. Routine switching does not require a new browser login when each Identity Profile retains its own valid persistent Codex home; browser or device authentication is still required for initial onboarding, revocation, expiry, or policy challenges.
- Codex configuration profiles layer configuration files and are not authentication profiles.
- Codex App Server exposes typed account state, ChatGPT rate limits, reset credits, and token-usage summaries. Its default stdio protocol is the preferred local integration seam.
- App Server's network WebSocket transport and externally managed ChatGPT-token mode are experimental and are not suitable foundations for the MVP.
- No reviewed official contract guarantees that an executing turn can change authentication identity or that a stored thread can be transferred between different Codex homes. Exact continuation therefore needs capability detection, version gating, and a guaranteed safe fallback.

### Thread and handoff surface in Codex CLI 0.150.1

- `thread/read`, `thread/resume`, and `thread/fork` operate on threads stored in the active App Server store. The CLI similarly provides `codex resume`, `codex fork`, `codex archive`, and `codex unarchive` within its active home.
- Neither the CLI nor App Server exposes a documented source-home parameter or a cross-home thread import/export operation.
- App Server account login and logout are process-level operations, but the official contract does not define rebinding a loaded thread to a new identity or changing identity during an active turn.
- Copying rollout files, sharing a session database, or symlinking session directories between homes is undocumented and risks schema drift, index or WAL divergence, ID collision, and content leakage.
- The safest cross-identity handoff is a fresh thread in the target Identity Home created from an explicit, user-reviewed handoff summary. Exact history transfer can only be experimental.
- Any experimental transfer must require idle/stopped source and target processes, compatible Codex and storage versions, a collision check, atomic staging, post-transfer read/resume validation, and rollback.

## `codex-as-go`

### Reusable ideas

- Native Go CLI wrapper that forwards Codex arguments and terminal streams.
- Interactive and non-interactive Identity Profile selection.
- Isolated temporary Codex home for onboarding and reauthentication.
- Account aliases, diagnostics, safe deletion confirmation, and portable Windows/Linux behavior.

### Production gaps

- It switches identities by copying raw `auth.json` files into a shared Codex home.
- The switch is not transactional; interruption can corrupt the alias-to-credential mapping.
- Process-name scanning is race-prone and produced false positives in local tests.
- Work and personal sessions, configuration, plugins, and history cross trust boundaries implicitly.
- It has no dashboard, usage aggregation, macOS release pipeline, signing, native platform CI, or production release workflow.

## `vs-codexscope`

### Reusable ideas

- Modular Go service with an embedded responsive React UI.
- Typed domain responses, partial-source failure handling, and privacy-preserving local session aggregation.
- Loopback binding, Host validation, CSP, no CORS, mutation checks, opaque identifiers, and spreadsheet-safe exports.
- Incremental JSONL scanning with bounded concurrency.

### Production gaps

- It supports one fixed Codex home and cannot launch or switch Identity Profiles.
- Online metrics call undocumented ChatGPT endpoints directly with extracted bearer credentials; App Server should replace this path.
- Its configured periodic UI refresh does not trigger backend recollection.
- Session pagination, persistent analytics, frontend tests, macOS/ARM64 releases, signing, and native platform QA are absent.
- A static mutation header is not sufficient authorization for future credential or remote-control operations.

## Verification performed during research

- `codex-as-go`: build and vet passed; 22 tests passed and 4 failed because process detection falsely classified the environment as a running Codex process.
- `vs-codexscope`: 12 packages passed; one HTTP test package could not bind an IPv6 loopback socket under the sandbox. No assertion failure was observed.
- Installed Codex CLI inspected read-only: version `0.150.1`; login, profile, doctor, App Server, and generated-schema surfaces were reviewed without reading authentication or session contents.

## Primary implications

1. Keep Codex authentication persistent and isolated per Identity Profile instead of copying credentials.
2. Use a version-gated App Server stdio adapter for provider-reported account and usage data.
3. Keep the dashboard strictly read-only with respect to remote account, credits, limits, and workspace state.
4. Separate guaranteed Safe Continuation from experimental Exact Continuation.
5. Preserve source, timestamp, freshness, and error status for every displayed metric.
6. Start greenfield and selectively port reviewed logic and tests from the prototypes.

## Multi-profile aggregation does not require shared state

- A dashboard can start a short-lived, isolated App Server subprocess for each persistent Identity Home and collect that profile's provider-reported metrics without changing the active CLI identity.
- Usage Snapshots can be keyed by Identity Profile and aggregated into per-profile, selected-profile, workspace, login-identity, and all-profile views.
- Locally derived session analytics can likewise be aggregated across multiple home indexes while preserving profile provenance and avoiding duplicate counting by stable session identity.
- A shared Codex home offers session convenience, not an analytics capability. Because it contains one active authentication context, polling multiple identities would require repeated credential mutation and serialization.
- Isolated Identity Homes permit concurrent Codex processes and concurrent read-only collection; a shared credential-swapping home does not.

## Experimental Shared Work Home feasibility

- A single Shared Work Home naturally exposes the same stored sessions, configuration, skills, plugins, and custom agents after an identity change.
- Exact continuation can be attempted between idle turns by stopping the current Codex process, transactionally replacing the active authentication file, and relaunching `codex resume <session-id>` in the same home.
- The switch cannot safely occur during an executing turn and must not race any Codex process or another switch operation.
- Avoiding routine reauthentication in this mode requires CodexFolio to retain and copy raw Codex credential files, contrary to the preferred Codex-owned isolated-home security boundary.
- A productionized switch requires an ownership lock, staged writes, an intent journal, rollback, startup recovery, restrictive Unix permissions and Windows ACLs, identity verification, redaction tests, and post-switch App Server validation.
- Shared Work Home mode serializes identities and cannot support simultaneous work or usage collection from multiple identities through that home. Separate profile-owned authentication sources are still required for combined live analytics.
- The behavior is plausible and close to the existing prototype, but it is not a documented Codex multi-identity feature and must remain experimental and fail closed on incompatible Codex versions.

## Metric interpretation

- Provider quota windows and credits are Identity Profile-level observations; Codex does not report an exact weekly or five-hour quota contribution for each local session.
- Before/after snapshots may estimate a session's relationship to a quota window, but concurrent activity and delayed provider updates prevent exact attribution.
- Local Codex metadata can provide recorded token categories without reading transcript content, but these values must not be represented as provider billing totals.
- Git changes observed while a session is active can be counted without retaining file content, but user edits, hooks, formatters, and concurrent tools prevent attributing all changed lines to Codex.
