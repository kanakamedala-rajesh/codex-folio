# Stable error-code policy

`internal/apperrors/codes.json` is the central registry for machine-readable
application error identifiers. `node scripts/check-error-codes.mjs` validates
it during repository verification, checks the Go constants for drift, and uses
the registry's Git history to ensure retired entries remain retired and present.

- Codes use `CF_<OWNER>_<CONDITION>` and an owner matching the module that
  defines the condition.
- Every entry records the product version in which it was introduced and one
  lifecycle: `active`, `deprecated`, or `retired`.
- A deprecated code names a registered replacement. A retired code records its
  retirement version. An identifier remains reserved forever and cannot be
  reused for another meaning.
- User-facing copy, remediation text, paths, credentials, and user data do not
  belong in the registry. Copy is mapped to identifiers at presentation
  boundaries and may be localized or revised independently.

Changing the meaning of a published identifier is incompatible. Deprecate it
and allocate a new identifier instead.
