# Comprehensive usage guide

This guide describes the implemented development-preview workflows for testers,
contributors, and technical evaluators. For a quick first run, use the shorter
[Usage guide](USAGE-GUIDE.md).

## Preview boundaries

CodexFolio is a local-first companion to an independently installed Codex
executable. It is not a replacement Codex client, provider proxy, billing
control, or identity-switching mechanism for a running process.

The current preview includes:

- local Identity Profiles with Managed or Referenced Identity Homes;
- Codex-owned browser and device-code authentication;
- deterministic profile selection and foreground Codex launch;
- normalized usage evidence, activity, projects, and analytics;
- repository-first checkpoints and Safe Continuation handoffs;
- reviewed Shared Configuration Packs;
- an authenticated, loopback-only dashboard with Overview, Profiles, Sessions,
  and Capacity/Tokens/Projects/Models/Activity/Compare Analytics flows.

Some dashboard destinations remain placeholders, persistent native service
installation is not yet an end-user feature, and no supported binary release
channel exists. Read [Compatibility](COMPATIBILITY.md) before using real
identities and [Privacy](PRIVACY.md) before exporting data.

## Build and identify the binary

The pinned development toolchain is recorded in `.tool-versions`, `.nvmrc`,
`go.mod`, and `web/package.json`. The authoritative versions and platform notes
are in the [build guide](../development/BUILDING.md).

From a clean checkout:

```sh
node scripts/verify.mjs
./build/bin/codex-folio version --json
./build/bin/codex-folio --help
```

Windows PowerShell uses `./build/bin/codex-folio` as
`.\build\bin\codex-folio.exe`. The canonical verifier installs locked frontend
dependencies and builds the native development executable. Its compile-only
cross-target checks do not qualify native runtime behavior on those platforms.

When reporting a bug, include the version JSON, operating system, architecture,
installed Codex version, exact command, stable error code, and a sanitized
expected-versus-actual description.

## State, service ownership, and vault mode

CodexFolio keeps application state in the platform-specific user data location.
Use `--state-root ABSOLUTE_PATH` to isolate a test. Supply the same override to
every command that should share that state.

Only one process owns writable state at a time. Commands use the running service
when available or acquire the state owner directly when it is stopped. Do not
copy an active database, run two different state owners, or edit runtime files.

Inspect or start the foreground owner:

```sh
./build/bin/codex-folio service status --json
./build/bin/codex-folio service start
```

`service start` prints a fresh one-time dashboard URL and waits in the
foreground. Press `Ctrl-C` for an orderly stop. A second invocation discovers an
existing owner, prints a new dashboard URL, and exits.

Supported vault modes are platform Secret Service and passphrase mode:

```sh
./build/bin/codex-folio service start --vault-mode secret-service
./build/bin/codex-folio service start --vault-mode passphrase
```

Bare commands on Linux select Secret Service. WSL and headless Linux users must
explicitly select `--vault-mode passphrase`; the application deliberately does
not infer it after `CF_VAULT_UNAVAILABLE`. The passphrase vault starts locked
again after a new service session. CodexFolio does not silently fall back to
plaintext storage.

**Known WSL/headless limitation:** the current CLI does not print a passphrase
prompt and does not disable terminal echo while reading it. The entered value is
therefore visible on screen and may be captured by terminal recording or
streaming tools. Do not use a shared or recorded terminal, never reuse another
password, and prefer an available Linux Secret Service or defer vault-dependent
testing until the input path is fixed. Never put a passphrase in shell history,
source control, an issue, or a shared log.

Keep the passphrase-backed service running while testing. Other commands then
route through that state owner. If no service is running, append
`--vault-mode passphrase` to each vault-dependent command and enter the same
passphrase, subject to the terminal-echo limitation above.

Recovery commands are intentionally separate from normal startup:

```sh
./build/bin/codex-folio service recovery verify
./build/bin/codex-folio service recovery list
./build/bin/codex-folio service recovery restore --candidate CANDIDATE_ID
```

