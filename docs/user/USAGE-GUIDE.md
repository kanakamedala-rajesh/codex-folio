# Usage guide

This guide is the shortest path for a first-time tester. CodexFolio is still a
development preview: build it from source, use a test repository, and avoid
important accounts until you have read the
[compatibility notes](COMPATIBILITY.md).

CodexFolio does not replace Codex. It organizes local Identity Profiles,
launches your installed Codex executable, and displays supported local usage
evidence. Codex still owns authentication and provider communication.

## 1. Build the preview

Install the pinned Go, Node.js, and npm versions listed in the
[build guide](../development/BUILDING.md). From the repository root, run:

```sh
node scripts/verify.mjs
./build/bin/codex-folio version
```

On Windows PowerShell, use:

```powershell
node scripts/verify.mjs
.\build\bin\codex-folio.exe version
```

The verifier installs the locked frontend packages, runs the repository checks,
and builds the native development binary.

## 2. Check your Codex installation

```sh
./build/bin/codex-folio codex discover
```

If Codex is not on `PATH`, provide its absolute path:

```sh
./build/bin/codex-folio codex discover --codex-bin /absolute/path/to/codex
```

Use the Windows executable path and quote paths containing spaces.

## 3. Start the dashboard

On Windows, macOS, or a Linux desktop with an unlocked Secret Service:

```sh
./build/bin/codex-folio service start
```

On WSL or headless Linux, explicitly choose the passphrase vault:

```sh
./build/bin/codex-folio service start --vault-mode passphrase
```

The service starts locked without asking for a passphrase, but still publishes
the dashboard. In a second local terminal, unlock that running session:

```sh
./build/bin/codex-folio vault unlock
```

The unlock command uses private terminal input with echo disabled. It sends the
passphrase only to the authenticated loopback command endpoint; the dashboard
never accepts or receives it. A wrong passphrase leaves the service locked, and
stopping or restarting the service requires a new unlock.

CodexFolio does not silently fall back to plaintext or infer passphrase mode
from a missing Secret Service.

The start command prints a one-time local dashboard URL. Open it in your browser
and keep this terminal running. A locked dashboard shows only safe unlock or
recovery guidance and does not run profile, usage, launch, analytics, or
continuation workflows. Press `Ctrl-C` when you want to stop the service.
Running `service start` again while the service is active prints a fresh URL.

The dashboard is loopback-only. Do not share its URL: the bootstrap value is a
short-lived, single-use local authorization credential.

## 4. Add your first Identity Profile

Open **Profiles**, then choose **Add Identity Profile**.

1. Enter a display name and CLI Alias, such as `Personal` and `personal`.
2. Keep **Managed Identity Home** for the safest first run.
3. Choose browser sign-in or device-code authentication.
4. Continue through the installed Codex authentication flow.
5. If the dashboard gives you a terminal command, run that exact command in a
   local terminal and return to Profiles afterward.

CodexFolio never asks for your password. An unfinished profile remains Pending
and cannot be selected or launched; reopen it from Profiles to resume.

You can also create a managed profile directly from the CLI:

```sh
./build/bin/codex-folio profile add personal --browser
```

When the service is stopped on WSL/headless Linux, add
`--vault-mode passphrase` to vault-dependent commands and enter the same
passphrase. Keeping the unlocked service running avoids reopening the vault for
each command because the CLI routes operations through that owner.

Use a Referenced Identity Home only when you intentionally want to register an
existing Codex home. CodexFolio does not own or delete referenced files.

## 5. Select, inspect, and launch

```sh
./build/bin/codex-folio profile list
./build/bin/codex-folio select personal
./build/bin/codex-folio usage refresh personal
./build/bin/codex-folio usage show
./build/bin/codex-folio launch personal --
./build/bin/codex-folio project resolve . --alias MyProject
./build/bin/codex-folio launch personal --project PROJECT_ID --
```

Selecting a profile affects future interactive launches. It does not switch or
stop an already-running Codex process. `launch` keeps Codex in the foreground so
normal terminal input, signals, and exit status are preserved.

In the dashboard:

