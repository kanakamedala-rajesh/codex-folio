# Repository publication checklist

This gate is for publishing source under the existing Apache-2.0 license. It is
not the [binary-release gate](FIRST-RELEASE-CHECKLIST.md). UI completion, marketing
assets, and code-signing purchases do not have to block source publication.

## Before changing visibility

- [ ] Confirm rights to publish all original code, documentation, dependencies,
  fonts, icons, screenshots, and other assets. Preserve `LICENSE`, `NOTICE`, and
  valid contribution attribution. Do not add employer or client material.
- [ ] Review a fresh full-history copy with every advertised branch and tag.
  Include available PR refs. Record the exact ref inventory and scanned revision.
  A shallow checkout or source ZIP is not a full-history review.
- [ ] Run an independently installed secret scanner against history and current
  files without a blanket baseline or fixture-directory exemption. Review each
  finding. Rotate real credentials; do not merely remove their visible text.
- [ ] Review issue and PR bodies, comments, review text, commit comments,
  attachments, releases, Actions logs from all retained attempts, artifacts,
  Pages, packages, LFS objects, and any enabled wiki. Automated export does not
  establish that binary or externally linked content has been examined.
- [ ] Record intentionally public contributor identities and no-reply preferences.
  Do not rewrite other contributors' attribution without coordination.
- [ ] Keep raw exports, findings, and backups outside the repository. Ensure they
  have appropriate local access restrictions. Do not upload them to public CI.
- [ ] Merge the documentation and hygiene changes after actual verification.
  Preserve historical milestone evidence and documented qualification exceptions.
- [ ] Verify the private security mailbox receives mail. Do not announce a
  response-time commitment that the maintainer cannot sustain.
- [ ] Prepare GitHub settings for public operation. Where the current private plan
  blocks branch rules, activate the prepared rules immediately after publication.

## GitHub controls to inspect

Use read-only inventory first. Configure least-privilege workflow tokens; disable
Actions-created PR approval unless explicitly needed. Require approval for all
external contributors before running their workflows. Review the actual proposed
workflow and scripts before granting that approval. Do not expose self-hosted
machines or application credentials to public PR jobs.

For `main`, require pull requests, the existing three platform verification checks,
and DCO. Block force pushes and deletion. A solo maintainer should not require
another person's approving review without arranging that reviewer; this can
otherwise block the maintainer's own PRs. CODEOWNERS is a review-routing aid, not
proof that a review happened. Do not enable merge queues without adding and
validating their required event/check handling.

Preserve the current standard hosted runners. Larger runners are billed even for
public repositories. Check storage, optional services, and budgets separately;
free standard-runner compute is not a promise that every GitHub product is free.
Do not turn off release qualification just to shorten CI.

## Visibility change and immediate checks

The maintainer performs the visibility change manually after signing off the
review record. No helper should toggle visibility as a side effect.

- [ ] View the repository signed out and check README, license, issue forms, and
  visible history, logs, and assets.
- [ ] Activate or verify main-branch protections, public-fork approval policy,
  private vulnerability reporting where available, secret scanning, and push
  protection where available. An inaccessible API response means unknown, not off.
- [ ] Run a harmless PR and confirm that the intended checks run and gate merging.
  Confirm the DCO checker still rejects an unsigned test commit.
- [ ] Inspect the next workflow's actual runner labels and billing classification.
  Past private-repository usage is not erased by future public operation.
- [ ] Freeze broad promotion until the runnable-preview onboarding gate is ready.

Maintain the signed-off publication record privately. A clean scanner run is one
piece of evidence, not a legal, privacy, or production-security certification.
