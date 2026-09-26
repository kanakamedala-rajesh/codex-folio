import type { ActivityRecord, HistoricalMetric } from "./generated/openapi";

// Summarize the same filtered records shown by the chart and table. Source
// versions remain separate; missing measurements never become measured zero.
export function summarizeHistory(records: ActivityRecord[]): HistoricalMetric[] {
  const groups = new Map<string, HistoricalMetric>();
  for (const record of records) {
    if (record.record_type !== "observed_session" || record.source !== "local_metadata") continue;
    for (const metric of record.historical_metrics ?? []) {
      const key = JSON.stringify([
        metric.metric_key,
        metric.unit,
        metric.source,
        metric.source_version,
      ]);
      let group = groups.get(key);
      if (!group) {
        group = {
          metric_key: metric.metric_key,
          unit: metric.unit,
          source: metric.source,
          source_version: metric.source_version,
          freshness: metric.freshness,
          availability: "absent",
          session_count: 0,
          measured_session_count: 0,
          unassigned_session_count: 0,
          coverage_start_at: metric.coverage_start_at,
          coverage_end_at: metric.coverage_end_at,
        };
        groups.set(key, group);
      }
      group.session_count++;
      if (!record.profile_id) group.unassigned_session_count++;
      if (Date.parse(metric.coverage_start_at) < Date.parse(group.coverage_start_at))
        group.coverage_start_at = metric.coverage_start_at;
      if (Date.parse(metric.coverage_end_at) > Date.parse(group.coverage_end_at))
        group.coverage_end_at = metric.coverage_end_at;
      if (metric.value !== undefined) {
        group.measured_session_count++;
        group.value = (BigInt(group.value ?? "0") + BigInt(metric.value)).toString();
        if (!record.profile_id)
          group.unassigned_value = (
            BigInt(group.unassigned_value ?? "0") + BigInt(metric.value)
          ).toString();
      }
      group.availability =
        group.measured_session_count === 0
          ? "absent"
          : group.measured_session_count < group.session_count
            ? "partial"
            : "available";
    }
  }
  return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([, value]) => value);
}
