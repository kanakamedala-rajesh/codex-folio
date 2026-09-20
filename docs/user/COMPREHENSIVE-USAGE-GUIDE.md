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

All six dashboard destinations are implemented: Overview, Profiles, Sessions,
Analytics, Alerts and Settings. Optional per-user native service enrollment is
implemented. No supported stable binary release channel exists, and implemented
features are not a claim of complete real-world or native-platform qualification.
Read [Compatibility](COMPATIBILITY.md) before using real identities and [Privacy](PRIVACY.md) before exporting data.

## Start from scratch: manual setup map

Use the [short guide](USAGE-GUIDE.md) for the ordered WSL commands. This reference
adds alternate-platform setup, existing history, full workflow checks and
troubleshooting. A fresh setup means a **new CodexFolio state root**, not deleting
Codex credentials or existing sessions.

1. Build with the pinned toolchain and identify the binary and installed Codex.
2. Stop the existing CodexFolio owner before switching state roots.
3. Choose an unused absolute directory outside the checkout and Codex homes.
4. Start that root, unlock its vault, and verify Profiles is empty.
5. Add one account at a time; verify sign-in account/workspace and Ready status.
6. Select a profile, refresh actual usage and inspect provider window details.
7. Register a test repository and run an actual foreground Codex session.
8. Collect activity from the same profile home; inspect Sessions and Analytics.
9. Follow the manual checklist below, recording failures and untested states.
10. Stop, restart the same root and verify persistence. Keep the old root intact
    when starting a separate fresh run.

### State-root discipline for every command

The short guide uses `CF_STATE="$HOME/codex-folio-manual-01"` in **each** WSL
terminal. This variable is only a shell convenience; the app does not read it
automatically. Every stateful CLI command needs `--state-root "$CF_STATE"`.
Reference examples later in this document omit that flag for readability and
otherwise operate on default state. Append it before running those examples
against your manual setup; for launch, insert it **before** the `--` separator.
Do not copy bare reference commands into an isolated run unchanged.

On Windows PowerShell, from the repository root, use these equivalents in both
terminals (Terminal A runs start; Terminal B runs unlock/status):

```powershell
$CF_STATE = Join-Path $env:USERPROFILE 'codex-folio-manual-01'
.\build\bin\codex-folio.exe service start --state-root "$CF_STATE" --vault-mode passphrase
```

```powershell
$CF_STATE = Join-Path $env:USERPROFILE 'codex-folio-manual-01'
.\build\bin\codex-folio.exe vault unlock --state-root "$CF_STATE"
.\build\bin\codex-folio.exe service status --state-root "$CF_STATE" --json
```

Use the Windows executable and native Windows paths for the remaining commands.
Do not mix a Windows service with WSL binaries/homes. On macOS, the short guide's
shell syntax applies with a native build and macOS paths. Passphrase mode makes
the explicit unlock steps consistent; platform-backed vault mode is an option
when its prerequisites are available, as described below.

### Expected empty starting state

After first unlock, Profiles should contain no registered identities, Sessions
no retained activity and Analytics no collected history. An empty state is not
an error. Adding a Managed profile creates a separate home; even a remote account
with extensive history will not automatically populate that new home with local
sessions. Registering a repository adds a Project Identity, not a session.
Usage refresh and activity refresh collect different evidence.

A fresh root does not reset remote quotas, change the account's plan or workspace,
copy default-home history, or enroll a native service. Foreground startup has no
periodic scheduler; refresh manually unless you explicitly test enrollment.

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

Only one process owns writable state per OS user, including across different
state-root overrides. Commands use the matching running service when available
or acquire the state owner directly when it is stopped. A different active root
is an ownership conflict, not a way to start a parallel isolated service. Do not
copy an active database, delete lock files, or edit runtime files.

Before switching roots, exit active foreground Codex sessions and stop an
on-demand owner with Ctrl-C in its service terminal. If the old owner is enrolled,
run `service uninstall --state-root /absolute/path/to/old-state` to remove that
enrollment, then inspect status and ensure its owner has stopped before starting
the new root. Uninstall is not a reset and retains data. If a foreground owner is
still running, stop it in its original terminal. Do not issue a blanket kill of
Codex processes.

Inspect or start the foreground owner:

```sh
./build/bin/codex-folio service status --json
./build/bin/codex-folio service start
```

`service start` prints a fresh one-time dashboard URL and waits in the
foreground. Press `Ctrl-C` for an orderly stop. A second invocation discovers an
existing owner, prints a new dashboard URL, and exits.