- **Overview** shows current or last-known usage evidence and eligible profiles.
- **Profiles** lets you add, resume, edit, select, reauthenticate, remove, restore,
  purge, or manage Shared Configuration Packs. Pack actions show immutable
  version health, fixed declarative document slots, assignment, projection
  conflicts, and reviewed promotion. Removal requires the exact CLI Alias;
  selected profiles need a replacement, and running Managed Launches block
  removal.
- **Refresh** requests new supported usage evidence.
- **Sessions** filters recorded Managed Launches and Observed Sessions by
  profile, project, date, and record type. Open a row for metadata-only details;
  **Back to Sessions** retains your filters.
- **Analytics** provides Capacity, Tokens, Projects, Models, Activity, and
  Compare. The profile, history, and project filters are shared across the
  detailed tabs. Charts have equivalent semantic tables; absent token/model
  metadata stays explicitly unsupported. Projects uses app-local aliases and
  basenames, lets you edit an alias, and never displays the canonical repository
  path. Compare does not pool quotas or change Selected Profile. **Local
  analytics data** previews exact export fields and record counts before a JSON
  or CSV browser download, configures retention from 30 days through unlimited,
  and dry-runs fully scoped purge counts before confirmation. Canonical project
  paths require explicit inclusion.
- **Alerts** shows active capacity, reset, reauthentication, stale-evidence,
  repeated-collection-failure, and compatibility conditions plus bounded,
  deduplicated history. Acknowledge an active condition or change the default
  20% warning and 10% critical remaining thresholds for one profile/window.
  Missing or contradictory evidence never becomes a capacity or exact-reset
  alert. Overview shows only decision-relevant notices; Alerts remains
  available without native service enrollment. An explicitly enrolled service
  can deliver supported native notifications. Notification text is generic by
  default; choose **Notification privacy → Include identity and quota details
  on this device** only if aliases and capacity may safely appear on shared or
  locked screens. This per-device choice is separate from service enrollment,
  update checks, and telemetry, and can be revoked by selecting **Generic
  notifications** again. Delivery failure never disables Alerts or collection.
- **Launch Codex** displays an explicit terminal command; it does not start a
  hidden browser process. Choose a registered Project Identity, then run that
  command to keep the selected working directory in the service-validated
  Launch Plan.
- **Prepare Handoff** on an eligible alternative captures a repository-first
  checkpoint for review. Edit the six handoff fields, inspect repository and
  validation evidence, redact selected paths or exact text, save the sanitized
  revision, and approve only after the source process is definitively exited.
  Optional transcript assistance remains off until you consent for that one
  handoff and provide a thread ID. Its candidates are transient: edit and
  redact them, review the sanitized preview, then use the separate approval
  action. Canceling restores the repository-first draft.
  Run the resulting terminal command to start a fresh foreground Codex process;
  canceling before that point starts nothing.
- **Settings → Safe Continuation data** lists retained, completed, recoverable,
  expired, and uncertain-start checkpoint states using safe project labels.
  Repository-first retention defaults to 30 days and transcript-assisted
  retention to 7 days; each accepts one or more days or `unlimited`. Export is
  previewed before an encrypted `.cfolio` browser download. Plaintext JSON needs
  a separate acknowledgement. Purge previews and confirms one exact revision.
- **Settings → Local diagnostics** controls diagnostic collection independently
  from the service, notifications, updates, and telemetry. The default is
  enabled at Info level for 14 days, with a 50 MB bundle ceiling. Preview the
  exact redacted field list and record count before choosing **Download
  reviewed JSON**; canceling downloads nothing.
- **Settings → Updates** keeps network access off by default. **Check for
  updates** performs one check and does not enable later checks. The separate
  **Check automatically** preference can be saved or revoked at any time.
  Valid evidence may show only the available version, release notes, verified
  download location, and installer guidance; CodexFolio never downloads,
  executes, or installs the update. Development builds report the production
  source as unconfigured until a release endpoint is approved.

The launch view stays at **Prepared · Not started** until the terminal command
reports a real process start. It then reports running, exit status, or failed
start; a nonzero exit is not quota evidence by itself.

The handoff view treats running and uncertain source-process state as blockers.
Target capacity and eligibility are guidance until the terminal command repeats
source, repository, target-home, authentication, usage, revision, and expiry
checks immediately before launch. A service restart never turns an uncertain
start into authorization: recovery becomes available only after storage,
repository, source-exit, target, revision, and expiry checks pass. CodexFolio
does not kill Codex during recovery or purge.

