# Issue #77: optional telemetry privacy boundary

The shipped client is deliberately usable with a disabled adapter. Production
composition supplies no endpoint and therefore reports `unavailable`; a test
recording adapter is the only operational transport in this ticket.

Consent is explicit and bound to consent schema v1. The durable state is a
singleton outside portable configuration and holds only consent metadata plus a
cryptographically random resettable installation ID. Browser and CLI responses
expose presence, never the ID value.

The event type is the allowlist: application version, OS family/architecture,
coarse feature/outcome, registered stable error code, duration bucket, and the
random ID. There is no extension map or raw payload field. Bounded queues and
attempt timeouts isolate local workflows from transport delay and failure.
Revocation and reset persist first, invalidate queued event work, then enqueue
best-effort deletion of the prior ID.

Operational enablement requires all seven facts: project HTTPS endpoint, public
schema, privacy notice, 30-day event-retention job, 13-month anonymous aggregate
retention job, deletion operation, and reset operation. No backend deployment or
production retention evidence is claimed by this implementation.

Verification uses deterministic domain, adapter, store, HTTP, CLI, and real
loopback browser fixtures. Native non-WSL validation and validations unavailable
through the browser fixture are explicitly outside the user-approved evidence
scope for this issue.
