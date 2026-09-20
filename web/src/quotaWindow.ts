// Provider slots are not plan names: derive a duration only from reported bounds.
export function quotaWindowLabel(
  observation: { window_start: string; window_end: string } | undefined,
  fallback: string,
): string {
  if (!observation) return fallback;
  const hours =
    (Date.parse(observation.window_end) - Date.parse(observation.window_start)) / 3_600_000;
  if (!Number.isFinite(hours) || hours <= 0) return fallback;
  if (hours === 168) return "Weekly window";
  if (hours >= 24 && Number.isInteger(hours / 24)) return `${hours / 24}-day window`;
  if (Number.isInteger(hours)) return `${hours}-hour window`;
  return fallback;
}