Inspect candidates before restoration. Preserve the displaced state reported by
the command until the restored database has been verified.

## Discover installed Codex

```sh
./build/bin/codex-folio codex discover --json
./build/bin/codex-folio codex discover --codex-bin /absolute/path/to/codex --json
```

Discovery searches `PATH`, runs a non-mutating version check, and rejects
ambiguous, relative, or invalid explicit executables. CodexFolio never downloads,
replaces, or upgrades Codex.

Use `--codex-bin` on profile authentication, reauthentication, launch, or
handoff when the intended executable is not the unambiguous `PATH` result.

## Identity Profile lifecycle

An Identity Profile combines a stable local ID, CLI Alias, display metadata,
Identity Home policy, authentication state, and optional configuration
assignment. It does not create or modify a remote identity, workspace, billing
account, or quota.

### Add a Managed Identity Home

Managed homes are isolated and app-owned. They are the default for a new profile:

```sh
./build/bin/codex-folio profile add work --browser
./build/bin/codex-folio profile add work-device --device-code
```

The installed Codex executable owns authentication. Browser authentication may
open or direct you to Codex's flow. Device-code mode may return a terminal step.
CodexFolio does not accept credentials through the dashboard.

Setup stages are persisted. If discovery, home preparation, authentication, or
validation is interrupted, the profile stays Pending and is neither selectable
nor launchable. Run the same add flow or open the Pending entry in Profiles to
resume and revalidate it.

### Register a Referenced Identity Home

Use an absolute existing home only when you understand its contents and ownership:

```sh
./build/bin/codex-folio profile add existing \
  --identity-home /absolute/path/to/existing/codex-home \
  --browser
```

Referenced registration does not copy, own, quarantine, or delete external
files. Duplicate login/workspace metadata may produce a warning while the local
profile remains distinct. Do not point two concurrently active processes at a
home unless Codex itself supports that use safely.

### List, edit, and select

```sh
./build/bin/codex-folio profile list
./build/bin/codex-folio profile edit work \
  --display-name "Work account" \
  --email "user@example.test" \
  --workspace "Example"
./build/bin/codex-folio select work
```

Email and workspace values are local display metadata. Selection controls the
default for future interactive launches and the initial single-profile dashboard
scope. Combined dashboard scope is presentation-only and does not change the
Selected Profile. Selection never changes a running Launch Profile.

Aliases follow the CLI's validation and uniqueness rules. Prefer short,
shell-friendly aliases because they appear in terminal handoffs.

### Reauthenticate

```sh
./build/bin/codex-folio profile reauthenticate work --browser
./build/bin/codex-folio profile reauthenticate work --device-code
```

Reauthentication reuses the established Identity Home. It cannot silently move
the profile to a different home. The profile remains unavailable until Codex
authentication and validation succeed.

### Remove, restore, and purge

These commands affect local CodexFolio registration, not the remote account:

```sh
./build/bin/codex-folio profile remove work --confirm work
./build/bin/codex-folio profile restore work
./build/bin/codex-folio profile purge work --confirm work
```

Removal can require a replacement when the target is selected and is blocked by
protected running-launch conditions. Managed homes enter a seven-day quarantine
before purge; restore reverses quarantine while eligible. Referenced removal
unregisters the profile and leaves external files untouched. Read the command's
preview and error before retrying a blocked lifecycle action.

The Profiles dashboard exposes the same service-enforced lifecycle. Open
**Removal and recovery**, review the local-only effect, type the exact CLI Alias,
and choose a ready replacement when removing the Selected Profile. Quarantined
Managed profiles show their recovery deadline with **Restore** and explicit
**Purge permanently** actions. Cancellation and mismatched confirmation leave
profile state unchanged.

## Dashboard operation and security

Start the service and copy its printed URL into a local browser:

```sh
./build/bin/codex-folio service start
```

The service binds loopback only. The URL contains a single-use bootstrap token
that is exchanged for a short-lived browser session and then removed from the
address bar. Browser mutations require the authenticated session, same-origin
Host/Origin checks, and CSRF protection. Production assets are embedded and do
not load scripts, styles, or fonts from a CDN.

