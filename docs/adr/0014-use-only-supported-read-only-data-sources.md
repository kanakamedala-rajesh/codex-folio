# Use only supported read-only data sources

The MVP will collect account and usage information only through documented Codex/App Server interfaces, eligible local Codex metadata that can be processed without retaining transcript content, and CodexFolio's own records. It will not scrape a web UI or depend on undocumented private endpoints to increase metric coverage. Unsupported, unavailable, stale, and reauthentication-required states remain visible rather than being converted to zero or an estimate. This trades breadth for safety, clarity, and resilience as Codex changes.
