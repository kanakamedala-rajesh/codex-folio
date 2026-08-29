# Let Codex own profile authentication

Each Identity Profile will have a persistent Identity Home in which Codex owns credential storage, refresh, and reauthentication. The companion will select the home when launching Codex instead of copying raw authentication files or acting as an OAuth token broker, avoiding routine browser login while keeping credential handling on documented Codex paths.
