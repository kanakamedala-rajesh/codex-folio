# Shared Configuration Pack research

## Principle

Share reviewed declarative intent, not live mutable Codex state. Project a validated copy into every isolated Identity Home instead of pointing concurrent Codex processes at one writable store.

## Shareable sources

- Allowlisted non-secret `config.toml` keys and named configuration profiles
- Custom agent role TOMLs
- Global `AGENTS.md` guidance; repository guidance remains in the repository
- Skills from immutable or pinned sources
- Plugin desired-state manifests and version locks, not mutable install caches
- MCP server definitions containing commands, URLs, policy, and secret-reference names—not literal secrets or OAuth grants
- Reviewed rules projected as immutable copies
- Platform-specific executable and path overlays

## Profile-owned state

- Codex and MCP authentication or OAuth grants
- Sessions, rollout files, and history
- SQLite thread and job state
- Logs and optional plaintext TUI logs
- Mutable plugin installation state
- Caches, generated indexes, sockets, locks, and daemon runtime state
- Account-specific paths, workspace constraints, and local overrides

## Proposed pack

```text
pack/<pack-id>/<version>/
  manifest.toml
  config/base.toml
  config/profiles/<name>.toml
  agents/<role>.toml
  guidance/AGENTS.md
  rules/*.rules
  skills/<skill>/...
  plugins.lock
  mcp/<server>.toml
  platform/{linux,macos,windows}.toml
```

The pack is immutable and content-addressed. Projection merges pack base, platform overlay, and profile-local override into staging; scans for forbidden secret-bearing fields; validates against the selected Codex version; and swaps copies only while that profile is stopped. Codex-written changes remain profile-local until the user explicitly promotes a reviewed change into a new pack version.