Optional persistent startup is explicit and per-user:

```sh
./build/bin/codex-folio service install --json
./build/bin/codex-folio service status --json
./build/bin/codex-folio service uninstall --json
```

`service install` writes and starts only CodexFolio's native user enrollment:
Task Scheduler on Windows AMD64, a LaunchAgent on macOS ARM64, or
`systemd --user` on Linux AMD64 and systemd-enabled WSL2. The enrolled command
uses the same executable and resolved state root and therefore reuses the same
owner lock. It never requests machine-wide privilege, edits shell/startup files,
or substitutes another autostart method. `service status` reports owner state
separately from `enrollment_state`, `enrollment_mechanism`, and
`enrollment_available`.

`service uninstall` removes only the native enrollment. It retains SQLite,
profiles, authentication, vault data, configuration, and foreground Codex work.
Repeated install/uninstall is safe and reports whether enrollment changed. When
the native mechanism is unavailable, status says so and on-demand `service
start` remains available.

### Periodic usage collection

Periodic collection runs only inside a service session started by an explicit
native enrollment. Merely opening the dashboard, running a CLI command, or
starting the foreground service does not enroll or enable the scheduler.
Passphrase-backed sessions do not start it until the vault is successfully
unlocked, and stop it again when the service session closes.

The defaults are five minutes while a Managed Launch is running and 30 minutes
while idle. Read or update the persisted values through **Settings → Periodic
collection schedule** or the generated-client-backed CLI surface:

```sh
./build/bin/codex-folio settings collection --json
./build/bin/codex-folio settings collection --active-minutes 10 --idle-minutes 60
```