For Shared Configuration Packs, assign only an approved version, preview every
projection, then explicitly apply that exact preview. Profile-local conflicts
are shown and preserved. Promotion creates a reviewed immutable version and
does not change the current profile assignment automatically. Browser pack
drafts accept only `config.toml`, `AGENTS.md`, and `plugins.lock`; use the CLI
for other supported declarative pack paths.

Unavailable or stale evidence is not zero usage. Check its state, source, and
capture time before acting on it.

Managed Identity Homes remain recoverable in Profiles for seven days after
removal. Referenced Identity Homes are only unregistered: CodexFolio never
deletes or rewrites their external files or the remote OpenAI identity.

Analytics purge is separate from profile removal. Its confirmation is bound to
the complete scope and the records counted by the preview; changing the scope
or matching data makes that confirmation stale and requires a new preview.

## 6. Stop or try again safely

Press `Ctrl-C` in the service terminal. Your local state remains available for
the next run.

For a disposable test, pass the same absolute `--state-root` directory to every
command. This isolates the preview from your normal CodexFolio state. Do not use
your normal Codex home as the state root, and do not remove directories
recursively to unregister a profile.

Useful checks:

```sh
./build/bin/codex-folio service status
./build/bin/codex-folio vault unlock
./build/bin/codex-folio profile list --json
./build/bin/codex-folio --help
```

Background startup remains optional. To enroll this exact development build
for the current user, inspect it, and remove only that enrollment:

```sh
./build/bin/codex-folio service install
./build/bin/codex-folio service status --json
./build/bin/codex-folio service uninstall
```

Review or change local diagnostic policy, then preview before choosing a local
destination:

```sh
./build/bin/codex-folio diagnostics settings
./build/bin/codex-folio diagnostics settings --enabled false --level warning --retention-days 7
./build/bin/codex-folio diagnostics export --dry-run
./build/bin/codex-folio diagnostics export --output ./codex-folio-diagnostics.json
```

Check update state or manage the separate network consent from the CLI:

```sh
./build/bin/codex-folio updates status
./build/bin/codex-folio updates check
./build/bin/codex-folio updates settings --automatic true
./build/bin/codex-folio updates settings --automatic false
```

The explicit check never changes the automatic preference. Until a
project-controlled production endpoint is configured, checks safely report
`unconfigured` and local features continue normally.

The export command requires the displayed confirmation and never replaces an
existing file. It does not upload, open an issue, enable telemetry, or include
analytics, checkpoints, configuration payloads, identities, paths, usage, or
Codex/repository content.

Only an explicitly installed native service runs periodic usage collection.
Opening the dashboard or using `service start` on demand does not enroll or
enable it. The default interval is five minutes while a Managed Launch is
running and 30 minutes while idle. Review or change both intervals in
**Settings → Periodic collection schedule**, or from a terminal:

```sh
./build/bin/codex-folio settings collection
./build/bin/codex-folio settings collection --active-minutes 10 --idle-minutes 60
```

Intervals cannot be shorter than five minutes. Known provider reset times can
trigger a collection near that boundary; missing or unknown reset metadata is
never guessed. A passphrase-backed enrolled service stays paused while locked.

CodexFolio uses Task Scheduler on Windows, a LaunchAgent on macOS, and
`systemd --user` on Linux or systemd-enabled WSL2. These commands do not request
administrator/root access, remove app data, change Codex, or fall back to a
different startup mechanism. If the native mechanism is unavailable, continue
with the existing on-demand `service start` workflow.

If a bare command on WSL reports `CF_VAULT_UNAVAILABLE`, rerun the service with
`--vault-mode passphrase`. This error normally means Linux Secret Service is not
available in that session; it does not authorize a plaintext fallback.

If authentication expires, use Profiles → **Reauthenticate**, or run:

```sh
./build/bin/codex-folio profile reauthenticate personal --browser
```

For every command, lifecycle operation, JSON format, and troubleshooting detail,
continue with the [Comprehensive usage guide](COMPREHENSIVE-USAGE-GUIDE.md).
Read [Privacy](PRIVACY.md) before exporting or sharing any diagnostics.
