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

> **Known WSL/headless limitation:** passphrase mode currently shows no input
> prompt and echoes the passphrase as you type. Do not use it in a shared,
> recorded, or observable terminal, and never enter a reused password. Prefer an
> available Linux Secret Service or defer vault-dependent testing until this is
> fixed.

CodexFolio does not silently fall back to plaintext or infer passphrase mode
from a missing Secret Service.

The command prints a one-time local dashboard URL. Open it in your browser and
keep this terminal running. Press `Ctrl-C` when you want to stop the service.
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
passphrase, subject to the known terminal-echo limitation above. Keeping the
service running avoids reopening the vault for each command because the CLI
routes operations through that owner.

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
- **Analytics** shows a selected profile's retained Capacity history and an
  explicit Compare view. History gaps remain unavailable, provider windows stay
  separate, and chart values have the same semantic table underneath. Compare
  does not pool quotas or change Selected Profile.
- **Launch Codex** displays an explicit terminal command; it does not start a
  hidden browser process. Choose a registered Project Identity, then run that
  command to keep the selected working directory in the service-validated
  Launch Plan.

The launch view stays at **Prepared · Not started** until the terminal command
reports a real process start. It then reports running, exit status, or failed
start; a nonzero exit is not quota evidence by itself.

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
./build/bin/codex-folio profile list --json
./build/bin/codex-folio --help
```

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