Both values accept whole minutes from 5 through 1,440. The five-minute minimum
is CodexFolio's conservative provider-safe floor: the supported Codex App Server
contract documents `account/rateLimits/read`, update notifications,
`windowDurationMins`, and `resetsAt`, but publishes no polling cadence. See the
[official App Server documentation](https://developers.openai.com/codex/app-server/).
CodexFolio therefore will not accept a tighter cadence without a new documented
provider contract.

Known future reset timestamps can move the next collection to that reset
boundary, subject to the same floor. Missing or unknown reset metadata is
ignored rather than inferred. Ordinary intervals and retries use bounded
positive jitter. Failures retain their normalized evidence and use persisted
exponential backoff from five minutes up to six hours; a later success restores
the configured cadence. At most four profiles are processed per scheduler tick,
overlapping ticks and same-profile refreshes coalesce, and downtime does not
replay every missed interval.

The scheduler reuses the existing collector, SQLite writer, retention workflow,
owner lock, and native launch identity. It does not create a second daemon or
writer. An idle enrolled service wakes at one-minute resolution, performs no
provider call before a profile is due, and writes schedule state only when the
next attempt changes or a collection completes. The repository verification
suite exercises active, idle, reset, retry, restart, coalescing, lock, API, CLI,
and browser persistence paths; native runtime resource qualification remains
part of the platform evidence recorded for release qualification.

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

Passphrase-backed `service start` acquires the single state owner and publishes
the dashboard without opening the vault or database. Unlock the running service
from a second local terminal:

```sh
./build/bin/codex-folio vault unlock
./build/bin/codex-folio service status --json
```

`vault unlock` reads from a terminal with echo disabled and sends the value only
over the authenticated loopback command transport. It does not accept a
passphrase argument or environment variable, and the browser has no unlock
endpoint or passphrase field. Failed unlock keeps one locked owner; successful
unlock activates the state-owning workflows once. Every service restart returns
passphrase mode to locked state. Never put a passphrase in shell history, source
control, an issue, or a shared log.

Keep the unlocked passphrase-backed service running while testing. Other
commands then route through that state owner. If no service is running, append
`--vault-mode passphrase` to each vault-dependent command and enter the same
passphrase through that command's existing input path.

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
  --non-interactive --yes --state-root "$CF_STATE"
```

This example deliberately omits `--browser` and `--device-code`: automatic mode
first checks whether the referenced home is already authenticated and reuses it.
`--yes` accepts the referenced-home confirmation; `--non-interactive` avoids
interactive setup prompts. Check the owning account before registering it. If
reuse fails, inspect the diagnostic before intentionally starting a new sign-in.
Explicit browser/device flags request that authentication flow instead of the
initial automatic reuse path. In Profiles → Add Identity Profile, choose
Reference an existing Identity Home and **Use existing sign-in** to perform
that noninteractive check without opening a new login. If authentication is
unusable, the profile stays Pending; resume with the same alias/path and choose
an explicit sign-in method when needed.

For the usual WSL default home, substitute `"$HOME/.codex"` only if that is the
home you actually use; an existing custom Codex home may be elsewhere. Do not
change CODEX_HOME globally or copy auth files to make the example work. Register
this as an additional clearly named profile, not as proof that it belongs to
Work or Personal. Then collect its metadata:

```sh
./build/bin/codex-folio activity refresh existing --state-root "$CF_STATE"
./build/bin/codex-folio activity list --profile existing --state-root "$CF_STATE"
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

While a passphrase service is locked, the authenticated dashboard exposes only
browser-safe service, vault, and database state plus fixed CLI guidance. It does
not receive paths, vault metadata, recovery contents, provider errors, or the
command credential, and it does not invoke state-owning workflows. If database
open or migration requires recovery, stop the foreground owner and run the
displayed existing `service recovery` commands before starting again.

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
- **Alerts:** active operational conditions, bounded deduplicated history,
  acknowledgement, per-profile/per-window capacity thresholds, actionable
  guidance, privacy-preserving native delivery for explicitly enrolled
  services, separate per-device notification-detail consent, and delivery
  health. Overview consumes only decision-relevant notices from the same
  evaluator.

The browser never receives Identity Home IDs/paths, raw Codex authentication
output, the service command credential, vault material, or raw provider payloads.
Device-code work that needs terminal interaction is returned as an explicit
local command.

### Operational alerts

Alert evaluation consumes the same normalized usage, availability, source
version, capture-time, provider-window, profile authentication, and collection
failure state used by Overview and the scheduler. Manual and periodic refreshes
share that evaluation path; opening Alerts also evaluates time-sensitive stale
and approaching-reset state through the application clock. On-demand alerts do
not require native service enrollment.

The bounded set is: 20% warning and 10% critical remaining by default,
exhaustion, approaching provider-reported resets and reset-credit expiry when a
supported source supplies them, reauthentication, evidence older than ten
minutes, three consecutive collection failures, and source compatibility
changes. Configure capacity thresholds for one Identity Profile and normalized
provider window in **Alerts → Capacity thresholds**. The critical value must be
below the warning value. There is no rule language or arbitrary action engine.

Missing, partial, contradictory, future, unsupported, out-of-window, or
non-provider capacity data does not manufacture remaining capacity, an exact
reset, or credit expiry. Repeated observations update one durable condition;
acknowledgement does not hide that an active condition still exists. Conditions
resolve when current evidence no longer supports them and remain in bounded
history. Dashboard delivery is available locally. Native notification delivery
runs only inside the explicitly enrolled state-owning service. Windows uses the
user-session toast facility, macOS uses Notification Center, and Linux uses the
desktop notification facility when `notify-send` and a desktop session are
available. There is no alternate daemon or remote push service.

Native text is generic by default: it says that CodexFolio has an operational
alert and directs you to the dashboard. **Alerts** and **Settings** expose the
same per-device **Notification privacy** preference. Detailed text may contain
the Identity Profile alias, remaining capacity, and guidance, so enable it only
when those values may appear on shared or locked screens. Enabling or revoking
detail does not install or uninstall the service, enable update checks or
telemetry, or change alert thresholds. Portable configuration does not carry
this consent.

Each new or reopened condition has one durable delivery lifecycle. The service
claims a bounded notification before calling the OS, preserves successful
delivery across repeated observations, and uses bounded retry intervals for
known failures. A crash after the durable claim is treated conservatively as an
uncertain failed delivery rather than risking a duplicate flood. Native facility
unavailability or failure is shown in delivery health and never disables alert
evaluation, the dashboard, on-demand collection, or foreground Codex.

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

## Portable nonsecret configuration

The dashboard **Settings → Portable configuration** and the `configuration`
CLI move only schema-v1 allowlisted settings. Export first returns an exact
field inventory, record counts, exclusions, bundle, and confirmation digest.
Writing requires that digest and an unused destination:

```sh
./build/bin/codex-folio configuration export --json
./build/bin/codex-folio configuration export \
  --write --output ./codex-folio-configuration-v1.json \
  --confirmation-digest DIGEST
```

Add `--include-project-aliases` to both export commands to carry optional
Project Aliases. Those records contain only a repository basename and alias;
ambiguous basenames are omitted, and canonical paths and local project IDs are
never exported.

Import is also preview-first:

```sh
./build/bin/codex-folio configuration import --input ./codex-folio-configuration-v1.json --json
./build/bin/codex-folio configuration import --input ./codex-folio-configuration-v1.json \
  --apply --reviewed --confirmation-digest DIGEST \
  --resolve profile:work=keep_local
```

Resolve every reported conflict with only one of its offered choices:
`keep_local`, `use_imported`, or `skip`. A malformed or unsupported bundle, an
unresolved conflict, a stale digest, or cancellation leaves local state
unchanged. The imported allowlist is profile aliases/display names, approved
immutable Shared Configuration Pack versions, alert thresholds, collection
intervals, appearance, and optional path-free Project Aliases. Pack versions
remain immutable and are not assigned or projected automatically.

Authentication, vault keys, Identity Homes, canonical paths, telemetry IDs and
consent, service enrollment, automatic update checks, notification detail,
experiments, usage/history/session/raw content, credentials, pack assignments,
and local overrides never transfer. Every imported profile is created Pending,
unselected, and without a home or authentication state. Resume it in Profiles,
choose a local Managed or Referenced Identity Home, and complete installed-Codex
authentication before it can become Ready, selected, or launched.

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

## Local diagnostics and support bundles

Local diagnostics are enabled by default at `info` level, retained for 14 days,
and bounded to 50 MB and 128 aggregate records. Collection can be disabled, and
the severity floor and retention can be changed independently without enrolling
the background service:

```sh
./build/bin/codex-folio diagnostics settings --json
./build/bin/codex-folio diagnostics settings --enabled false --level error --retention-days 7
```

Retention accepts 1–30 days. Disabling collection or raising the severity floor
does not enable another data source. Expired records are removed in bounded
store operations.

Use `diagnostics export --dry-run` to inspect the included field inventory,
record count, encoded size, and confirmation digest. For an interactive export,
provide `--output FILE`, review that preview, and type its exact digest. An
automation caller must use `--confirm DIGEST --non-interactive` against the same
running state-owner preview. Existing destinations are never replaced.

The versioned JSON contains application/schema versions, OS family and
architecture, coarse feature enablement, safe service/database/vault health,
the current diagnostic controls, and stable component/error aggregates. It
excludes identities, workspaces, projects, canonical/home paths, usage values,
sessions, Codex or repository content, credentials, command arguments,
analytics, checkpoints, and configuration payloads. CodexFolio neither uploads
the bundle nor opens a support issue.

## Consent-gated update checks

Update network access is off by default. The dashboard's **Check for updates**
action and `codex-folio updates check` perform one bounded manifest request;
neither action enrolls future checks or enables telemetry. Automatic checks use
a separately persisted preference:

```sh
./build/bin/codex-folio updates status --json
./build/bin/codex-folio updates check
./build/bin/codex-folio updates settings --automatic true
./build/bin/codex-folio updates settings --automatic false
```

When enabled and the explicitly enrolled background service is running, a persisted due time limits
successful checks to once per 24 hours and failed checks to a one-hour retry.
Revocation prevents later automatic requests. The manifest contract is strict,
bounded JSON over HTTPS. Both the manifest URL and download path must match the
compiled project-controlled allowlist; redirects, unknown fields, malformed
versions, and off-allowlist destinations fail closed. CodexFolio fetches only
metadata. It never downloads or opens the reported artifact, replaces the
running executable, runs an installer, updates Codex, or claims signing or
deployment verification.

This development build intentionally has no configured production release
endpoint. Status and checks therefore report `unconfigured` without disrupting
local features. Recording HTTPS fixtures qualify the adapter behavior but are
not evidence of a live production update service.

## Optional telemetry client

Telemetry is off by default and uses a separate schema-versioned consent:

```sh
./build/bin/codex-folio telemetry status --json
./build/bin/codex-folio telemetry schema
./build/bin/codex-folio telemetry enable --schema-version 1
./build/bin/codex-folio telemetry revoke
./build/bin/codex-folio telemetry reset-id
```

Enablement requires a project HTTPS endpoint, the public schema and privacy
notice, operational 30-day individual-event and 13-month anonymous-aggregate
retention jobs, and deletion/reset operations. The production adapter provides
none of these, so status is `unavailable` and enable fails closed. Recording
adapters exist only to verify the real service path without deploying a backend.

Schema v1 contains only application version, OS family/architecture, coarse
feature and outcome, a registered stable error code, duration bucket, and a
random resettable installation ID. Identity, workspace, project, paths, usage,
sessions, Codex content, credentials, command arguments, and raw payloads are
not representable. Consent and the ID live outside portable configuration;
browser, CLI, and diagnostics report only whether an ID exists, never its value.
Collection uses bounded non-blocking queues, so an unavailable or failing
telemetry service cannot block local workflows. Revoke and reset invalidate
pending events immediately and queue best-effort deletion of the prior ID.

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

## Manual validation checklist

Start with a fresh root and real accounts as described above. Use 1024×768 and
1440×1000 browser viewports for the desktop pass. These are viewport dimensions,
not necessarily the monitor resolution. Keep a log with one row per action:

| Page/control | Profile and precondition | Steps/input | Expected | Actual | Result | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| Example: Overview Refresh | Work, Ready | Click Refresh, open details | Fresh source/window evidence | Fill after testing | Pass / Fail / Blocked / Not tested | Screenshot/time/version |

Record the selected profile separately from dashboard/filter scope, plus the
binary version, browser, viewport, local time and relevant source timestamp.
Capture the exact page/dialog after the action. A screenshot of a different
scope or earlier state is not evidence for the action. Redact credentials,
bootstrap URLs and private repository/session content before sharing.

### A. Establish real account and data expectations

- Confirm each sign-in's account and workspace before testing. Local aliases,
  display names and workspace labels do not authenticate that relationship.
- For the supplied test accounts, expect Work weekly-only, Personal five-hour
  plus weekly, and personal-free monthly-only. Confirm provider duration/reset
  metadata; these expectations are not universal plan rules.
- Keep same-workspace identities separate in Compare. Do not add their remaining
  percentages or infer a shared allowance from their labels.
- First collect actual usage. Then run a short real session in a registered test
  repository and collect activity. Empty managed-home history is expected before
  that; reference an existing authenticated home only if you want its history.

### B. Page-by-page functional checks

Perform each listed action separately, recording its own outcome. For a save,
reopen or reload the view to check persistence, then restore the original value
when the change was only a test. For Cancel, verify the prior value remains.

| Surface | Actions to exercise | Expected behavior to compare |
| --- | --- | --- |
| Shared navigation | Open all six pages; use keyboard Tab/Shift-Tab, skip link and Enter; try Back/reload; change dashboard scope | Correct page/focus, usable controls, truthful authorization/re-entry guidance; scope does not silently switch a running launch |
| Overview | Select each scope; Refresh; Open details; move history selector; dismiss guidance; Open Alerts; inspect alternatives | Correct account/source/window/time, explicit missing data, corresponding history values; do not treat Unsupported as zero or a second limit |
| Profiles | Add/resume; expand details; edit/save/reopen; edit/cancel; Select; reauthenticate intentionally | Ready/Pending states reflect setup, local metadata persists, cancellation preserves values, future selection is explicit |
| Shared Configuration Packs | Create a small nonsecret draft; inspect exact documents/digests; approve; assign; preview projection; test conflict review | Approval/version/assignment are distinct; preview identifies changes and preserves local conflicts. Apply only to a disposable managed test profile |
| Sessions | Reload; profile/project/date/type filters; empty result; next/previous pages; open both record types; return and inspect filter retention | Records match filters and inclusive display dates; Managed Launch and Observed Session remain distinct; detail is metadata-only |
| Analytics | Open Capacity, Tokens, Projects, Models, Activity, Compare; change filters; compare each chart with its table | Correct scope and units, explicit missing metadata, no summed incompatible quotas; include a single-month chart check |
| Analytics management | Project alias save/cancel; JSON/CSV export preview/cancel/download; retention save/reopen; purge preview/cancel | Alias persists only on save; actual file matches chosen format/fields; paths excluded unless requested; no deletion from preview/cancel |
| Alerts | Active/History; keyboard tabs; thresholds invalid/valid/save/reopen; acknowledge an actual active alert | Invalid thresholds cannot save, history remains truthful, acknowledgement does not conceal a still-active condition. No active alert means acknowledgement is not yet tested |
| Settings | Light/Dark/System and reload; collection intervals invalid/valid/save; notification privacy; diagnostics preview/cancel/download | Changes persist independently; invalid values are rejected; downloaded JSON is an actual file, not just success feedback |
| Settings data | Configuration export and import preview/cancel; checkpoint inventory/refresh, retention and export preview/cancel | Correct counts and exclusions; Cancel does not apply; each checkpoint identifies its state/revision. Import apply and plaintext export are separate choices |
| Updates/telemetry | Manual update check; inspect separate automatic preference; expand telemetry prerequisites/schema | Manual check does not enable automatic checks; unconfigured production endpoints/telemetry remain explicitly unavailable |

For downloads, inspect the resulting file locally and compare its selected
format and fields with the preview. A browser toast alone does not establish a
download. Keep raw exports private unless reviewed for sharing.

### C. Launch and handoff end to end

1. Register a test repository and choose its Project Identity in Launch Codex.
2. Confirm Prepared / Not started before running the displayed terminal command.
   Keep the command's state root and other arguments intact.
3. Run it in a local terminal. Confirm the intended working directory/account,
   complete a short actual interaction and observe Running then Exited. Exit
   status alone does not establish quota exhaustion.
4. While a launch is running, select another profile and verify the existing
   launch retains its original profile. Exit the source normally before handoff.
5. Prepare Handoff to a different Ready account for the same test repository.
   Review Goal, Completed Work, Pending Work, Known Validation, Risks and Next
   Action; exercise path and exact-text redaction, save and inspect the revision.
6. Approve only once the source is definitively exited. First test Cancel before
   terminal execution and verify no target process starts.
7. Prepare/review/approve again and run the displayed terminal command to test
   actual continuation. Verify the target account, repository and checkpoint
   context in the new foreground session. A prepared command is not a completed
   handoff.
8. Inspect Settings → Safe Continuation data and the final checkpoint state.
   If a start is uncertain, follow recovery guidance; do not force a second launch.

Transcript assistance is an additional, explicit per-handoff consent flow.
Use a session you are willing to expose to that workflow; test candidate review,
redaction, cancel and approval separately. Repository-first handoff does not
establish that transcript assistance works.

### D. Destructive, native and unavailable-state checks

Use disposable profiles/homes, nonsecret packs and test data for actual profile
removal/replacement, quarantine/restore/purge, referenced unregister, pack
projection and configuration-import conflict resolution/apply. A preview or
disabled confirmation is only a guard check, not proof that Apply works. Never
use the normal Codex home to test deletion or deliberately corrupt real state.

Test lock/restart with the chosen test root; each new passphrase service session
starts locked and needs unlock. Recovery/corruption and deliberate transport
failures need a separately prepared disposable environment. Record them as not
tested if you have not created the required state.

Native enrollment, notifications and OS credential integration need the actual
platform. Windows browser screen readers and browser-native 200% zoom can be
checked manually even when the service runs in WSL. Record the browser/reader
and actual method; changing viewport or CSS scale is not equivalent to native
zoom or spoken-reader testing. Do not label unavailable checks Pass.

Finally restart the same root, unlock, obtain a new dashboard entry and verify
profiles, settings and history persist. Restore any test preferences and record
retained test profiles, sessions, files and checkpoints.

### Current known issues relevant to manual testing

The audit identified historical-data capacity conflicts, Alerts keyboard/error
handling, a missing isolated chart marker, referenced-home authentication reuse,
and narrow layout/focus defects. Corrections passed isolated regression checks; see the [sanitized audit summary](../validation/milestone-5/REPORT.md)
for their status and remaining qualification limits. A fresh root is a clean
baseline, not a milestone qualification claim. Never include credentials, private
session content or account screenshots in a shared test report.

Automated development checks use fake Codex and isolated SQLite/vault fixtures.
They do not establish live-provider, spoken-reader, native high-contrast,
signed-release or every-platform runtime qualification.

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
This selects the encrypted passphrase vault rather than plaintext. In another
local terminal, run `vault unlock`; a failed attempt leaves the same owner
locked and ready for another attempt.

### Native service enrollment is unavailable

Run `service status --json` and inspect `enrollment_available`,
`enrollment_mechanism`, and `enrollment_state`. Linux and WSL2 require a working
`systemd --user` session; macOS requires LaunchAgents; Windows requires the
current user's Task Scheduler. CodexFolio does not replace an unavailable
mechanism with cron, login scripts, registry startup entries, or a machine-wide
service. Use foreground `service start` for on-demand operation.

### The passphrase service is locked

Run `service status --json` to confirm `service_state`, `vault_state`, and
`database_state`, then run `vault unlock` in a private local terminal. The
dashboard cannot unlock the vault. If the state is `recovery_required`, stop the
foreground owner and follow its fixed `service recovery verify`, `list`, and
`restore` guidance instead of deleting state files.

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
