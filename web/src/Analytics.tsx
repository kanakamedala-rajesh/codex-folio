import { quotaWindowLabel } from "./quotaWindow";
import { useEffect, useEffectEvent, useMemo, useState, type RefObject } from "react";
import type {
  AnalyticsResponse,
  ActivityRecord,
  HistoryAggregate,
  HistoryResponse,
  ProjectEditRequest,
  ProjectIdentity,
  ProjectsResponse,
  SelectionResponse,
  UsageObservation,
  UsageSnapshotResponse,
} from "./generated/openapi";
import { analyticsCopy as c, provenanceCopy, stateCopy } from "./copy";
import { AnalyticsData, type AnalyticsDataView, type HistoryManager } from "./AnalyticsData";

const metricKeys = ["codex.primary.used_percent", "codex.secondary.used_percent"] as const;
const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const month = new Intl.DateTimeFormat(undefined, {
  month: "short",
  year: "numeric",
  timeZone: "UTC",
});
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
const button =
  "min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const field =
  "min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink w-full min-w-0 cursor-pointer";
const cell =
  "border-b border-rule px-1 py-3 text-left align-top wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]";

type HistoryRange = "30-days" | "90-days" | "13-months" | "all";
type WindowFilter = "both" | (typeof metricKeys)[number];

type HistoryReader = (
  profileId: string,
  projectId: string,
  from: string,
) => Promise<HistoryResponse>;
type ProjectReader = () => Promise<ProjectsResponse>;
type ProjectEditor = (request: ProjectEditRequest) => Promise<ProjectsResponse>;
type ActivityReader = (profileAlias: string, projectId: string) => Promise<ActivityRecord[]>;
type AnalyticsTab = "capacity" | "tokens" | "projects" | "models" | "activity" | "compare";

interface AnalyticsProps {
  data: AnalyticsResponse;
  selection: SelectionResponse | null;
  now: number;
  heading: RefObject<HTMLHeadingElement | null>;
  readHistory: HistoryReader;
  manageHistory: HistoryManager;
  readProjects: ProjectReader;
  editProject: ProjectEditor;
  readActivity: ActivityReader;
  readOverallHistory: () => Promise<AnalyticsResponse>;
  expired: (error: unknown) => void;
}

interface HistoryPoint {
  aggregates: HistoryAggregate[];
  aggregate?: HistoryAggregate;
  remaining: number | null;
}

interface HistorySample {
  key: string;
  at: Date;
  primary?: HistoryPoint;
  secondary?: HistoryPoint;
}

function label(value: string) {
  return stateCopy[value] ?? value.replaceAll("_", " ");
}

function provenance(value: string) {
  return provenanceCopy[value] ?? value;
}

function instant(value: string) {
  return value ? date.format(new Date(value)) : c.unavailable;
}

function age(value: string, now: number) {
  if (!value) return c.unavailable;
  const seconds = Math.floor((now - Date.parse(value)) / 1000);
  if (seconds < 0) return c.future;
  if (seconds < 60) return c.secondsAgo(number.format(seconds));
  if (seconds < 3600) return c.minutesAgo(number.format(Math.floor(seconds / 60)));
  return c.hoursAgo(number.format(Math.floor(seconds / 3600)));
}

function currentReading(snapshot: UsageSnapshotResponse | undefined, metricKey: string) {
  const observations = snapshot?.observations.filter((item) => item.metric_key === metricKey) ?? [];
  const availability = snapshot?.availability.find((item) => item.metric_key === metricKey);
  const observation = observations.length === 1 ? observations[0] : undefined;
  const contradictory =
    availability?.state === "contradictory" ||
    observations.length > 1 ||
    observations.some((item) => item.availability === "contradictory");
  const state = contradictory
    ? "contradictory"
    : (availability?.state ?? observation?.availability ?? "temporarily_unavailable");
  const compatible =
    !contradictory &&
    (state === "available" || state === "stale") &&
    observation?.unit === "percent" &&
    observation.value >= 0 &&
    observation.value <= 100;
  return {
    availability: state,
    observation,
    observations,
    remaining: compatible ? 100 - observation.value : null,
  };
}

function historyStart(range: HistoryRange, now: number) {
  if (range === "all") return "all";
  const at = new Date(now);
  if (range === "13-months") {
    at.setUTCDate(1);
    at.setUTCMonth(at.getUTCMonth() - 13);
    at.setUTCHours(0, 0, 0, 0);
  } else at.setUTCDate(at.getUTCDate() - (range === "90-days" ? 90 : 30));
  return at.toISOString();
}

function monthKey(value: string) {
  const at = new Date(value);
  return `${at.getUTCFullYear()}-${String(at.getUTCMonth() + 1).padStart(2, "0")}`;
}

function monthIndex(value: Date) {
  return value.getUTCFullYear() * 12 + value.getUTCMonth();
}

function samplesFor(aggregates: HistoryAggregate[], windowFilter: WindowFilter) {
  const points = new Map<string, HistorySample>();
  for (const aggregate of aggregates) {
    const index = metricKeys.indexOf(aggregate.metric.metric_key as (typeof metricKeys)[number]);
    if (
      index < 0 ||
      aggregate.metric.unit !== "percent" ||
      aggregate.value < 0 ||
      aggregate.value > 100 ||
      (windowFilter !== "both" && aggregate.metric.metric_key !== windowFilter)
    )
      continue;
    const key = monthKey(aggregate.last_captured_at);
    const existing = points.get(key) ?? {
      key,
      at: new Date(`${key}-01T00:00:00Z`),
    };
    const fieldName = index === 0 ? "primary" : "secondary";
    const prior = existing[fieldName];
    const aggregates = [...(prior?.aggregates ?? []), aggregate].sort(
      (left, right) =>
        Date.parse(left.last_captured_at) - Date.parse(right.last_captured_at) ||
        left.id.localeCompare(right.id),
    );
    const finalCapturedAt = aggregates[aggregates.length - 1].last_captured_at;
    const finalEvidence = aggregates.filter((item) => item.last_captured_at === finalCapturedAt);
    const contradictory = finalEvidence.some((item) => item.availability === "contradictory");
    const selected = contradictory ? undefined : finalEvidence[finalEvidence.length - 1];
    existing[fieldName] = {
      aggregates,
      aggregate: selected,
      remaining:
        selected && (selected.availability === "available" || selected.availability === "stale")
          ? 100 - selected.value
          : null,
    };
    points.set(key, existing);
  }
  const available = [...points.values()].sort(
    (left, right) => left.at.getTime() - right.at.getTime(),
  );
  if (!available.length) return available;
  const complete: HistorySample[] = [];
  const last = monthIndex(available[available.length - 1].at);
  const byMonth = new Map(available.map((sample) => [monthIndex(sample.at), sample]));
  for (let index = monthIndex(available[0].at); index <= last; index++) {
    const year = Math.floor(index / 12);
    const monthNumber = index % 12;
    complete.push(
      byMonth.get(index) ?? {
        key: `${year}-${String(monthNumber + 1).padStart(2, "0")}`,
        at: new Date(Date.UTC(year, monthNumber, 1)),
      },
    );
  }
  return complete;
}