Do not publish, message, log, or bookmark bootstrap URLs. If authorization is
expired or already used, run `service start` again to receive a new link.

Implemented dashboard flows include:

- **Overview:** selected or explicit combined scope, current/last-known capacity,
  evidence age and provenance, eligible alternatives, refresh, and a shared
  Project Identity launch view with terminal handoff and lifecycle status;
  repository-first Safe Continuation capture, evidence review, redaction,
  optional per-handoff transcript candidate review, revision-bound sanitized
  approval, cancellation, retained-state management, and fresh-target lifecycle
  tracking;
- **Profiles:** safe inventory, Managed/Referenced onboarding, Pending resume,
  local metadata edits, selection, installed-Codex discovery, and
  reauthentication, local removal, managed quarantine recovery/purge, and the
  same launch view for eligible profiles; Shared Configuration Pack version
  health, bounded declarative drafts, explicit approval/assignment, projection
  conflict review/application, and reviewed promotion.
- **Sessions:** one metadata timeline with profile/project/date/record-type
  filters, independent launch and observed details, source/provenance and
  correlation confidence, keyboard return, and narrow-screen disclosures.
- **Analytics:** selected-profile Capacity, Tokens, Projects, Models, and
  Activity tabs with shared profile/history/project filters, native charts
  backed by equivalent semantic tables, explicit unsupported states, safe
  Project Alias editing, and an explicit Compare view that keeps identities,
  subscription windows, evidence state, source, provenance, and API
  credit/spend units separate.

The browser never receives Identity Home IDs/paths, raw Codex authentication
output, the service command credential, vault material, or raw provider payloads.
Device-code work that needs terminal interaction is returned as an explicit
local command.

### Sessions timeline

Open **Sessions** (under **More** on narrow screens). Filters affect the timeline
only; they do not change Selected Profile. Date ranges apply to launch record creation or observed start
instants in your OS display zone; custom from/through dates are inclusive.
All dates are shown by default. Longer results have keyboard-accessible pages.
Filters survive details and navigation within the authorized page.

**Reload timeline** reads retained service metadata; it does not run collection.
Use the existing `activity refresh PROFILE` CLI workflow to collect supported
local metadata. A failed reload keeps the previous records and their original
timestamps. Recorded history has unknown current-source freshness. No matching
records or missing fields do not mean zero activity.

Details use Project Aliases/basenames and retain separate facts: a Managed
Launch owns its immutable Launch Profile, preparation time, lifecycle and exit
status. Running confirms process start; abandonment does not prove a normal exit;
an Observed Session owns its source start/last-observed timestamps, model and
token count when available. Last observed is neither a process exit nor a live
heartbeat. Tokens are locally derived metadata; absent model/tokens remain
unavailable, while a recorded zero remains zero.

Only existing explicit source-session evidence establishes related records.
Uncorrelated, ambiguous and contradictory states remain visible without an
invented relationship. Even supported correlation is not causation. No raw
commands, transcripts, prompts/responses, tool output, raw diffs or canonical
project paths enter these views. **Back to Sessions** restores the originating
row's focus and filters.

## Foreground launch

```sh
./build/bin/codex-folio launch work --
./build/bin/codex-folio launch work -- --help
./build/bin/codex-folio launch work --codex-bin /absolute/path/to/codex -- --model MODEL
./build/bin/codex-folio launch work --project PROJECT_ID -- --model MODEL
```

Arguments after `--` are passed to installed Codex. CodexFolio validates the
profile and launch plan, then keeps the child in the foreground with inherited
standard input/output, native signals, working directory, and exit status.
`--project` resolves an existing Project Identity through the command-authenticated
local service and uses its canonical repository location without exposing that
path to the browser.

