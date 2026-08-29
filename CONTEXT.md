# VS CodexFolio

A local companion for launching Codex under different identities and understanding their usage without repeated routine authentication.

## Identity and access

**Identity Profile**:
A user-named local Codex authentication context bound to a login identity, workspace, API key, or other supported credential source.
_Avoid_: Account, config profile

**Pending Profile**:
An incomplete Identity Profile registration that is retained for resumable setup but cannot be selected or launched until Codex authentication and Identity Home validation succeed.
_Avoid_: Broken profile, active profile

**Login Identity**:
The person-level identity authenticated through ChatGPT or another supported authentication method.
_Avoid_: Account

**Workspace**:
An OpenAI-managed organizational context selected under a Login Identity, with its own access, policy, and usage conditions.
_Avoid_: Account, organization profile

**Identity Home**:
The persistent Codex home owned by one Identity Profile, in which Codex manages that profile's authentication lifecycle.
_Avoid_: Credential store, account folder

**Managed Identity Home**:
An Identity Home created in CodexFolio's app-local storage and owned by CodexFolio for backup, quarantine, restoration, and purge.
_Avoid_: Existing Codex home

**Referenced Identity Home**:
An existing external Codex home registered for launch and observation without transferring filesystem ownership to CodexFolio.
_Avoid_: Imported home, managed home

**Shared Work Home**:
An experimental Codex home in which multiple Identity Profiles deliberately share sessions, configuration, skills, plugins, and custom agents while only one identity is active at a time.
_Avoid_: Shared account, multi-login home

## Continuity

**Shared Work Context**:
The repository state and deliberately shared configuration needed to continue work across Identity Profiles without treating credentials as shared state.
_Avoid_: Shared account, global profile

**Identity Handoff**:
Continuing repository work under a different Identity Profile after the previous identity becomes unavailable or reaches a usage limit.
_Avoid_: Hot-swap, account swap

**Safe Continuation**:
The guaranteed handoff path that launches a new Codex session under a target Identity Profile in the same repository with an approved repository-first checkpoint, without depending on live identity switching or exact thread transfer.
_Avoid_: Session resume

**Exact Continuation**:
An explicitly invoked experimental handoff that attempts to resume the same Codex thread under a different Identity Profile after the source process exits; it may be unavailable or fail across versions, identities, or workspaces.
_Avoid_: Guaranteed resume, hot-swap

**Local Identity Operation**:
A change to CodexFolio's local Identity Profile registry or authentication setup that does not modify the remote OpenAI user, workspace, billing, credit, or quota state.
_Avoid_: Account update

**CLI Alias**:
The unique, case-insensitive, shell-portable name used to address an Identity Profile in commands.
_Avoid_: Profile ID, display name

**Display Name**:
The editable human-readable label for an Identity Profile; it is not used as a stable identifier.
_Avoid_: Profile ID, CLI alias

## Usage

**Usage Snapshot**:
A time-stamped, source-attributed view of limits, credits, token activity, or locally derived usage for one Identity Profile.
_Avoid_: Usage record, live usage

**Managed Launch**:
A Codex process started by CodexFolio for which CodexFolio directly observes launch and exit boundaries.
_Avoid_: Codex session

**Observed Session**:
Session metadata discovered through a supported Codex source, which may not share the same boundaries as a Managed Launch.
_Avoid_: Managed process, CodexFolio session

**Project Identity**:
An app-local association between a repository location and a user-editable Project Alias, used for local analytics without adding files to the repository.
_Avoid_: Repository ID, project file

**Metric Availability**:
The explicit state of a metric as available, unsupported, temporarily unavailable, stale, or requiring reauthentication.
_Avoid_: Missing value, zero

**Provider-reported Metric**:
Read-only usage metadata returned through a documented Codex or OpenAI interface for the selected Identity Profile.
_Avoid_: Exact metric

**Locally-derived Metric**:
Usage metadata calculated from local Codex state without inspecting prompt, response, command, diff, or tool-output content.
_Avoid_: Provider usage

**Estimated Metric**:
A derived usage value whose assumptions and uncertainty must be visible to the user.
_Avoid_: Actual usage

**Selected Profile**:
The persisted Identity Profile shared by CodexFolio's dashboard and interactive CLI as the default highlighted choice for future interactive launches; it does not change existing Codex processes.
_Avoid_: Active identity

**Dashboard Scope**:
The Identity Profile or explicit combined view currently selected for dashboard presentation. Selecting a single profile through the primary dashboard selector also updates Selected Profile; entering Combined Identity View does not.
_Avoid_: Active identity, global account

**Launch Profile**:
The Identity Profile selected for one Managed Launch.
_Avoid_: Active identity

**Shared-Home Identity**:
The Identity Profile currently materialized in the experimental Shared Work Home while no conflicting Shared Work Home operation is allowed.
_Avoid_: Global active identity

**Combined Identity View**:
An explicitly selected aggregate of compatible metrics across multiple Identity Profiles while preserving each metric's source and provenance.
_Avoid_: Total account, merged account

**Eligible Profile Count**:
The number of Identity Profiles currently able to launch Codex according to companion-observable state; it is not a combined quota percentage.
_Avoid_: Total capacity

## Lifecycle

**Profile Quarantine**:
A recoverable app-local holding state for a removed Identity Profile and its Managed Identity Home before final purge; it does not apply to Referenced Identity Homes and has no effect on the remote OpenAI identity.
_Avoid_: Deleted account, recycle bin