function pathFor(samples: HistorySample[], fieldName: "primary" | "secondary") {
  if (!samples.length) return [];
  const first = monthIndex(samples[0].at);
  const span = Math.max(1, monthIndex(samples[samples.length - 1].at) - first);
  const paths: string[] = [];
  let path = "";
  let priorMonth = -2;
  for (const sample of samples) {
    const point = sample[fieldName];
    const currentMonth = monthIndex(sample.at);
    if (!point || point.remaining === null) {
      if (path) paths.push(path);
      path = "";
      priorMonth = currentMonth;
      continue;
    }
    const x = 42 + ((currentMonth - first) * 888) / span;
    const y = 220 - point.remaining * 2;
    if (!path || currentMonth !== priorMonth + 1) {
      if (path) paths.push(path);
      path = `M${x.toFixed(1)} ${y.toFixed(1)}`;
    } else {
      path += ` L${x.toFixed(1)} ${y.toFixed(1)}`;
    }
    priorMonth = currentMonth;
  }
  if (path) paths.push(path);
  return paths;
}

function pointText(point: HistoryPoint | undefined) {
  return point?.remaining === null || !point ? c.unavailable : `${number.format(point.remaining)}%`;
}

function pointState(sample: HistorySample) {
  if (!sample.primary && !sample.secondary) return c.noCompatibleSample;
  const states = new Set(
    [sample.primary, sample.secondary]
      .filter(Boolean)
      .flatMap((point) =>
        (point!.aggregate ? [point!.aggregate] : point!.aggregates).map((aggregate) =>
          aggregate.availability === "stale"
            ? `${c.lastKnown} · ${label(aggregate.availability)}`
            : label(aggregate.availability),
        ),
      ),
  );
  return [...states].join(" · ");
}

function qualifiedEvidence(provenanceValue: string, assumptions: string, uncertainty: string) {
  if (provenanceValue !== "Estimated Metric" && !assumptions && !uncertainty) return [];
  return [c.assumptions(assumptions || c.unavailable), c.uncertainty(uncertainty || c.unavailable)];
}

function pointEvidence(point: HistoryPoint | undefined) {
  if (!point) return c.unavailable;
  return (point.aggregate ? [point.aggregate] : point.aggregates)
    .map((aggregate) =>
      [
        `${number.format(aggregate.value)} ${aggregate.metric.unit}`,
        `${label(aggregate.source)} ${aggregate.source_version} · ${provenance(aggregate.provenance)}`,
        c.observed(instant(aggregate.last_observed_at)),
        c.captured(instant(aggregate.last_captured_at)),
        c.windowDetail(
          `${instant(aggregate.bucket_start)} — ${instant(aggregate.bucket_end)} · ${aggregate.timezone}`,
        ),
        ...qualifiedEvidence(aggregate.provenance, aggregate.assumptions, aggregate.uncertainty),
      ].join(" · "),
    )
    .join("; ");
}