The dashboard only prepares the explicit terminal command. Before that command
runs, closing the launch view creates no activity. The view reports Prepared,
Running, Exited (including nonzero status), or Failed from service activity; it
does not infer quota exhaustion from an exit code. Changing Selected Profile
does not mutate a launch already associated with another profile.

For Safe Continuation, choose **Prepare Handoff** beside an eligible alternative.
The browser asks the service to capture the registered Project Identity; it
never sends an arbitrary repository path, executable, or command. Review and
edit Goal, Completed Work, Pending Work, Known Validation, Risks, and Next
Action. Repository branch/head/diff evidence includes provenance and
completeness, and validation evidence keeps its source, freshness, timestamp,
and exit status when known. Select repository paths or enter exact text to
redact, then save the sanitized draft. Any edit creates a new revision, so a
stale approval is rejected.

Transcript assistance is a separate, off-by-default path inside the handoff
view. Consent applies only to the entered Codex thread ID and is not remembered.
Until **Review transcript candidates** is selected, no thread history is read.
The service resolves the source Identity Home and installed Codex internally,
extracts only bounded candidates for the six handoff fields, and returns those
for transient editing. The browser never receives the Identity Home, command
credential, arbitrary files, raw archive, raw transcript, commands, or tool
output. Select **Review sanitized preview** after editing/redaction, then use
the distinct **Approve sanitized preview** action. Only that approved sanitized
revision is persisted or transferred. Canceling or an unavailable history
source restores the unchanged repository-first draft.

Approval is available only when the managed source is definitively exited and
the target is currently eligible. Running or uncertain source state blocks it.
After approval, copy the displayed `codex-folio handoff` command into a terminal.
The browser remains at **Approved · Terminal launch not started** until a real
foreground process starts. The terminal path repeats the current repository,
source-exit, target Identity Home, authentication, usage, checkpoint revision,
and expiry checks before launch. Capacity may be stale or unavailable and is
shown as cautionary evidence rather than invented certainty. Canceling the
browser review creates no launch.

Launching an explicit alias does not mutate Selected Profile. Starting another
profile does not stop or switch an existing Codex process. Avoid concurrent
launches against the same Identity Home unless the underlying tool supports it.

## Usage evidence

```sh
./build/bin/codex-folio usage refresh work
./build/bin/codex-folio usage show
./build/bin/codex-folio usage show --combined
```

Refresh uses supported read-only Codex sources and stores allowlisted normalized
observations. The output distinguishes provider-reported, locally derived,
estimated, and observed evidence. Availability states such as unsupported,
stale, partial, contradictory, reauthentication-required, and collection-failed
must not be interpreted as zero.

Combined output keeps quotas separated by profile; it does not pool capacity.
After refresh failure, last-known observations retain their original timestamps
and should be treated according to their displayed age.

## Projects and activity

Resolve a repository to its app-local Project ID and optional alias:

```sh
./build/bin/codex-folio project resolve . --alias SampleProject
./build/bin/codex-folio project list
./build/bin/codex-folio project edit PROJECT_ID --alias RenamedProject
./build/bin/codex-folio project reconcile PROJECT_ID /current/repository/path
```

Ordinary output prefers project aliases or basenames rather than exposing full
canonical paths.

Refresh and inspect the metadata activity timeline:

```sh
./build/bin/codex-folio activity refresh work
./build/bin/codex-folio activity list --profile work
./build/bin/codex-folio activity list --project PROJECT_ID
```

Managed Launches and Observed Sessions remain distinct record types. Correlation
metadata is evidence, not proof that two records are the same process.

## Analytics, retention, and export

Open **Analytics** (under **More** on narrow screens) for retained usage history.
**Capacity** starts in the selected profile scope and can request 30 days, 90
days, 13 months, or all retained history. The provider-window filter changes
which compatible percentage series are shown; it does not combine limits or
reinterpret their provider reset boundaries. Missing months remain explicit
gaps. The chart and its semantic table use the same filtered samples, and table
rows become disclosures on narrow screens.

