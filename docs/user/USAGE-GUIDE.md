# Usage guide: set up from scratch

This is a fresh, manual setup for the development preview. It creates a new
CodexFolio state directory without deleting your existing setup. CodexFolio
organizes local profiles and launches your installed Codex; Codex owns account
sign-in and provider communication.

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

## 3. Start and unlock

In **Terminal A**:

```sh
./build/bin/codex-folio service start --state-root "$CF_STATE" --vault-mode passphrase
```

Keep Terminal A open. In **Terminal B**:

```sh
./build/bin/codex-folio vault unlock --state-root "$CF_STATE"
./build/bin/codex-folio service status --state-root "$CF_STATE" --json
./build/bin/codex-folio profile list --state-root "$CF_STATE"
```

For a new state directory, the first unlock initializes its vault using the
passphrase you enter privately. Remember it: later unlocks use the same value.
There is no browser passphrase field. Never put the passphrase in a command,
screenshot, report, or environment variable.

Open the one-time URL printed in Terminal A. Confirm the vault is unlocked and
Profiles is empty. If the link expires or was already used, run this in Terminal B
and open the new URL:

```sh
./build/bin/codex-folio service start --state-root "$CF_STATE" --vault-mode passphrase
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
CodexFolio credential form. If setup remains Pending, resume the same alias.

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
For existing real history, use **Use existing sign-in** after choosing a referenced
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
browser.

This automatic on-demand lifetime does not install OS-login startup or enable
periodic collection. Those remain separate, explicit choices. To stop this
detached on-demand owner in this development slice, identify the owning process
with `service status --json` and use an orderly platform process stop; the
dedicated companion stop workflow is delivered separately.

For the dashboard restart check, stop and rerun the plain command with the same
state root and vault mode, then use the printed reopening command for a fresh
one-time browser URL. Profiles and retained history should still be present. A
new passphrase-backed service session requires one fresh private unlock.

For another completely fresh run, stop the owner and choose a new path such as
`$HOME/codex-folio-manual-02`. Preserve the previous directory for comparison.
This guide does not require deleting your normal setup, Codex home, or history.