function HistoryTable({ samples }: { samples: HistorySample[] }) {
  return (
    <table className="w-full border-collapse tabular-nums">
      <caption className="pt-[0.6rem] pb-4 text-left text-[0.9rem] text-muted">
        {c.historyCaption}
      </caption>
      <thead className="max-md:sr-only">
        <tr>
          {[c.month, c.primaryRemaining, c.secondaryRemaining, c.availability, c.source].map(
            (heading) => (
              <th key={heading} scope="col" className={`${cell} font-semibold`}>
                {heading}
              </th>
            ),
          )}
        </tr>
      </thead>
      <tbody>
        {samples.map((sample) => (
          <tr key={sample.key}>
            <th scope="row" className={`${cell} max-md:hidden font-semibold`}>
              {month.format(sample.at)}
            </th>
            <td colSpan={5} className="border-b border-rule py-2 md:hidden">
              <details className="py-2">
                <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                  {month.format(sample.at)} · {pointText(sample.primary)}
                </summary>
                <dl className="grid grid-cols-[minmax(8rem,0.8fr)_1fr] gap-x-4 gap-y-2 pb-3">
                  <dt className="text-muted">{c.secondaryRemaining}</dt>
                  <dd>{pointText(sample.secondary)}</dd>
                  <dt className="text-muted">{c.availability}</dt>
                  <dd>{pointState(sample)}</dd>
                  <dt className="text-muted">{c.source}</dt>
                  <dd>
                    {c.primaryShort}: {pointEvidence(sample.primary)}
                    <br />
                    {c.secondaryShort}: {pointEvidence(sample.secondary)}
                  </dd>
                </dl>
              </details>
            </td>
            <td className={`${cell} max-md:hidden`}>{pointText(sample.primary)}</td>
            <td className={`${cell} max-md:hidden`}>{pointText(sample.secondary)}</td>
            <td className={`${cell} max-md:hidden`}>{pointState(sample)}</td>
            <td className={`${cell} max-md:hidden text-sm`}>
              <span className="block">
                {c.primaryShort}: {pointEvidence(sample.primary)}
              </span>
              <span className="mt-2 block">
                {c.secondaryShort}: {pointEvidence(sample.secondary)}
              </span>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function HistoryChart({ samples }: { samples: HistorySample[] }) {
  if (!samples.length) return <p className="mb-4 max-w-[75ch]">{c.noHistory}</p>;
  const primary = pathFor(samples, "primary");
  const secondary = pathFor(samples, "secondary");
  return (
    <>
      <svg className="h-auto w-full" viewBox="0 0 980 260" role="img" aria-label={c.chartLabel}>
        <path d="M42 20V220H950" stroke="var(--rule)" fill="none" />
        <text x="2" y="25" fill="var(--muted)" fontSize="16">
          100
        </text>
        <text x="12" y="225" fill="var(--muted)" fontSize="16">
          0
        </text>
        {[
          { paths: primary, color: "var(--cyan)", dashed: false },
          { paths: secondary, color: "var(--magenta)", dashed: true },
        ].map(({ paths, color, dashed }) =>
          paths.map((path) => {
            const isolated = /^M([\d.]+) ([\d.]+)$/.exec(path);
            return (
              <g key={`${color}-${path}`}>
                <path
                  d={path}
                  stroke={color}
                  strokeWidth="3"
                  strokeDasharray={dashed ? "9 6" : undefined}
                  fill="none"
                />
                {isolated && (
                  <circle
                    cx={Number(isolated[1])}
                    cy={Number(isolated[2])}
                    r="5"
                    stroke={color}
                    strokeWidth="3"
                    fill={dashed ? "var(--bg)" : color}
                  />
                )}
              </g>
            );
          }),
        )}
        <text x="42" y="252" fill="var(--muted)" fontSize="16">
          {month.format(samples[0].at)}
        </text>
        <text x="825" y="252" fill="var(--muted)" fontSize="16">
          {month.format(samples[samples.length - 1].at)}
        </text>
      </svg>
      <p className="mb-4 max-w-[75ch]">{c.chartLegend}</p>
    </>
  );
}

function Compare({ data, selection, now }: Pick<AnalyticsProps, "data" | "selection" | "now">) {
  const apiCredit = data.aggregates.filter(
    (item) =>
      item.metric_key.includes("credit") ||
      item.metric_key.includes("spend") ||
      ["credit", "credits", "currency", "usd"].includes(item.unit.toLowerCase()),
  );
  return (
    <>
      <p className="my-6 border-y border-warning py-4">
        {c.combined} · {c.explicit} · {c.selectionRemains(selection?.alias ?? c.none)}
      </p>
      <table className="w-full border-collapse tabular-nums">
        <caption className="pt-[0.6rem] pb-4 text-left text-[0.9rem] text-muted">
          {c.compareCaption}
        </caption>
        <thead className="max-md:sr-only">
          <tr>
            {[c.profile, c.primaryRemaining, c.secondaryRemaining, c.evidenceState, c.source].map(
              (heading) => (
                <th key={heading} scope="col" className={`${cell} font-semibold first:min-w-28`}>
                  {heading}
                </th>
              ),
            )}
          </tr>
        </thead>
        <tbody>
          {data.candidates.map((candidate) => {
            const snapshot = data.profiles.find((item) => item.profile_id === candidate.profile_id);
            const primary = currentReading(snapshot, metricKeys[0]);
            const secondary = currentReading(snapshot, metricKeys[1]);
            const evidence = snapshot
              ? `${age(snapshot.captured_at, now)} · ${label(candidate.capacity_state)}`
              : label(candidate.capacity_state);
            return (
              <tr key={candidate.profile_id}>
                <th scope="row" className={`${cell} max-md:hidden min-w-28 font-semibold`}>
                  {candidate.alias}
                </th>
                <td colSpan={5} className="border-b border-rule py-2 md:hidden">
                  <details className="py-2">
                    <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                      {candidate.alias} · {formatRemaining(primary.remaining)}
                    </summary>
                    <dl className="grid grid-cols-[minmax(8rem,0.8fr)_1fr] gap-x-4 gap-y-2 pb-3">
                      <dt className="text-muted">{c.secondaryRemaining}</dt>
                      <dd>{formatRemaining(secondary.remaining)}</dd>
                      <dt className="text-muted">{c.evidenceState}</dt>
                      <dd>{evidence}</dd>
                      <dt className="text-muted">{c.source}</dt>
                      <dd>
                        {c.primaryShort}: {observationsEvidence(primary.observations)}
                        <br />
                        {c.secondaryShort}: {observationsEvidence(secondary.observations)}
                      </dd>
                    </dl>
                  </details>
                </td>
                <td className={`${cell} max-md:hidden`}>{formatRemaining(primary.remaining)}</td>
                <td className={`${cell} max-md:hidden`}>{formatRemaining(secondary.remaining)}</td>
                <td className={`${cell} max-md:hidden`}>{evidence}</td>
                <td className={`${cell} max-md:hidden text-sm`}>
                  <span className="block">
                    {c.primaryShort}: {observationsEvidence(primary.observations)}
                  </span>
                  <span className="mt-2 block">
                    {c.secondaryShort}: {observationsEvidence(secondary.observations)}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <section className="border-t border-rule py-6">
        <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
          {c.apiCredit}
        </h2>
        {apiCredit.length ? (
          apiCredit.map((item) => (
            <p key={item.metric_key} className="mb-4 max-w-[75ch]">
              {item.metric_key} · {number.format(item.value)} {item.unit} · {item.source_class} ·{" "}
              {c.profiles(item.profile_count)}
            </p>
          ))
        ) : (
          <p className="mb-4 max-w-[75ch]">{c.noApiCredit}</p>
        )}
        <p className="mb-4 max-w-[75ch] text-muted">{c.unitBoundary}</p>
      </section>
      <p className="mb-4 max-w-[75ch]">{c.deduplication}</p>
    </>
  );
}

function formatRemaining(value: number | null) {
  return value === null ? c.unavailable : `${number.format(value)}%`;
}

function observationWindows(observations: Array<UsageObservation | undefined>) {
  const windows = observations
    .filter(Boolean)
    .map((item) =>
      item!.window_start && item!.window_end
        ? `${instant(item!.window_start)} — ${instant(item!.window_end)} · ${item!.window_timezone}`
        : c.resetUnavailable,
    );
  return windows.length ? windows.join("; ") : c.resetUnavailable;
}

function observationEvidence(observation: UsageObservation | undefined) {
  if (!observation) return c.unavailable;
  return [
    `${number.format(observation.value)} ${observation.unit}`,
    `${label(observation.source)} ${observation.source_version} · ${provenance(observation.provenance)}`,
    `${label(observation.availability)} · ${label(observation.freshness)}`,
    c.observed(instant(observation.observed_at)),
    c.captured(instant(observation.captured_at)),
    c.windowDetail(observationWindows([observation])),
    ...qualifiedEvidence(observation.provenance, observation.assumptions, observation.uncertainty),
  ].join(" · ");
}

function observationsEvidence(observations: UsageObservation[]) {
  return observations.length
    ? observations.map((observation) => observationEvidence(observation)).join("; ")
    : c.unavailable;
}

function activityType(value: string) {
  return value === "managed_launch" ? c.managedLaunch : c.observedSession;
}

function activityEvidence(record: ActivityRecord) {
  return c.sourceEvidence(
    label(record.source),
    record.source_version,
    record.record_type === "managed_launch"
      ? provenanceCopy["Locally-derived Metric"]
      : provenance(record.provenance),
  );
}

function contributingEvidence(records: ActivityRecord[]) {
  return [...new Set(records.map(activityEvidence))].join("; ") || c.unavailable;
}

function currentProject(record: ActivityRecord, projects: ProjectIdentity[]) {
  const project = projects.find((item) => item.project_id === record.project_id);
  return (
    project?.alias ||
    project?.basename ||
    record.project_alias ||
    record.project_basename ||
    c.unavailable
  );
}

function filteredActivity(
  records: ActivityRecord[],
  profileId: string,
  projectId: string,
  range: HistoryRange,
  now: number,
) {
  const from = historyStart(range, now);
  const threshold = from === "all" ? Number.NEGATIVE_INFINITY : Date.parse(from);
  return records.filter(
    (record) =>
      (!profileId || record.profile_id === profileId) &&
      (!projectId || record.project_id === projectId) &&
      Date.parse(record.last_observed_at || record.started_at) >= threshold,
  );
}

function EvidenceChart({
  rows,
  label: chartLabel,
}: {
  rows: { label: string; value: number }[];
  label: string;
}) {
  if (!rows.length) return null;
  const maximum = Math.max(...rows.map((row) => row.value), 1);
  const height = 36 + rows.length * 42;
  return (
    <svg
      className="h-auto w-full max-w-[60rem]"
      viewBox={`0 0 900 ${height}`}
      role="img"
      aria-label={chartLabel}
    >
      {rows.map((row, index) => {
        const y = 18 + index * 42;
        const width = (Math.max(0, row.value) / maximum) * 590;
        return (
          <g key={row.label}>
            <text x="0" y={y + 18} fill="var(--text)" fontSize="15">
              {row.label.length > 26 ? `${row.label.slice(0, 25)}…` : row.label}
            </text>
            <rect x="220" y={y} width="590" height="24" fill="none" stroke="var(--rule)" />
            <rect x="220" y={y} width={width} height="24" fill="var(--cyan)" />
            <text x="825" y={y + 18} fill="var(--text)" fontSize="15">
              {number.format(row.value)}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

function RecordTable({
  records,
  projects,
  mode,
}: {
  records: ActivityRecord[];
  projects: ProjectIdentity[];
  mode: "tokens" | "activity";
}) {
  const visible =
    mode === "tokens" ? records.filter((record) => record.tokens_used !== "") : records;
  if (!visible.length)
    return <p className="mb-4 max-w-[75ch]">{mode === "tokens" ? c.noTokens : c.noActivity}</p>;
  return (
    <table className="w-full border-collapse tabular-nums">
      <caption className="pt-[0.6rem] pb-4 text-left text-[0.9rem] text-muted">
        {mode === "tokens" ? c.tokensSubtitle : c.activitySubtitle}
      </caption>
      <thead className="max-md:sr-only">
        <tr>
          {[
            c.lastObserved,
            c.recordType,
            c.profile,
            c.project,
            mode === "tokens" ? c.tokens : c.lifecycle,
            c.evidence,
          ].map((heading) => (
            <th key={heading} scope="col" className={`${cell} font-semibold`}>
              {heading}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {visible.map((record) => {
          const evidence = activityEvidence(record);
          return (
            <tr key={record.id}>
              <th scope="row" className={`${cell} max-md:hidden font-semibold`}>
                {instant(record.last_observed_at || record.started_at)}
              </th>
              <td colSpan={6} className="border-b border-rule py-2 md:hidden">
                <details className="py-2">
                  <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                    {activityType(record.record_type)} · {record.profile_alias}
                  </summary>
                  <dl className="grid grid-cols-[minmax(7rem,0.7fr)_1fr] gap-x-4 gap-y-2 pb-3">
                    <dt className="text-muted">{c.lastObserved}</dt>
                    <dd>{instant(record.last_observed_at || record.started_at)}</dd>
                    <dt className="text-muted">{c.project}</dt>
                    <dd>{currentProject(record, projects)}</dd>
                    <dt className="text-muted">{mode === "tokens" ? c.tokens : c.lifecycle}</dt>
                    <dd>
                      {mode === "tokens"
                        ? record.tokens_used || c.unsupported
                        : label(record.lifecycle) || c.unavailable}
                    </dd>
                    <dt className="text-muted">{c.evidence}</dt>
                    <dd>
                      {evidence}
                      {record.correlation_state
                        ? ` · ${c.correlation}: ${label(record.correlation_state)}`
                        : ""}
                    </dd>
                  </dl>
                </details>
              </td>
              <td className={`${cell} max-md:hidden`}>{activityType(record.record_type)}</td>
              <td className={`${cell} max-md:hidden`}>{record.profile_alias}</td>
              <td className={`${cell} max-md:hidden`}>{currentProject(record, projects)}</td>
              <td className={`${cell} max-md:hidden`}>
                {mode === "tokens"
                  ? record.tokens_used || c.unsupported
                  : label(record.lifecycle) || c.unavailable}
              </td>
              <td className={`${cell} max-md:hidden text-sm`}>
                {evidence}
                {record.correlation_state
                  ? ` · ${c.correlation}: ${label(record.correlation_state)}`
                  : ""}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function TokenView({
  records,
  projects,
  summary,
}: {
  records: ActivityRecord[];
  projects: ProjectIdentity[];
  summary: AnalyticsResponse | null;
}) {
  const supported = records.filter((record) => record.tokens_used !== "");
  return (
    <section className="border-t border-rule py-6">
      <h2 className="mb-4 text-[1.4rem] font-bold">{c.tokensTitle}</h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.tokensSubtitle}</p>
      {summary && <HistoricalSummary data={summary} />}
      <EvidenceChart
        label={c.tokensChart}
        rows={supported.map((record) => ({
          label: `${record.profile_alias} · ${currentProject(record, projects)}`,
          value: Number(record.tokens_used),
        }))}
      />
      {supported.length ? <p className="mb-4 text-sm text-muted">{c.chartTableNote}</p> : null}
      <RecordTable records={records} projects={projects} mode="tokens" />
    </section>
  );
}

function HistoricalSummary({ data }: { data: AnalyticsResponse }) {
  const metrics = data.historical_metrics ?? [];
  return (
    <section aria-label={c.historicalSummary} className="mb-6 rounded border border-rule p-4">
      <h3 className="mb-2 font-semibold">{c.historicalSummary}</h3>
      <p className="mb-3 max-w-[75ch] text-sm text-muted">{c.historicalSummaryDetail}</p>
      {metrics.length ? (
        <dl className="grid gap-4">
          {metrics.map((metric) => (
            <div key={`${metric.metric_key}:${metric.source}:${metric.source_version}`}>
              <dt className="font-semibold">
                {metric.metric_key === "codex.local.tokens_used" ? c.tokens : metric.metric_key} ·{" "}
                {metric.source === "local_metadata" ? c.historicalSource : metric.source}
                {metric.source_version ? ` ${metric.source_version}` : ""}
              </dt>
              <dd className="mt-1">
                {metric.value === undefined
                  ? c.unavailable
                  : `${number.format(BigInt(metric.value))} ${metric.unit}`}
                {` · ${metric.availability} · ${c.historicalFreshness}`}
              </dd>
              <dd className="mt-1 text-sm text-muted">
                {c.historicalCoverage(metric.measured_session_count, metric.session_count)} ·{" "}
                {c.unassignedContribution}:{" "}
                {metric.unassigned_value !== undefined
                  ? `${number.format(BigInt(metric.unassigned_value))} ${metric.unit}`
                  : c.unavailable}
              </dd>
              <dd className="mt-1 text-sm text-muted">
                {c.historicalPeriod}: {instant(metric.coverage_start_at)} –{" "}
                {instant(metric.coverage_end_at)}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>{c.noHistoricalSummary}</p>
      )}
    </section>
  );
}

function ModelView({ records }: { records: ActivityRecord[] }) {
  const groups = new Map<string, ActivityRecord[]>();
  for (const record of records)
    if (record.model) groups.set(record.model, [...(groups.get(record.model) ?? []), record]);
  const rows = [...groups.entries()].sort(([left], [right]) => left.localeCompare(right));
  return (
    <section className="border-t border-rule py-6">
      <h2 className="mb-4 text-[1.4rem] font-bold">{c.modelsTitle}</h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.modelsSubtitle}</p>
      {!rows.length ? (
        <p>{c.noModels}</p>
      ) : (
        <>
          <EvidenceChart
            label={c.modelsChart}
            rows={rows.map(([name, items]) => ({ label: name, value: items.length }))}
          />
          <p className="mb-4 text-sm text-muted">{c.chartTableNote}</p>
          <table className="w-full border-collapse">
            <caption className="sr-only">{c.modelsSubtitle}</caption>
            <thead className="max-md:sr-only">
              <tr>
                {[c.model, c.records, c.lastObserved, c.evidence].map((heading) => (
                  <th key={heading} scope="col" className={`${cell} font-semibold`}>
                    {heading}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map(([name, items]) => {
                const latest = [...items].sort(
                  (left, right) =>
                    Date.parse(right.last_observed_at) - Date.parse(left.last_observed_at),
                )[0];
                return (
                  <tr key={name}>
                    <th scope="row" className={`${cell} max-md:hidden font-semibold`}>
                      {name}
                    </th>
                    <td colSpan={4} className="border-b border-rule py-2 md:hidden">
                      <details className="py-2">
                        <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                          {name} · {number.format(items.length)}
                        </summary>
                        <dl className="grid grid-cols-[minmax(7rem,0.7fr)_1fr] gap-x-4 gap-y-2 pb-3">
                          <dt className="text-muted">{c.records}</dt>
                          <dd>{number.format(items.length)}</dd>
                          <dt className="text-muted">{c.lastObserved}</dt>
                          <dd>{instant(latest.last_observed_at)}</dd>
                          <dt className="text-muted">{c.evidence}</dt>
                          <dd>{contributingEvidence(items)}</dd>
                        </dl>
                      </details>
                    </td>
                    <td className={`${cell} max-md:hidden`}>{items.length}</td>
                    <td className={`${cell} max-md:hidden`}>{instant(latest.last_observed_at)}</td>
                    <td className={`${cell} max-md:hidden text-sm`}>
                      {contributingEvidence(items)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}

function ProjectView({
  projects,
  records,
  loadState,
  editProject,
  onError,
}: {
  projects: ProjectIdentity[];
  records: ActivityRecord[];
  loadState: "loading" | "loaded" | "failed";
  editProject: ProjectEditor;
  onError: (error: unknown) => void;
}) {
  const [editing, setEditing] = useState("");
  const [alias, setAlias] = useState("");
  const [message, setMessage] = useState("");
  const counts = new Map<string, number>();
  for (const record of records)
    if (record.project_id) counts.set(record.project_id, (counts.get(record.project_id) ?? 0) + 1);
  async function save(projectId: string) {
    try {
      await editProject({ project_id: projectId, alias });
      setEditing("");
      setMessage(c.aliasSaved);
    } catch (error) {
      setMessage(c.aliasFailed);
      onError(error);
    }
  }
  if (!projects.length && loadState !== "loaded") {
    return (
      <section className="border-t border-rule py-6">
        <h2 className="mb-4 text-[1.4rem] font-bold">{c.projectsTitle}</h2>
        <p className="mb-4 max-w-[75ch] text-muted">{c.projectsSubtitle}</p>
        <p className="mb-4 max-w-[75ch]">
          {loadState === "loading" ? c.projectsLoading : c.projectsInitialLoadFailed}
        </p>
      </section>
    );
  }
  if (!projects.length) return <p className="mb-4 max-w-[75ch]">{c.noProjects}</p>;
  return (
    <section className="border-t border-rule py-6">
      <h2 className="mb-4 text-[1.4rem] font-bold">{c.projectsTitle}</h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.projectsSubtitle}</p>
      <EvidenceChart
        label={c.projectsChart}
        rows={projects.map((project) => ({
          label: project.alias || project.basename,
          value: counts.get(project.project_id) ?? 0,
        }))}
      />
      <p className="mb-4 text-sm text-muted">{c.chartTableNote}</p>
      <p role="status" className="mb-4">
        {message}
      </p>
      <table className="w-full border-collapse">
        <caption className="sr-only">{c.projectPrivacy}</caption>
        <thead className="max-md:sr-only">
          <tr>
            {[c.alias, c.basename, c.records, c.lastObserved, c.evidence, c.editAlias].map(
              (heading) => (
                <th key={heading} scope="col" className={`${cell} font-semibold`}>
                  {heading}
                </th>
              ),
            )}
          </tr>
        </thead>
        <tbody>
          {projects.map((project) => {
            const latest = records
              .filter((record) => record.project_id === project.project_id)
              .sort(
                (left, right) =>
                  Date.parse(right.last_observed_at) - Date.parse(left.last_observed_at),
              )[0];
            const projectRecords = records.filter(
              (record) => record.project_id === project.project_id,
            );
            const aliasControl = (suffix: string) =>
              editing === project.project_id ? (
                <form
                  className="flex flex-wrap gap-2"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void save(project.project_id);
                  }}
                >
                  <label
                    className="sr-only"
                    htmlFor={`project-alias-${suffix}-${project.project_id}`}
                  >
                    {c.alias}
                  </label>
                  <input
                    id={`project-alias-${suffix}-${project.project_id}`}
                    value={alias}
                    onChange={(event) => setAlias(event.target.value)}
                    required
                    maxLength={120}
                    className={field}
                  />
                  <button className={button} type="submit">
                    {c.saveAlias}
                  </button>
                  <button className={button} type="button" onClick={() => setEditing("")}>
                    {c.cancelAlias}
                  </button>
                </form>
              ) : (
                <button
                  className={button}
                  onClick={() => {
                    setAlias(project.alias || project.basename);
                    setEditing(project.project_id);
                    setMessage("");
                  }}
                >
                  {c.editAlias}
                </button>
              );
            return (
              <tr key={project.project_id}>
                <th scope="row" className={`${cell} max-md:hidden font-semibold`}>
                  {project.alias || project.basename}
                </th>
                <td colSpan={6} className="border-b border-rule py-2 md:hidden">
                  <details className="py-2">
                    <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                      {project.alias || project.basename} · {number.format(projectRecords.length)}
                    </summary>
                    <dl className="grid grid-cols-[minmax(7rem,0.7fr)_1fr] gap-x-4 gap-y-2 pb-3">
                      <dt className="text-muted">{c.basename}</dt>
                      <dd>{project.basename}</dd>
                      <dt className="text-muted">{c.records}</dt>
                      <dd>{number.format(projectRecords.length)}</dd>
                      <dt className="text-muted">{c.lastObserved}</dt>
                      <dd>{latest ? instant(latest.last_observed_at) : c.unavailable}</dd>
                      <dt className="text-muted">{c.evidence}</dt>
                      <dd>{contributingEvidence(projectRecords)}</dd>
                      <dt className="text-muted">{c.editAlias}</dt>
                      <dd>{aliasControl("narrow")}</dd>
                    </dl>
                  </details>
                </td>
                <td className={`${cell} max-md:hidden`}>{project.basename}</td>
                <td className={`${cell} max-md:hidden`}>{projectRecords.length}</td>
                <td className={`${cell} max-md:hidden`}>
                  {latest ? instant(latest.last_observed_at) : c.unavailable}
                </td>
                <td className={`${cell} max-md:hidden text-sm`}>
                  {contributingEvidence(projectRecords)}
                </td>
                <td className={`${cell} max-md:hidden`}>{aliasControl("wide")}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="mt-4 max-w-[75ch] text-muted">{c.projectPrivacy}</p>
    </section>
  );
}

export function Analytics({
  data,
  selection,
  now,
  heading,
  readHistory,
  manageHistory,
  expired,
  readProjects,
  editProject,
  readActivity,
  readOverallHistory,
}: AnalyticsProps) {
  const [tab, setTab] = useState<AnalyticsTab>("capacity");
  const [dataView, setDataView] = useState<AnalyticsDataView | null>(null);
  const [profileId, setProfileId] = useState(
    selection?.profile_id ?? data.candidates[0]?.profile_id ?? "",
  );
  const [range, setRange] = useState<HistoryRange>("13-months");
  const [windowFilter, setWindowFilter] = useState<WindowFilter>("both");
  const [projectId, setProjectId] = useState("");
  const [projects, setProjects] = useState<ProjectIdentity[]>([]);
  const [activity, setActivity] = useState<ActivityRecord[]>(data.activity);
  const [overallHistory, setOverallHistory] = useState<AnalyticsResponse | null>(null);
  const [aggregates, setAggregates] = useState<HistoryAggregate[]>([]);
  const [status, setStatus] = useState<string>(c.loading);
  const [activityStatus, setActivityStatus] = useState(c.activityRetained(data.activity.length));
  const [projectStatus, setProjectStatus] = useState<string>(c.projectsLoading);
  const [projectLoadState, setProjectLoadState] = useState<"loading" | "loaded" | "failed">(
    "loading",
  );
  const [historyNow] = useState(now);
  const [dataRevision, setDataRevision] = useState(0);
  const loadHistory = useEffectEvent(readHistory);
  const handleExpired = useEffectEvent(expired);
  const loadProjects = useEffectEvent(readProjects);
  const loadActivity = useEffectEvent(readActivity);
  const loadOverallHistory = useEffectEvent(readOverallHistory);

  useEffect(() => {
    if (tab !== "tokens" || profileId || projectId) return;
    let cancelled = false;
    void loadOverallHistory()
      .then((result) => {
        if (!cancelled) setOverallHistory(result);
      })
      .catch((error) => {
        if (!cancelled) {
          setOverallHistory(null);
          handleExpired(error);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [tab, profileId, projectId, dataRevision]);

  useEffect(() => {
    let cancelled = false;
    loadProjects()
      .then((result) => {
        if (!cancelled) {
          setProjects(result.projects);
          setProjectLoadState("loaded");
          setProjectStatus(c.projectsLoaded(result.projects.length));
        }
      })
      .catch((error) => {
        if (cancelled) return;
        setProjectLoadState("failed");
        setProjectStatus(c.projectsInitialLoadFailed);
        handleExpired(error);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (tab === "capacity" || tab === "compare") return;
    let cancelled = false;
    const alias = data.candidates.find((item) => item.profile_id === profileId)?.alias ?? "";
    loadActivity(alias, projectId)
      .then((result) => {
        if (!cancelled) {
          setActivity(result);
          setActivityStatus(c.activityLoaded(result.length));
        }
      })
      .catch((error) => {
        if (cancelled) return;
        setActivityStatus(c.activityLoadFailed);
        handleExpired(error);
      });
    return () => {
      cancelled = true;
    };
  }, [data.candidates, dataRevision, profileId, projectId, range, tab]);

  useEffect(() => {
    if (tab !== "capacity" || !profileId) return;
    let cancelled = false;
    // Provider capacity is profile/window scoped. A Project Identity selected
    // for the other analytics tabs must not exclude that shared quota history.
    loadHistory(profileId, "*", historyStart(range, historyNow))
      .then((result) => {
        if (cancelled) return;
        setAggregates(result.aggregates ?? []);
        setStatus(c.loaded(result.aggregates?.length ?? 0));
      })
      .catch((error) => {
        if (cancelled) return;
        setAggregates([]);
        setStatus(c.loadFailed);
        handleExpired(error);
      });
    return () => {
      cancelled = true;
    };
  }, [dataRevision, historyNow, profileId, projectId, range, tab]);

  const samples = useMemo(() => samplesFor(aggregates, windowFilter), [aggregates, windowFilter]);
  const profile = data.candidates.find((item) => item.profile_id === profileId);
  const selectedProject = projects.find((item) => item.project_id === projectId);
  const snapshot = data.profiles.find((item) => item.profile_id === profileId);
  const primary = currentReading(snapshot, metricKeys[0]);
  const secondary = currentReading(snapshot, metricKeys[1]);
  const visibleActivity = useMemo(
    () => filteredActivity(activity, profileId, projectId, range, now),
    [activity, profileId, projectId, range, now],
  );
  const visibleProjects = projectId
    ? projects.filter((item) => item.project_id === projectId)
    : projects;
  const title =
    tab === "capacity"
      ? c.title
      : tab === "compare"
        ? c.compareTitle
        : tab === "tokens"
          ? c.tokensTitle
          : tab === "projects"
            ? c.projectsTitle
            : tab === "models"
              ? c.modelsTitle
              : c.activityTitle;
  const subtitle =
    tab === "capacity"
      ? c.subtitle
      : tab === "compare"
        ? c.compareSubtitle
        : tab === "tokens"
          ? c.tokensSubtitle
          : tab === "projects"
            ? c.projectsSubtitle
            : tab === "models"
              ? c.modelsSubtitle
              : c.activitySubtitle;
  const editProjectAndRefresh: ProjectEditor = async (request) => {
    const result = await editProject(request);
    setProjects(result.projects);
    return result;
  };

  if (dataView) {
    return (
      <AnalyticsData
        view={dataView}
        heading={heading}
        candidates={data.candidates}
        projects={projects}
        profileId={profileId}
        projectId={projectId}
        from={historyStart(range, historyNow)}
        manageHistory={manageHistory}
        expired={expired}
        dataChanged={() => setDataRevision((value) => value + 1)}
        close={() => {
          setDataView(null);
          requestAnimationFrame(() => heading.current?.focus());
        }}
      />
    );
  }

  return (
    <>
      <header className="mb-7 border-b border-rule pb-5 [&_p]:mb-0">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {title}
        </h1>
        <p className="mb-4 max-w-[75ch] text-muted">{subtitle}</p>
      </header>
      <nav className="mb-6 flex flex-wrap gap-3" aria-label={c.sections}>
        {c.tabs.map((item) => {
          const selectedTab = tab === item.toLowerCase();
          return (
            <button
              key={item}
              aria-current={selectedTab ? "page" : undefined}
              onClick={() => {
                const next = item.toLowerCase() as AnalyticsTab;
                if (next === "capacity") setStatus(c.loading);
                else if (next !== "compare") setActivityStatus(c.activityLoading);
                if (next === "capacity" && !profileId)
                  setProfileId(selection?.profile_id ?? data.candidates[0]?.profile_id ?? "");
                setTab(next);
              }}
              className={`${button} aria-[current=page]:border-accent aria-[current=page]:text-accent`}
            >
              {item}
            </button>
          );
        })}
      </nav>
      {tab === "compare" ? (
        <Compare data={data} selection={selection} now={now} />
      ) : (
        <>
          <details open className="mb-6 border-b border-rule pb-4">
            <summary className="min-h-11 cursor-pointer py-2 font-semibold">
              Filters · {profile?.alias ?? (tab === "capacity" ? c.none : c.overallHistory)} ·{" "}
              {range === "30-days"
                ? c.last30Days
                : range === "90-days"
                  ? c.last90Days
                  : range === "13-months"
                    ? c.last13Months
                    : c.allHistory}{" "}
              ·{" "}
              {tab === "capacity"
                ? c.allProjects
                : selectedProject
                  ? selectedProject.alias || selectedProject.basename
                  : c.allProjects}
              {tab === "capacity" && (
                <>
                  {" "}
                  ·{" "}
                  {windowFilter === "both"
                    ? c.bothWindows
                    : windowFilter === metricKeys[0]
                      ? c.primary
                      : c.secondary}
                </>
              )}
            </summary>
            <div className="mb-6 flex flex-wrap gap-3 [&_label]:grid [&_label]:min-w-[min(100%,12rem)] [&_label]:gap-[0.4rem]">
              <label>
                {c.scope}
                <select
                  value={profileId}
                  onChange={(event) => {
                    setStatus(c.loading);
                    setActivityStatus(c.activityLoading);
                    setProfileId(event.target.value);
                  }}
                  className={field}
                >
                  {tab !== "capacity" && <option value="">{c.overallHistory}</option>}
                  {data.candidates.map((item) => (
                    <option key={item.profile_id} value={item.profile_id}>
                      {item.alias}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                {c.history}
                <select
                  value={range}
                  onChange={(event) => {
                    setStatus(c.loading);
                    setActivityStatus(c.activityLoading);
                    setRange(event.target.value as HistoryRange);
                  }}
                  className={field}
                >
                  <option value="30-days">{c.last30Days}</option>
                  <option value="90-days">{c.last90Days}</option>
                  <option value="13-months">{c.last13Months}</option>
                  <option value="all">{c.allHistory}</option>
                </select>
              </label>
              {tab !== "capacity" ? (
                <label>
                  {c.project}
                  <select
                    value={projectId}
                    onChange={(event) => {
                      setStatus(c.loading);
                      setActivityStatus(c.activityLoading);
                      setProjectId(event.target.value);
                    }}
                    className={field}
                  >
                    <option value="">{c.allProjects}</option>
                    {projects.map((item) => (
                      <option key={item.project_id} value={item.project_id}>
                        {item.alias || item.basename}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {tab === "capacity" ? (
                <label>
                  {c.window}
                  <select
                    value={windowFilter}
                    onChange={(event) => setWindowFilter(event.target.value as WindowFilter)}
                    className={field}
                  >
                    <option value="both">{c.bothWindows}</option>
                    <option value={metricKeys[0]}>{c.primary}</option>
                    <option value={metricKeys[1]}>{c.secondary}</option>
                  </select>
                </label>
              ) : null}
            </div>
          </details>
          {tab !== "capacity" ? (
            <p role="status" className="mb-4 max-w-[75ch] text-muted">
              {activityStatus} {projectStatus}
            </p>
          ) : null}
          {tab === "capacity" ? (
            <>
              <p className="mb-6 border-b border-rule pb-5">
                {c.current}:{" "}
                <strong>
                  {formatRemaining(primary.remaining)}{" "}
                  {quotaWindowLabel(primary.observations[0], c.primary)}
                </strong>{" "}
                ·{" "}
                <strong>
                  {formatRemaining(secondary.remaining)}{" "}
                  {quotaWindowLabel(secondary.observations[0], c.secondary)}
                </strong>{" "}
                · {label(primary.availability)} · {label(secondary.availability)}
                <small className="mt-2 block text-sm text-muted">
                  {c.primaryShort}: {observationsEvidence(primary.observations)}
                  <br />
                  {c.secondaryShort}: {observationsEvidence(secondary.observations)}
                </small>
              </p>
              <section className="border-t border-rule py-6">
                <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                  {c.capacityHistory}
                </h2>
                <p className="mb-4 max-w-[75ch] text-muted">
                  {c.monthlyFinal(samples.length)} · {profile?.alias ?? c.none} · {c.bucketZone}
                </p>
                <p role="status" className="mb-4 max-w-[75ch] text-muted">
                  {status}
                </p>
                <HistoryChart samples={samples} />
                {samples.length ? <HistoryTable samples={samples} /> : null}
              </section>
              <p className="mb-4 max-w-[75ch] text-muted">{c.boundary}</p>
            </>
          ) : tab === "tokens" ? (
            <TokenView
              records={visibleActivity}
              projects={projects}
              summary={!profileId && !projectId ? overallHistory : null}
            />
          ) : tab === "projects" ? (
            <ProjectView
              projects={visibleProjects}
              records={visibleActivity}
              loadState={projectLoadState}
              editProject={editProjectAndRefresh}
              onError={expired}
            />
          ) : tab === "models" ? (
            <ModelView records={visibleActivity} />
          ) : (
            <section className="border-t border-rule py-6">
              <h2 className="mb-4 text-[1.4rem] font-bold">{c.activityTitle}</h2>
              <p className="mb-4 max-w-[75ch] text-muted">
                {c.observedRecords(visibleActivity.length)}
              </p>
              <EvidenceChart
                label={c.activitySubtitle}
                rows={["managed_launch", "observed_session"].map((kind) => ({
                  label: activityType(kind),
                  value: visibleActivity.filter((record) => record.record_type === kind).length,
                }))}
              />
              <p className="mb-4 text-sm text-muted">{c.chartTableNote}</p>
              <RecordTable records={visibleActivity} projects={projects} mode="activity" />
            </section>
          )}
          <p className="mb-4 max-w-[75ch] text-muted">{c.displayZone(zone)}</p>
          <section className="mt-6 border-t border-rule py-6">
            <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
              {c.localData}
            </h2>
            <p className="mb-4 max-w-[75ch] text-muted">{c.localDataDetail}</p>
            <div className="flex flex-wrap gap-3">
              <button className={button} onClick={() => setDataView("export")}>
                {c.previewAnalyticsExport}
              </button>
              <button className={button} onClick={() => setDataView("retention")}>
                {c.manageRetention}
              </button>
              <button className={button} onClick={() => setDataView("purge")}>
                {c.previewScopedPurge}
              </button>
            </div>
          </section>
        </>
      )}
    </>
  );
}
