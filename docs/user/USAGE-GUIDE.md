# Usage guide: set up from scratch

This is a fresh, manual setup for the development preview. It creates a new
CodexFolio state directory without deleting your existing setup. CodexFolio
organizes local profiles and launches your installed Codex; Codex owns account
sign-in and provider communication.

For a first foreground launch, build the CLI and run `./build/bin/codex-folio`
with no subcommand. If its directory is not on `PATH`, an interactive prompt
offers to add it for future plain `codex-folio` use. Approve only if you want
that change; declining continues with the direct binary. On bash or zsh, the
approved setup appends a line to `.bashrc` and the active bash login file, or to
`.zshrc` for zsh; on Windows, it adds
the binary directory to your user `Path`. Open a new terminal before trying
the plain command. A separate prompt offers background collection of supported
usage metadata. Choose yes for periodic collection or no for on-demand refresh
only; the choice is remembered and can be changed in Dashboard Settings. It does
not enroll OS-login startup. When no profile is ready, choose `r` to register an existing
Codex home in place or `m` to create an isolated Managed Identity Home. Give it
an alias and display name, complete Codex's sign-in if needed, then choose the
ready profile in the picker to launch. After authentication, the CLI lists
detected history sources and asks separately before importing each supported
source. Press Enter or `n` to decline and continue to the picker. A referenced
home remains shared with direct Codex; registration alone does not import
history. Missing or failed history import does not change a Ready profile or
block launch. Pending setup can be resumed
by running the plain command again. The longer manual workflow below is useful
for checking multiple accounts and usage evidence.

Dashboard profile setup also offers source review and optional import after the
terminal step completes. To bring supported existing sessions into overall
history later, open **Sessions**,
choose **Review sources**, inspect each source's support state and session count,
then explicitly consent to **Import source** for the source you choose. Refusing
or leaving consent unchecked imports nothing. Unknown ownership appears as
**Unassigned History** in the overall timeline; it is not a selectable profile
and does not contribute to individual-profile or Combined Identity View totals.
Import reads source metadata without changing Codex files.
To correct ownership, open an observed session and choose **Correct ownership**,
or select several observed sessions and use **Assign selected sessions**.
Choose a ready profile or **Unassigned History**, then save. Repeat imports keep
your corrections; the original ownership attribution remains visible separately.
Sessions shows imported token counts only when supported local metadata records
them; a recorded zero stays zero, while missing history remains unavailable.
In Analytics, **Overall history** includes Unassigned contributions separately
from profile totals. Importing sessions does not recreate past provider quota
or credit snapshots; refresh provider usage separately for current evidence.
Local profile token totals use imported ownership, not every thread in a shared
Codex home.

The commands below use **WSL/Linux with bash or zsh**, two terminals, and a
browser. Run them from the CodexFolio repository root. For Windows/macOS and the
full feature reference, see the [comprehensive guide](COMPREHENSIVE-USAGE-GUIDE.md).

## 1. Build and check prerequisites

Install Git and the pinned toolchain: Go **1.27.0**, Node.js **24.18.0**, npm
**11.16.0**. Codex must be installed separately and available in this WSL
environment, not only on the Windows side. See [Building](../development/BUILDING.md)
and [Compatibility](COMPATIBILITY.md).

```sh
node scripts/verify.mjs
./build/bin/codex-folio version --json
./build/bin/codex-folio codex discover
```

Continue after verification succeeds and discovery identifies the intended
Codex executable. If discovery is ambiguous, use
`codex discover --codex-bin /absolute/path/to/codex` and supply that same
`--codex-bin` to profile setup and launch commands.

## 2. Stop the previous service and choose fresh state