**Compare** is an explicit Combined Identity View. It does not change Selected
Profile and never pools quotas. Each profile retains its own availability,
evidence age, source, provenance, capture time, and provider windows. API
credit/spend appears only when the service has compatible currency or credit
metrics; ordinary token or percentage measurements are not relabeled as money.

**Tokens**, **Projects**, **Models**, and **Activity** reuse the same profile,
history-range, and Project Identity filters. Tokens remain attached to their
Observed Session records and are not silently summed across overlapping
evidence. Models report only supported locally observed metadata—not provider
capability or task suitability. Activity keeps Managed Launch and Observed
Session rows separate and carries source, provenance, lifecycle, and correlation
state. An absent dimension is shown as unsupported or unavailable, never zero.

Projects uses the encrypted app-local identity mapping but sends only Project
Aliases and basenames to the browser. Choose **Edit Project Alias** to change the
ordinary display name. This does not move the repository, change its canonical
mapping, or write CodexFolio artifacts into it. Historical rows resolve the
current alias by Project ID.

History reloads read the local service database; they do not collect new
provider evidence. Use **Refresh** or `usage refresh` for supported collection.
Ordinary history views receive only the service's allowlisted aggregate
projection—no Identity Home paths, workspace names, raw provider payloads, or
authentication material.

Choose **Local analytics data** from any Analytics tab to use the browser flows:

- **Preview analytics export** selects normalized usage, availability,
  aggregate, or activity datasets. JSON can include several datasets; CSV is
  limited to one. The preview names every included field and count. Download
  serializes that retained preview response rather than querying again, so the
  downloaded rows correspond to the reviewed counts. Project Alias and basename
  are the default; canonical project paths require the explicit checkbox.
- **Manage retention** reads and saves the 13-calendar-month default, an
  explicit interval of at least 30 days, or unlimited retention. An optional
  bounded maintenance run processes at most the existing per-class batch. It
  does not enroll a scheduler, purge aggregates, reinterpret provider windows,
  or change diagnostics and checkpoint retention.
- **Preview scoped purge** requires profile, project, From, To, and record-class
  dimensions. The dry run shows counts and the 1,000-record atomic limit. Type
  its exact confirmation only after reviewing the result. A changed scope or a
  changed matching record set invalidates the preview, and the service rejects
  stale confirmation instead of widening deletion.

Cancelling or failing these flows does not create an analytics download or
delete data. Browser download destinations remain user-controlled; CodexFolio
does not upload data or choose an existing file to overwrite. Analytics export
remains separate from portable configuration, diagnostics, and encrypted
continuation checkpoint export. Identity Home paths, credentials, raw provider
payloads, conversation/tool content, commands, diffs, diagnostics, and vault
material are never analytics export fields.

Use `--json` where machine-readable output is needed. Review a dry run before
purging or exporting:

```sh
./build/bin/codex-folio analytics aggregates \
  --profile '*' --project '*' --from all --to all

./build/bin/codex-folio analytics export \
  --format json \
  --datasets usage,availability,aggregates,activity \
  --scope selected_profile \
  --profile selected --project '*' --from all --to all \
  --dry-run

./build/bin/codex-folio analytics purge \
  --profile PROFILE_ID --project '*' --from all --to all \
  --classes usage,aggregates,observed_sessions,managed_launches,checkpoints \
  --dry-run
```

Export requires an explicit output path for the real write. Canonical paths are
excluded unless `--include-paths` is explicitly supplied. Purge returns a
confirmation token bound to the scoped record set; use the documented
`--confirm TOKEN` form only after checking the scope and record counts. If
matching data changes, preview again to obtain a fresh token.

Retention can be inspected or changed with:

```sh
./build/bin/codex-folio analytics retention
./build/bin/codex-folio analytics retention 90
./build/bin/codex-folio analytics retention 90 --run
```

## Shared Configuration Packs

Configuration Packs are immutable reviewed versions projected into profile
homes. A minimal lifecycle is:

