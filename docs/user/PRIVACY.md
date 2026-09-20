# Privacy and data handling in the development preview

This page separates current documented behavior from future product intentions.
It is not a claim of an independent security certification. The reviewed
baseline is `5c6ca1d`. See [compatibility](COMPATIBILITY.md) for qualification gaps.

## Authentication and network boundaries

CodexFolio launches the installed Codex application and delegates authentication
to it. Isolated Identity Homes are the recommended design. Credentials and home
contents must not be copied into browser responses, analytics, public diagnostics,
or issue reports. The local browser service is designed for loopback access,
not exposure through a public tunnel or a router port-forward.

Local-first does not mean offline-only. Installed Codex may contact OpenAI and
consume account usage. CodexFolio update checks are separately consented: an
explicit check authorizes one request, while automatic checks require a
persisted opt-in that can be revoked. They request only a bounded version,
release-note, verified-download-location, and installer-guidance manifest from
a compiled project-controlled HTTPS endpoint. The development build has no
production endpoint configured. An optional telemetry client is shipped but is
off by default and cannot be enabled in production because no project HTTPS
endpoint, published privacy notice, retention jobs, or deletion/reset operation
is configured. Update consent does not enable telemetry.

## Data classes and retention

| Data class | Current documented behavior |
| --- | --- |
| Normalized usage detail | Default expiry is 13 calendar months; configurable day counts start at 30, or expiry can be disabled |
| Compacted usage aggregates | Remain until explicitly purged; detail expiry does not delete these aggregates |
| Bounded latest-state/authentication evidence | Can remain after detail compaction for current-state projection |
| Expired activity metadata | Deleted by maintenance, with running/pending and referenced launch records preserved |
| Repository checkpoints | Separate retention policy; inspect `checkpoint retention` |
| Transcript-assisted checkpoints | Separate consent, review, and retention policy; not ordinary analytics data |
| Profile quarantine | Local removal and purge are different operations; product policy specifies a seven-day quarantine |
| User-written exports and backups | Copies at destinations you choose; do not assume later database purge removes these copies |
| Native notifications | Generic text by default. A separate per-device preference may allow Identity Profile aliases, capacity, and guidance on OS-managed notification surfaces, including shared or locked screens |
| Local diagnostics | Enabled by default; stable component/error aggregates retain 14 days by default, with a configurable 1–30 day limit and independent disable/severity controls |
| Diagnostic support bundles | Created only after local preview and confirmation; written to a destination the user chooses and never uploaded automatically |
| Update checks | Off by default; explicit checks are one-shot, automatic checks require separate revocable consent, and only bounded release metadata is retained |
| Optional telemetry | Client present but off and unavailable in production; enablement requires separate consent to public schema v1 and every operational privacy prerequisite |

The analytics implementation processes bounded maintenance batches; it does not
promise that every expired row disappears immediately. No periodic scheduler is
installed by the analytics retention command itself. See the detailed retention,
aggregation, and purge contract in the [build guide](../development/BUILDING.md).

The short retention statement in the product document is broader than the detail
retention behavior described here. Maintainers must reconcile the accepted
requirements and implementation before advertising a uniform deletion guarantee.
This page does not silently approve a change to that product requirement.

## Native notification privacy

Installing the user service does not consent to detailed notification text.
Update checks, telemetry choices, imported configuration, and generic
confirmation also do not grant that consent. Generic native notifications do
not include Identity Profile aliases, quota values, provider windows, paths, or
Codex content. If you explicitly enable detailed text in Alerts or Settings,
the operating system may display an alias, remaining capacity, and guidance on
notification history, shared desktops, or locked screens according to platform
policy. Re-select **Generic notifications** to revoke detail for future
deliveries.

The native adapter receives only the already-projected title and body. Delivery
failure is stored as a stable safe status; raw native command output and message
content are not written to diagnostics. Alerts remain available locally when a
desktop notification facility or permission is unavailable.

## Analytics and exports

The documented analytics boundary excludes prompts, responses, raw diffs,
commands, tool output, raw sessions, and credentials. Normalized exports use
project aliases and basenames by default. Explicit path inclusion can expose
private project locations. Review export fields and destinations before sharing.

Repository-first checkpoints and explicitly approved transcript-assisted recovery
have a different purpose and consent boundary from usage analytics. Review the
checkpoint before launch, especially when changing workspace or identity. Never
assume that data approved for one organization is approved for another.

Diagnostic bundles are a separate versioned export class. They contain only
application/schema versions, OS family/architecture, coarse feature state, safe
service/database/vault health, current diagnostic settings, and stable
component/error aggregates. They exclude identity/workspace/project labels,
paths, usage values, session/Codex/repository content, credentials, command
arguments, analytics, checkpoints, and configuration payloads. Preview and
confirmation never authorize an upload, issue creation, telemetry, or replacing
an existing destination.

Portable configuration is a third, separate versioned export class. Its
allowlist is profile aliases/display names, approved immutable configuration
pack content, alert thresholds, collection intervals, appearance, and optional
Project Aliases represented only by repository basename and alias. It never
contains authentication state, vault keys, Identity Homes, canonical paths,
local project IDs, telemetry identifiers or consent, service enrollment,
automatic update consent, notification detail, experiments, usage, histories,
sessions, raw content, credentials, pack assignments, or local overrides.
Import does not grant any excluded consent and creates profiles as Pending until
a local home is chosen and installed Codex completes authentication.

## Optional telemetry boundary

Telemetry schema v1 permits only the application version, OS family and
architecture, coarse feature and outcome, registered stable error code,
duration bucket, and a resettable random installation ID. It cannot represent
identity, workspace, project, paths, usage, sessions, Codex content,
credentials, command arguments, or raw payloads. The ID value is never returned
by the browser API, CLI status, diagnostics, or portable configuration.

Consent is versioned and independent of notifications, service enrollment,
updates, generic confirmations, and configuration import. Revoke takes effect
locally before any best-effort remote cleanup. Reset replaces the local random
ID before queuing deletion of the prior ID. The public policy is 30 days for
individual events and 13 months for anonymous aggregates, but this development
build makes no production retention or deletion claim because no backend is
deployed. Missing evidence for any prerequisite keeps collection unavailable.

## Public reports

Do not upload `auth.json`, Identity Home directories, private transcripts, local
SQLite files, vault data, environment files, or unreviewed diagnostic bundles.
Replace account identifiers, paths, and code with synthetic examples. Even a
redacted scanner report can contain author names, file paths, and commit metadata.

Report suspected vulnerabilities through [Security](../../SECURITY.md). Revoke or
rotate an exposed credential before removing visible copies. Deleting a public
issue, commit, or release cannot retrieve copies that others already obtained.
