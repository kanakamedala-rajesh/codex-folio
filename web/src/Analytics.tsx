import { useEffect, useEffectEvent, useMemo, useRef, useState, type RefObject } from "react";
import type {
  AnalyticsResponse,
  HistoryAggregate,
  HistoryResponse,
  SelectionResponse,
  UsageObservation,
  UsageSnapshotResponse,
} from "./generated/openapi";
import { analyticsCopy as c, provenanceCopy, stateCopy } from "./copy";

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

type HistoryReader = (profileId: string, from: string) => Promise<HistoryResponse>;

interface AnalyticsProps {
  data: AnalyticsResponse;
  selection: SelectionResponse | null;
  now: number;
  heading: RefObject<HTMLHeadingElement | null>;
  readHistory: HistoryReader;
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
    : (observation?.availability ?? availability?.state ?? "temporarily_unavailable");
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
    at.setUTCMonth(at.getUTCMonth() - 13);
    at.setUTCDate(1);
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
    const contradictory = aggregates.some((item) => item.availability === "contradictory");
    const selected = contradictory ? undefined : aggregates[aggregates.length - 1];
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
        {primary.map((path) => (
          <path key={path} d={path} stroke="var(--cyan)" strokeWidth="3" fill="none" />
        ))}
        {secondary.map((path) => (
          <path
            key={path}
            d={path}
            stroke="var(--magenta)"
            strokeWidth="3"
            strokeDasharray="9 6"
            fill="none"
          />
        ))}
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

export function Analytics({ data, selection, now, heading, readHistory, expired }: AnalyticsProps) {
  const [tab, setTab] = useState<"capacity" | "compare">("capacity");
  const [profileId, setProfileId] = useState(
    selection?.profile_id ?? data.candidates[0]?.profile_id ?? "",
  );
  const [range, setRange] = useState<HistoryRange>("13-months");
  const [windowFilter, setWindowFilter] = useState<WindowFilter>("both");
  const [aggregates, setAggregates] = useState<HistoryAggregate[]>([]);
  const [status, setStatus] = useState<string>(c.loading);
  const historyNow = useRef(now);
  const loadHistory = useEffectEvent(readHistory);
  const handleExpired = useEffectEvent(expired);

  useEffect(() => {
    if (tab !== "capacity" || !profileId) return;
    let cancelled = false;
    loadHistory(profileId, historyStart(range, historyNow.current))
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
  }, [profileId, range, tab]);

  const samples = useMemo(() => samplesFor(aggregates, windowFilter), [aggregates, windowFilter]);
  const profile = data.candidates.find((item) => item.profile_id === profileId);
  const snapshot = data.profiles.find((item) => item.profile_id === profileId);
  const primary = currentReading(snapshot, metricKeys[0]);
  const secondary = currentReading(snapshot, metricKeys[1]);

  return (
    <>
      <header className="mb-7 border-b border-rule pb-5 [&_p]:mb-0">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {tab === "capacity" ? c.title : c.compareTitle}
        </h1>
        <p className="mb-4 max-w-[75ch] text-muted">
          {tab === "capacity" ? c.subtitle : c.compareSubtitle}
        </p>
      </header>
      <nav className="mb-6 flex flex-wrap gap-3" aria-label={c.sections}>
        {c.tabs.map((item) => {
          const implemented = item === "Capacity" || item === "Compare";
          const selectedTab = tab === item.toLowerCase();
          return (
            <button
              key={item}
              disabled={!implemented}
              aria-current={selectedTab ? "page" : undefined}
              title={!implemented ? c.comingLater : undefined}
              onClick={() => {
                if (!implemented) return;
                const next = item.toLowerCase() as "capacity" | "compare";
                if (next === "capacity") setStatus(c.loading);
                setTab(next);
              }}
              className={`${button} aria-[current=page]:border-accent aria-[current=page]:text-accent`}
            >
              {item}
              {!implemented && <span className="sr-only"> · {c.comingLater}</span>}
            </button>
          );
        })}
      </nav>
      {tab === "compare" ? (
        <Compare data={data} selection={selection} now={now} />
      ) : (
        <>
          <div className="mb-6 flex flex-wrap gap-3 [&_label]:grid [&_label]:min-w-[min(100%,12rem)] [&_label]:gap-[0.4rem]">
            <label>
              {c.scope}
              <select
                value={profileId}
                onChange={(event) => {
                  setStatus(c.loading);
                  setProfileId(event.target.value);
                }}
                className={field}
              >
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
          </div>
          <p className="mb-6 border-b border-rule pb-5">
            {c.current}:{" "}
            <strong>
              {formatRemaining(primary.remaining)} {c.primaryShort}
            </strong>{" "}
            ·{" "}
            <strong>
              {formatRemaining(secondary.remaining)} {c.secondaryShort}
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
          <p className="mb-4 max-w-[75ch] text-muted">{c.displayZone(zone)}</p>
        </>
      )}
    </>
  );
}