```sh
./build/bin/codex-folio configuration-pack create team 1.0.0 \
  --file AGENTS.md=@/absolute/path/to/AGENTS.md
./build/bin/codex-folio configuration-pack approve team 1.0.0
./build/bin/codex-folio configuration-pack assign work team 1.0.0
./build/bin/codex-folio configuration-pack preview work
./build/bin/codex-folio configuration-pack project work
```

Local overrides remain profile-local. Promotion is a reviewed operation:

```sh
./build/bin/codex-folio configuration-pack promotion-preview work 1.0.1
./build/bin/codex-folio configuration-pack promote work 1.0.1 --reviewed
```

Preview before projection or promotion. Packs are configuration, not credentials;
do not place tokens, private keys, or authentication files in them.

The Profiles dashboard exposes the same immutable pack service through a
browser-safe contract. Browser-created drafts deliberately support only three
fixed document slots: `config.toml`, `AGENTS.md`, and `plugins.lock`. The
browser cannot supply arbitrary paths, read existing Identity Home content, or
receive document content from stored versions.

Assignment requires a Ready Profile with a Managed Identity Home and an
explicitly reviewed approved version. **Preview projection** lists every target
path and any profile-local conflict before **Apply reviewed projection** becomes
available. The application is bound to the preview digest; a changed pack or
override requires a fresh preview. Conflicting local files remain in place.

**Preview promotion** similarly binds approval to the reviewed content digest.
Creating the promoted version is explicit, immutable, and does not reassign the
Profile. Cancelling either review performs no write. A running Managed Launch
continues to block projection while leaving preview and local configuration
intact.

## Checkpoints and Safe Continuation

Capture repository-first context:

```sh
./build/bin/codex-folio checkpoint capture . \
  --goal "Finish the current change" \
  --completed-work "Implementation is present" \
  --pending-work "Run review and verification" \
  --next-action "Run the focused test"
```

The result includes a checkpoint ID. Inspect, sanitize, approve, or export it:

```sh
./build/bin/codex-folio checkpoint show CHECKPOINT_ID
./build/bin/codex-folio checkpoint review CHECKPOINT_ID
./build/bin/codex-folio checkpoint export CHECKPOINT_ID /private/destination.cfolio
```

Encrypted exports use `.cfolio`; explicit plaintext exports use `.json`.
Plaintext export requires its explicit option and confirmation. Review every
field and redaction before sharing a checkpoint across identities or workspaces.

The dashboard exposes the same retained data under **Settings → Safe
Continuation data**. Repository-first checkpoints default to 30 days and
transcript-assisted checkpoints to 7 days; configure either from one day to
`unlimited`. Completion remains retained until expiry or an exact purge. The
inventory distinguishes retained drafts, recoverable approvals, completed
handoffs, expired records, and uncertain starts. After a service restart, an
uncertain start is non-authorizing. Recovery is offered only when durable
storage, repository identity, definitive source exit, target eligibility,
revision, and expiry checks pass; CodexFolio never kills Codex.

Checkpoint export first displays included and always-excluded fields. The
default browser download is an independently passphrase-encrypted `.cfolio`
artifact. Plaintext JSON requires its own visible warning and acknowledgement.
Canceling creates no download. The browser controls the destination and handles
an existing filename, so the service neither discloses a destination path nor
silently overwrites a file. Purge likewise requires a preview, an exact current
revision, and typed confirmation; a launching or changed checkpoint is rejected.

Prepare a fresh foreground continuation under another eligible profile:

```sh
./build/bin/codex-folio handoff personal . \
  --goal "Continue the reviewed repository task" \
  --next-action "Inspect the checkpoint, then resume"
```

The dashboard-approved form is revision-bound and is intentionally shorter:

```sh
./build/bin/codex-folio handoff personal \
  --checkpoint CHECKPOINT_ID \
  --revision APPROVED_REVISION
```

Safe Continuation checks source-process exit, repository identity, target
eligibility, approval, revision, and expiry before launch. It provides reviewed
repository context to a fresh Codex process; it does not promise exact transfer
of conversation history, hidden model state, permissions, or provider state.

