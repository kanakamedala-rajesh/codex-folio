# CLI conventions

The Phase 0 command surface establishes the automation contract for later
commands. Applicable commands will provide `--json`; non-interactive commands
must not mix human diagnostics with machine-readable data.

## Exit status

| Status | Meaning                                                                |
| ------ | ---------------------------------------------------------------------- |
| `0`    | The requested command completed successfully.                          |
| `1`    | The command failed while performing valid requested work.              |
| `2`    | The command line was invalid, including unknown commands or arguments. |

A foreground `launch` command will return the installed Codex process status as
specified by ADR 0019. Additional stable statuses require an ADR when they alter
the automation contract.

## Output streams

- **stdout** contains requested data, successful human-readable output, or help.
  JSON mode writes one complete JSON value and no explanatory prose.
- **stderr** contains diagnostics for unsuccessful commands. Diagnostics begin
  with `codex-folio` and, when applicable, a stable identifier from the
  [error-code registry](../../internal/apperrors/codes.json).
- A failed machine-readable command writes no partial data to stdout.

User-facing diagnostic text may improve without changing automation. Scripts
must depend on documented exit statuses, JSON fields, and stable error
identifiers rather than English wording.
