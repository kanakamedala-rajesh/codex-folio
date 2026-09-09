# Safe Continuation research

## Recommended baseline

Safe Continuation creates a deterministic, repository-first checkpoint without using the exhausted source identity's model quota. A target identity starts a new thread from the same repository and the user-approved checkpoint.

“Repository-first” describes the continuation source of truth, not a repository storage location. CodexFolio must not generate `.codex-folio` or any other application-state file in the repository. The repository/worktree remains in place; the checkpoint lives in encrypted app-local storage and references the repository through a non-secret local identity. A portable checkpoint file is created only through explicit export.

That repository reference is resolved through the encrypted app-local Project Identity mapping. Browser responses and ordinary exports use its user-editable alias and repository basename, not the full local path.

Suggested bounded schema:

```text
artifact_version
created_at and freshness
repository basename, branch, HEAD, and upstream divergence
staged, modified, and untracked repository-relative paths
diff statistics without raw diff content
explicit persisted goal
explicit plan steps and statuses
recorded verification command, timestamp, exit status, and source
user-reviewed blockers, decisions, and next actions
provenance and completeness per field
```

Do not rerun tests automatically during capture. Tests may mutate state or use external services; carry only previously recorded evidence and mark it stale when appropriate.

## Optional context recovery

App Server `thread/read` can read stored history without starting a model turn. An explicit enhancement may:

1. Read the source thread only after user consent.
2. Keep raw turns in memory and out of logs, crash reports, telemetry, and persistent storage.
3. Extract bounded candidate facts.
4. Let the user edit and approve every candidate.
5. Persist and transmit only the approved structured checkpoint.

If semantic summarization is requested, the target identity performs it only after the user previews and approves the supplied context. CodexFolio must never silently send personal conversation content into a work or enterprise workspace.

## Limits and fallback

- Default checkpoint size: 8–16 KiB, approximately 2–4k tokens.
- Default retention: 30 days for repository-first checkpoints and seven days for transcript-assisted checkpoints, configurable from one day to unlimited.
- A successful target launch marks the checkpoint completed but retains it until expiry or explicit analytics purge.
- A reservation records the OS boot-session identity. If process start is not reported, keep the checkpoint non-authorizing during that boot; after a verified boot-session change, abandon the stale pending lease, restore the checkpoint, and rerun every current handoff check. Time alone is never evidence that the target stopped.
- Exclude raw prompts, replies, diffs, tool output, environment values, authentication data, absolute home paths, and credential-bearing remotes.
- Use repository-relative paths and allow path redaction.
- Label fields as local-observed, user-confirmed, or model-derived.
- Fall back to repository-only continuation when history is missing, active, corrupt, unsupported, too large, insufficiently redactable, or disallowed by workspace policy.