## Optional shell integration

Generate inspectable completion or a named launch wrapper for your shell:

```sh
./build/bin/codex-folio shell generate --shell zsh
./build/bin/codex-folio shell generate --shell bash --wrapper
./build/bin/codex-folio shell generate --shell powershell --wrapper
```

The command prints the path and manual source instructions. CodexFolio does not
rewrite the Codex executable or silently edit shell startup files. Remove only
the generated integration with the matching command:

```sh
./build/bin/codex-folio shell remove --shell zsh
```

## JSON output and stable errors

Commands that support `--json` write structured data to stdout and diagnostics
to stderr. A nonzero exit status still matters; do not treat parsable JSON as
success by itself.

Errors include stable identifiers such as `CF_PROFILE_*`, `CF_USAGE_*`,
`CF_PLATFORM_*`, or `CF_CONTINUATION_*`. Preserve the identifier in bug reports,
but remove aliases, emails, workspace names, paths, command arguments, dashboard
URLs, and private repository content unless the field is explicitly safe.

The [CLI conventions](../development/CLI-CONVENTIONS.md) define stream, exit,
confirmation, and non-interactive behavior.

## First-time tester checklist

Use a non-sensitive repository and, preferably, a disposable state root.

1. Run canonical verification and record the exact version JSON.
2. Discover Codex, including an explicit override test if needed.
3. Start the service and confirm the dashboard URL works only once.
4. Add a Managed profile and confirm interrupted setup remains Pending.
5. Add a Referenced profile only with a disposable existing home.
6. Select a profile and confirm a running launch is not switched.
7. Refresh usage and inspect source, availability, and capture time.
8. Exercise browser reauthentication without entering credentials in the SPA.
9. Stop the service with `Ctrl-C` and confirm `service status` reports stopped.
10. Report only sanitized evidence.

Automated development checks use fake Codex and isolated SQLite/vault fixtures.
They do not establish live-provider, native screen-reader, native high-contrast,
signed-release, or every-platform runtime qualification.

## Troubleshooting

### Codex cannot be discovered

Run `codex discover --json`. If multiple or no candidates are found, supply one
validated absolute `--codex-bin` path. Confirm that the same user can execute the
binary and that its version command succeeds.

### The service cannot start

Run `service status --json`. Check whether another owner is running, whether the
state path is writable, and whether the selected vault mode is available. Do not
delete lock or database files to force startup; use the reported recovery path.

On WSL/headless Linux, `CF_VAULT_UNAVAILABLE` from a bare command normally means
the default Linux Secret Service is absent. Start with
`service start --vault-mode passphrase`, then keep that foreground owner running.
This selects the encrypted passphrase vault rather than plaintext, but the
current passphrase input limitation described above still applies.

### The dashboard link is expired

Run `service start` again for a fresh bootstrap URL. Do not weaken browser
security checks or reuse a captured link.

### A profile remains Pending

Open Profiles and resume it, or repeat its `profile add` command with the same
alias and intended home. Resolve the displayed discovery, authentication, or
validation error. Pending is a safety state, not a profile to force-select.

### Usage is unavailable

Check profile readiness, reauthentication state, Codex compatibility, and the
stable error code. Preserve last-known timestamps. Unsupported or missing source
data is not a zero balance.

### A lifecycle action is blocked

Read the exact error before retrying. A selected profile may need an eligible
replacement; a running launch can block removal; a managed home can still be in
quarantine; confirmation text or tokens may be stale.

## Safe reporting

Use [Support](../../SUPPORT.md) for ordinary problems and
[Security](../../SECURITY.md) for vulnerabilities. Never attach:

- dashboard bootstrap URLs or service command credentials;
- authentication files, tokens, cookies, vault contents, or passphrases;
- complete Identity Homes or canonical private paths;
- private transcripts, repository contents, or raw provider payloads;
- unreviewed analytics, checkpoint, or diagnostic exports.

Start with a synthetic reproduction and the stable error code. Add sensitive
detail only through an approved private support channel.
