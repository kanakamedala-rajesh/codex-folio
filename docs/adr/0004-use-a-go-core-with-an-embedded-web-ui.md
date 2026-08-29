# Use a Go core with an embedded web UI

The MVP will ship as a native Go executable containing the CLI, application service, loopback HTTP API, and embedded responsive web assets. This keeps terminal-only operation and cross-platform distribution simple while preserving a transport-independent UI boundary that can support a native desktop shell later.
