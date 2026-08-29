# VS CodexFolio Dashboard Brief

## Primary job

The overview must answer two questions within the first viewport:

1. Can the Identity Profile in the current Dashboard Scope continue working now?
2. If not—or if another profile is materially better—which compatible identity should take over?

This is an operational decision surface first and an analytics dashboard second.

## Selected direction: Signal Rail

Signal Rail uses the visual language of calibrated measurement instruments:

- a persistent, slightly off-center identity/navigation rail;
- one dominant active-profile capacity instrument;
- ranked eligible alternatives adjacent to it;
- sparse charcoal surfaces separated by fine rules rather than stacked floating cards;
- restrained cyan, amber, lime, and magenta signals assigned to stable semantic roles;
- compact, evidence-oriented labels and provenance marks;
- graphs drawn as measurement traces rather than decorative dashboard charts.

The direction should feel precise, calm, local, and trustworthy—not theatrical or retro-futurist.

## First viewport hierarchy

1. **Decision statement:** plain-language status such as “You can continue working” or “Handoff recommended,” with freshness and collection health.
2. **Scoped profile:** alias, identity/workspace context, available model/session metadata, five-hour and weekly capacity, credits when available, and reset times in the OS locale.
3. **Recommended alternatives:** ranked compatible profiles with remaining provider-reported capacity, compatibility reasons, auth/health warnings, and explicit Handoff or Launch actions.
4. **Context trace:** a compact recent-capacity trend that supports the recommendation without outranking it.
5. **Alerts:** only actionable or decision-relevant notices in the overview; full history lives one level deeper.

Combined metrics are never the default. They require an explicit Dashboard Scope change and must remain visibly distinguished from a single-profile view.

## Provenance and truthfulness

Every metric carries a semantic and accessible provenance indicator:

- **Provider reported:** limits, resets, credits, and online token summaries.
- **Locally derived:** tokens and metadata recorded in local Codex state.
- **Estimated:** quota-window deltas associated with sessions.
- **Observed during session:** repository additions and deletions observed while a session was active.

Never claim estimated quota use is “session weekly usage.” Never claim observed repository changes are “lines written by Codex.” Missing, stale, unsupported, and contradictory data are first-class display states rather than silently coerced values.

## Interaction model

- Profile selection, refresh, launch, and handoff are operable with keyboard alone.
- Primary navigation contains Overview, Profiles, Sessions, Analytics, Alerts, and Settings; handoff remains contextual.
- The Overview's primary actions are Refresh, Launch Codex, Prepare Handoff, and Open details.
- Handoff is explicit. The dashboard never kills or replaces a running Codex process.
- Destructive local profile deletion remains outside the overview and uses typed alias confirmation plus quarantine.
- Charts expose the same information through an adjacent semantic table.
- Status changes use screen-reader announcements without stealing focus.
- Focus is visually unmistakable and compatible with forced/high-contrast modes.
- Motion is limited to state continuity and feedback, and respects reduced-motion preferences.
- Capacity recommendations show their capture age and lose the “Recommended” label after ten minutes without fresh provider-reported data.

## Route contents

- **Overview:** scoped capacity, eligible alternatives, recent trace, actionable alerts, and the four primary actions.
- **Profiles:** operational inventory with identity/home/configuration health and explicit local lifecycle actions; never credential material.
- **Sessions:** one filterable chronology of separately labeled Managed Launches and Observed Sessions, with metadata-only details.
- **Analytics:** Capacity, Tokens, Projects, Models, Activity, and Compare tabs, each paired with semantic table equivalents.
- **Alerts:** active conditions, history, threshold configuration, notification privacy, and delivery health.
- **Settings:** service, retention, vault, diagnostics, telemetry, appearance, locale, and experimental features.

## Partial and stale data

Keep the information architecture stable when a source is unavailable. Render last successful evidence with exact age and reason, or an explicit unsupported/reauthentication state. Never substitute zero for absence, remove a section because refresh failed, or rank stale capacity as recommended.

## Export

Export normalized JSON or a CSV representation of a selected table/chart dataset after previewing included fields and record counts. Default to aliases and basenames. Full paths require explicit inclusion, and credentials, raw source payloads, conversation content, tool content, and raw diffs are never exportable.

## Narrow screens

The rail condenses into an accessible navigation control without removing profile switching. The page order remains:

1. current identity and continue/no-continue status;
2. immediate limit windows and resets;
3. recommended alternative and handoff action;
4. alerts;
5. optional trend and detailed analytics links.

Dense comparison tables become vertically stacked disclosure rows leading to accessible detail views. The page never requires horizontal scrolling at 200% zoom or common phone widths.

Navigation uses a profile/status bar plus bottom destinations for Overview, Profiles, and Alerts. Sessions, Analytics, and Settings remain keyboard- and screen-reader-accessible through More. Handoff stays in the capacity workflow rather than becoming navigation.

## Theme and density

Implement complete dark and light Signal Rail themes and follow the operating-system preference by default. Light mode is a deliberate translation of calibrated rails, semantic signal colors, provenance patterns, and restrained instrument surfaces—not a mechanical inversion. Forced/high-contrast modes remain valid independently.

Ship one accessible responsive density in the MVP. Do not trade 200% zoom, readable telemetry, or keyboard/touch targets for extra rows, and do not multiply component states with compact/comfortable preferences until evidence warrants them.

## Accessibility guardrails for the selected aesthetic

- Condensed display typography is limited to short labels or numerals; body copy and controls use a highly legible UI face.
- Signal colors always have text, icon, pattern, or position redundancy.
- Fine rules meet required contrast or are non-essential decoration.
- Dense telemetry text has user-scalable spacing and does not become the sole representation of critical status.
- Circular gauges are optional visual summaries; exact values and reset times remain readable text.

## Comp interpretation

The selected comp is compositional option one and the finish-review reference. Its synthetic identities, models, values, dates, ranking, and timezone are illustrative only. In particular, recommendation ranking must be produced by the compatibility and capacity policy rather than copied from the image.

Reference artifacts:

- `.impeccable/mocks/decision/signal-rail.webp`
- `.impeccable/mocks/decision/signal-rail-board.webp`
- `.impeccable/mocks/decision/signal-rail-hero.webp`

Before production UI implementation, expand the acceptance reference to wide and narrow Overview; Profiles/onboarding; Sessions/detail; Analytics capacity/compare with semantic tables; Alerts/Settings; Safe Continuation/handoff; and empty, stale, reauthentication, offline, and recovery states. Use available UI/UX skills explicitly during comp shaping, comp-to-code implementation, React optimization, and browser acceptance testing.