If your old service is running, stop it with `Ctrl-C` in its original terminal.
For an enrolled service, follow the [service ownership instructions](COMPREHENSIVE-USAGE-GUIDE.md#state-service-ownership-and-vault-mode).
There is one writable service owner per OS user: a different state root does
not allow a second concurrent owner. Do not delete owner locks to bypass this.

In **both terminals**, set the same new, absolute path outside this repository:

```sh
CF_STATE="$HOME/codex-folio-manual-01"
```

Choose a name you have never used. Do not point it at `~/.codex`, a repository,
or your existing CodexFolio state. Keep `--state-root "$CF_STATE"` on every
stateful command below; omitting it targets the default setup.

## 3. Start with Windows-backed WSL protection

In **Terminal A**:

```sh
./build/bin/codex-folio service start --state-root "$CF_STATE" --vault-mode wsl-dpapi
```

Keep Terminal A open. In **Terminal B**:

```sh
./build/bin/codex-folio service status --state-root "$CF_STATE" --json
./build/bin/codex-folio profile list --state-root "$CF_STATE"
```

The build places `codex-folio-wsl-vault.exe` beside the Linux executable. The
service sends envelope-key bytes to that one-shot helper only over bounded
stdin/stdout pipes and stores only DPAPI-protected key material in the distinct
WSL vault file. It uses the interoperating Windows user's DPAPI context; it does
not isolate Linux processes that can act as that Windows user.

Open the one-time URL printed in Terminal A. Confirm the vault is unlocked and
Profiles is empty. If the link expires or was already used, run this in Terminal B
and open the new URL:

```sh
./build/bin/codex-folio service start --state-root "$CF_STATE" --vault-mode wsl-dpapi
```

This reuses the owner and exits after printing a fresh link. Treat that URL as
a private credential.

## 4. Add and verify your three accounts

Create **Managed Identity Homes**, one at a time. Complete the sign-in before
starting the next command. Check the account and workspace in each Codex sign-in
flow; the browser may remember the previous account.

```sh
./build/bin/codex-folio profile add work --display-name Work --browser --state-root "$CF_STATE"
./build/bin/codex-folio profile add personal --display-name Personal --browser --state-root "$CF_STATE"
./build/bin/codex-folio profile add personal-free --display-name personal-free --browser --state-root "$CF_STATE"
./build/bin/codex-folio profile list --state-root "$CF_STATE"
./build/bin/codex-folio select work --state-root "$CF_STATE"
```

You can instead use **Profiles → Add Identity Profile**, with the same aliases
and Managed home choice. For device-code sign-in, use `--device-code` instead
of `--browser`. Finish authentication through installed Codex, never through a
CodexFolio credential form. **Continue in Codex** immediately shows a copyable
command for the selected method and this installation. Run it in your terminal;
Profiles shows waiting and running status, then marks the profile Ready after
the command completes. If setup fails or is interrupted, the profile remains
Pending. Reopen it and run the shown command again with the same alias.

All three profiles should become Ready. An alias is a local label, not proof
of which remote account was authenticated.

## 5. Refresh and compare real limits

```sh
./build/bin/codex-folio usage refresh work --state-root "$CF_STATE"
./build/bin/codex-folio usage refresh personal --state-root "$CF_STATE"
./build/bin/codex-folio usage refresh personal-free --state-root "$CF_STATE"
./build/bin/codex-folio usage show --combined --state-root "$CF_STATE"
```

Open Overview and inspect each profile using Dashboard Scope and Open details.
For the accounts described for this manual test, compare against:

| Account | Expected windows |
| --- | --- |
| Work — business premium | Weekly only |
| Personal — business standard | Five-hour and weekly |
| personal-free — free | Monthly only |

These are account-specific expectations, not a universal mapping of plans to
limits. Confirm actual provider duration, reset, source and capture time.
Work and Personal sharing a workspace does not justify pooling their quotas.
Work/free may still show a Secondary window labeled Unsupported/Unavailable;
that does not mean a second allowance exists.

**Known unresolved issue:** older observations can cause false “Conflicting
observations.” Fresh state avoids importing old local readings, but does not
fix that defect. If it occurs, record the source/version/window details rather
than treating it as a sign-in failure or deleting evidence.

## 6. Create a real session and collect history

Fresh managed homes start with **no existing sessions**. They do not inherit
history from your default `~/.codex` home.

Register a repository you intend to use for testing; replace the path below:

```sh
./build/bin/codex-folio project resolve /absolute/path/to/test-repository --alias ManualTest --state-root "$CF_STATE"
./build/bin/codex-folio project list --state-root "$CF_STATE"
```

Copy its Project ID, then replace `PROJECT_ID` below:

```sh
./build/bin/codex-folio launch work --project PROJECT_ID --state-root "$CF_STATE" --
```

For the shorter picker path, run CodexFolio from the repository where you want
Codex to start:

```sh
./build/bin/codex-folio --state-root "$CF_STATE"
```

The current Selected Profile is marked with `*`. Press Enter to launch it,
enter another displayed number to select and launch that profile, or enter `q`
to cancel. The picker reuses Terminal A's unlocked service and does not change
an already running launch. Use the explicit `launch` form above when you need a
Project ID, Codex arguments, or a particular alias without changing Selected
Profile.

Complete a short real interaction, then exit Codex normally. This uses the Work
account and its quota. A `--help` launch only proves process startup, not a model
session. All CodexFolio options belong **before** `--`.

```sh
./build/bin/codex-folio activity refresh work --state-root "$CF_STATE"
./build/bin/codex-folio activity list --profile work --state-root "$CF_STATE"
```

Reload Sessions. Inspect Managed Launch and Observed Session records separately;
they are not necessarily one-to-one. **Reload timeline** reads retained data;
`activity refresh` collects supported metadata from the registered home.
To import supported existing history, review sources and consent separately in
Sessions. Use the **Unassigned History** profile filter to inspect records whose
ownership is unknown. Repeat imports skip already retained source sessions.
For an existing sign-in, use **Use existing sign-in** after choosing a referenced
home in Profiles, or follow [Referenced Identity Home setup](COMPREHENSIVE-USAGE-GUIDE.md#register-a-referenced-identity-home).

## 7. Validate manually, then restart

Use a medium or large browser window. Work through the
[manual validation checklist](COMPREHENSIVE-USAGE-GUIDE.md#manual-validation-checklist).
It covers all six pages, dialogs, saves/cancels, real launches and handoff,
with separate destructive and platform checks. Record actual results; the
checklist is not a claim that every feature currently passes.

To stop the manually started foreground owner, exit any Codex session and press
`Ctrl-C` in Terminal A. You can then exercise ordinary passphrase startup
without manually keeping a service terminal open:

```sh
./build/bin/codex-folio --state-root "$CF_STATE" --vault-mode passphrase
```

CodexFolio starts or reuses the full on-demand companion, reads the vault
passphrase once through private terminal input when that owner is locked, and
sends it through the authenticated `UnlockVault` command. Press Enter on the
highlighted Selected Profile. The installed Codex process remains the foreground
terminal child with its input, output, working directory, signals, arguments and
exit status preserved, while the companion remains available after Codex exits.
Ordinary startup prints a non-secret dashboard address and a repeatable
`service start` command for fresh browser authorization; it does not open a
browser. Before opening the HTTPS dashboard for the first time, run
`./build/bin/codex-folio service certificate`, check the printed SHA-256
fingerprint, and import its public certificate into your browser's trusted
certificate authorities. This is a one-time browser or OS trust setup; do not
bypass a certificate warning. On WSL2, import the certificate into the Windows
browser you use to open the dashboard. Open the one-time link, then choose
**Trust this browser** if it is your own browser and you want the current
address to reopen after ordinary restarts. Leave it untrusted on a shared or
private browser. The [comprehensive guide](COMPREHENSIVE-USAGE-GUIDE.md) covers
platform-specific certificate setup and removal.
In **Settings → Trusted browser**, use **Forget this browser** or **Revoke all
browsers** to end access; open a fresh terminal link afterward.

For a fresh ordinary installation, run the same plain command without
`--vault-mode`. CodexFolio initializes the current user's supported native
protection (Windows DPAPI, macOS Keychain, Linux Secret Service, or
Windows-user DPAPI through the bundled WSL2 helper), remembers
that non-secret choice, and later starts reach the picker without an application
passphrase. If Linux Secret Service is locked or unavailable, the same flow
offers retry, the repeated-interaction passphrase alternative, or cancellation;
cancellation changes no selection and rerunning retries setup. Windows and
macOS report the native prerequisite to unlock or restore. CodexFolio never
downgrades to plaintext or a local unprotected key.

On WSL2, a fresh plain start chooses `wsl-dpapi`. Restore Windows
interoperability or the bundled helper when setup reports it unavailable. A
custom installation may set `CODEX_FOLIO_WSL_VAULT_HELPER` to the helper's
absolute Linux path; secrets are never passed through that environment value.
An existing non-empty state root without a selection remains on its legacy
provider and is never initialized with a replacement WSL key.

For an existing Linux or WSL passphrase installation, stop its running service
owner and run the plain command without `--vault-mode`. Choose **migrate now**,
then enter the old passphrase once through the private terminal prompt.
CodexFolio's detached service creates a validated recovery backup, moves every
allowlisted protected database field to the supported native destination,
reopens and authenticates the retained state, and only then remembers the new
storage choice. Profiles, selection, history, checkpoints and Identity Homes
remain; Codex-owned authentication files are not opened or copied. The picker
and foreground Codex launch continue in the same command after success.

Choose **continue with passphrase storage** to keep ordinary explicit
passphrase operation, or **cancel** to change nothing. A wrong passphrase,
unavailable destination, failed verification or interrupted transition never
initializes an empty database or replaces the old vault key. Rerunning the plain
command either completes the verified destination or restores the validated
passphrase backup before offering migration again. `CF_VAULT_MIGRATION_REQUIRED`
means this recoverable transition still needs attention; do not delete the
database, passphrase vault, migration journal or recovery directory. This is a
same-installation protection change, not portable credential backup.

This automatic on-demand lifetime does not install OS-login startup or grant
periodic collection consent. The setup prompt remembers that separate choice;
declining leaves foreground launch and on-demand refresh available. Run
`codex-folio service stop` to stop an idle companion. If Managed Launches are
active, choose whether to stop after they finish; `service stop --wait` makes
that choice without a prompt, and `service stop --cancel` cancels a pending
stop. Stopping never terminates Codex and leaves OS-login enrollment installed.
The next plain `codex-folio` command starts the companion again.

For the dashboard restart check, stop and rerun the plain command with the same
state root and vault mode, then use the printed reopening command for a fresh
one-time browser URL. Profiles and retained history should still be present. A
new passphrase-backed service session requires one fresh private unlock.

For another completely fresh run, stop the owner and choose a new path such as
`$HOME/codex-folio-manual-02`. Preserve the previous directory for comparison.
This guide does not require deleting your normal setup, Codex home, or history.
