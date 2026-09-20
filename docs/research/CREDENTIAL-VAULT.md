# Shared Work Home credential vault research

This research records the original Shared Work Home rationale. Its WSL/headless and plaintext recommendations are superseded by [ADR 0035](../adr/0035-qualify-prompt-free-secure-storage.md); transaction research below does not authorize experimental implementation during Milestone 5A.

## Constraint

Codex requires an active authentication representation in the selected Codex home. Shared Work Home mode cannot provide persistent multi-identity switching without either repeated OpenAI authentication or CodexFolio securely retaining material that can restore each profile's Codex authentication.

## Original recommendations (superseded where noted)

1. **Windows:** Encrypt profile records with an application master key protected by DPAPI for the current user. Apply an explicit user-only DACL to active and recovery files.
2. **macOS:** Encrypt profile records with an application master key stored in Keychain. Keep active files owner-only.
3. **Linux desktop:** Prefer an application master key stored through Secret Service when an unlocked user session provides it.
4. **WSL/headless Linux:** Use an encrypted file unlocked by a user passphrase and a memory-hard KDF. Cache the unlocked key only in process memory.
5. **Compatibility fallback:** Permit private plaintext profile records only after an explicit warning. Enforce `0700` directories and `0600` files on Unix and a user-only DACL on Windows.

Storing an encryption key next to its ciphertext does not provide meaningful protection and is prohibited.

## Transaction and recovery

- Acquire an exclusive Shared Work Home ownership lock before materializing or synchronizing credentials.
- Persist an intent lease containing an opaque profile identifier, vault generation, and active-auth fingerprint.
- Verify no Codex process is using the Shared Work Home before changing its authentication representation.
- Modify only the authentication representation required for identity selection; do not rewrite sessions, configuration, skills, plugins, agents, or other Codex-owned state.
- Stage and validate the target authentication before atomically activating it.
- Verify the materialized identity through a documented Codex account interface before committing the new vault generation.
- After Codex exits, validate the refreshed authentication identity and seal it into a new vault generation before advancing the manifest.
- Retain the prior vault generation until the commit completes.
- After a crash, recover the active file only under the profile recorded in the lease, never under a potentially stale alias.
- Reauthentication creates a new generation only after isolated Codex login succeeds and the returned identity is verified.

## Original service limitation (qualified by ADR 0035)

An unattended collector can unlock only through an explicitly provisioned service-identity secret. It must never silently downgrade to plaintext. Passphrase-backed WSL/headless vaults require an interactive unlock after reboot, so background collection must remain unavailable until unlocked.

The current stable-core contract requires prompt-free operation only on qualified OS-backed configurations, including a natively proven Windows-user-backed WSL integration. Explicit passphrase mode remains a non-prompt-free alternative. No plaintext or adjacent-key fallback is permitted. Automatic companion lifetime, collection consent, and OS-login enrollment are separate decisions under [ADR 0037](../adr/0037-separate-companion-lifetime-from-consent-and-attribution.md).
