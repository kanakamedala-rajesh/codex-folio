import { useEffect, useEffectEvent, useRef, useState } from "react";
import {
  createCodexFolioApiClient,
  UsageRefreshError,
  type AnalyticsResponse,
  type ActivityRecord,
  type ProfileAuthenticationRequest,
  type ProfileEditRequest,
  type ProfileLifecycleRecord,
  type ProfileLifecycleRequest,
  type ProfileSummary,
  type ProjectIdentity,
  type SelectionResponse,
  type UsageSnapshotResponse,
} from "./generated/openapi";
import { copy as c, stateCopy, provenanceCopy } from "./copy";
import { Profiles } from "./Profiles";
import { Launch, type LaunchTarget } from "./Launch";
import "./styles.css";

const api = createCodexFolioApiClient("", async (input, init) => {
  const response = await fetch(input, init);
  if (!response.ok) {
    const failure = (await response.json()) as { code: string; message: string };
    throw new UsageRefreshError(failure.code, response.status, failure.message);
  }
  return response;
});
const metrics = ["codex.primary.used_percent", "codex.secondary.used_percent"];
const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
const label = (value: string) => stateCopy[value] ?? value.replaceAll("_", " ");
const provenance = (value: string) => provenanceCopy[value] ?? value;
function instant(value: string) {
  return value ? date.format(new Date(value)) : c.ageUnknown;
}
function age(value: string, now: number) {
  if (!value) return c.ageUnknown;
  const seconds = Math.floor((now - Date.parse(value)) / 1000);
  if (seconds < 0) return c.future;
  if (seconds < 60) return `${number.format(seconds)} ${c.secondsAgo}`;
  if (seconds < 3600) return `${number.format(Math.floor(seconds / 60))} ${c.minutesAgo}`;
  return `${number.format(Math.floor(seconds / 3600))} ${c.hoursAgo}`;
}
function reading(snapshot: UsageSnapshotResponse | undefined, metric: string) {
  const observations = snapshot?.observations.filter((o) => o.metric_key === metric) ?? [];
  const state = snapshot?.availability.find((a) => a.metric_key === metric)?.state;
  const observation = observations.length === 1 ? observations[0] : undefined;
  const conflict =
    state === "contradictory" ||
    observations.length > 1 ||
    observations.some((o) => o.availability === "contradictory");
  return {
    observation,
    conflict,
    state: state ?? "temporarily_unavailable",
    value:
      !conflict &&
      observation &&
      observation.unit === "percent" &&
      observation.value >= 0 &&
      observation.value <= 100
        ? 100 - observation.value
        : null,
  };
}
function Icon({ index }: { index: number }) {
  const paths = [
    "M3 10 12 3l9 7v11h-6v-7H9v7H3Z",
    "M4 21v-3c0-5 16-5 16 0v3ZM12 3a4 4 0 1 0 0 8 4 4 0 1 0 0-8",
    "M12 3a9 9 0 1 0 0 18 9 9 0 1 0 0-18M12 7v6l4 2",
    "M4 21V11h3v10m4 0V6h3v15m4 0V2h3v19",
    "M5 18h14l-2-4V9a5 5 0 0 0-10 0v5ZM10 21h4",
    "M12 8a4 4 0 1 0 0 8 4 4 0 1 0 0-8M12 2v3m0 14v3M2 12h3m14 0h3M5 5l2 2m10 10 2 2M5 19l2-2M17 7l2-2",
  ];
  return (
    <svg
      className="size-6 shrink-0 fill-none stroke-current [stroke-width:1.7] [stroke-linecap:round] [stroke-linejoin:round] forced-colors:text-[CanvasText]"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d={paths[index]} />
    </svg>
  );
}
function Capacity({ snapshot, now }: { snapshot?: UsageSnapshotResponse; now: number }) {
  return (
    <>
      <div className="mt-6 grid grid-cols-2 gap-[0.85rem] md:gap-6">
        {metrics.map((metric, i) => {
          const r = reading(snapshot, metric);
          return (
            <section className="relative min-w-0 [&_p]:wrap-anywhere" key={metric}>
              <h3 className="mb-4 text-[1.05rem] font-bold">{i === 0 ? c.primary : c.secondary}</h3>
              {r.value !== null ? (
                <>
                  <svg
                    className="mx-auto mt-4 block h-auto w-full max-w-60 text-accent forced-colors:text-[CanvasText]"
                    viewBox="0 0 240 175"
                    aria-hidden="true"
                  >
                    <path
                      className="stroke-rule"
                      d="M37 153 A100 100 0 1 1 203 153"
                      fill="none"
                      strokeWidth="12"
                    />
                    <path
                      d="M37 153 A100 100 0 1 1 203 153"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="12"
                      pathLength="100"
                      strokeDasharray={`${r.value} 100`}
                    />
                  </svg>
                  <p className="max-w-[75ch] relative -mt-[3.6rem] mb-[1.2rem] text-center md:-mt-[5.6rem] md:mb-8 [&_strong]:block [&_strong]:text-[1.7rem] [&_strong]:font-semibold [&_strong]:leading-[1.1] [&_strong]:tracking-[-0.035em] md:[&_strong]:text-[2.4rem] max-md:[&_span]:text-sm">
                    <strong>{number.format(r.value)}%</strong>
                    <span>
                      {r.state !== "available" ||
                      (r.observation && now - Date.parse(r.observation.captured_at) > 600000)
                        ? c.lastKnown
                        : c.remaining}
                    </span>
                  </p>
                </>
              ) : (
                <p className="mb-4 max-w-[75ch] grid min-h-40 place-content-center text-[1.4rem]">
                  {r.conflict ? c.conflicting : c.noValue}
                </p>
              )}
              <p className="mb-4 max-w-[75ch] pt-3 text-sm md:text-[0.9rem]">
                {r.observation?.window_end
                  ? `${c.resets} ${instant(r.observation.window_end)} · ${zone}`
                  : c.unknownReset}
              </p>
              <p className="mb-4 max-w-[75ch] text-muted">
                {label(r.state)}
                {snapshot?.availability.find((a) => a.metric_key === metric)?.reason && (
                  <>
                    {" "}
                    · {label(snapshot.availability.find((a) => a.metric_key === metric)!.reason)}
                  </>
                )}
                {r.observation && (
                  <>
                    {" "}
                    · {provenance(r.observation.provenance)} · {age(r.observation.captured_at, now)}
                  </>
                )}
              </p>
            </section>
          );
        })}
      </div>
      <p className="mb-4 max-w-[75ch] text-muted">
        {c.credits}: {c.unsupportedCredits}
      </p>
    </>
  );
}
function Evidence({ snapshot, now }: { snapshot: UsageSnapshotResponse; now: number }) {
  return (
    <div className="grid gap-6 [&_section]:min-w-0">
      {snapshot.observations.map((o) => (
        <section key={o.observation_id}>
          <h3 className="mb-4 text-[1.05rem] font-bold">{o.metric_key}</h3>
          <dl className="grid grid-cols-[minmax(130px,0.65fr)_1fr] gap-x-5 gap-y-3">
            <dt className="text-muted">{c.value}</dt>
            <dd className="mb-4 wrap-anywhere">
              {number.format(o.value)} {o.unit} {c.used}
            </dd>
            <dt className="text-muted">{c.availability}</dt>
            <dd className="mb-4 wrap-anywhere">
              {label(o.availability)} ·{" "}
              {o.freshness === "stale" || now - Date.parse(o.captured_at) > 600000
                ? c.stale
                : label(o.freshness)}
            </dd>
            <dt className="text-muted">{c.provenance}</dt>
            <dd className="mb-4 wrap-anywhere">{provenance(o.provenance)}</dd>
            <dt className="text-muted">{c.source}</dt>
            <dd className="mb-4 wrap-anywhere">
              {o.source} {o.source_version}
            </dd>
            <dt className="text-muted">{c.captured}</dt>
            <dd className="mb-4 wrap-anywhere">
              {instant(o.captured_at)} · {zone} · {age(o.captured_at, now)}
            </dd>
            <dt className="text-muted">{c.window}</dt>
            <dd className="mb-4 wrap-anywhere">
              {instant(o.window_start)} — {instant(o.window_end)} · {o.window_timezone}
            </dd>
            {(o.assumptions || o.uncertainty) && (
              <>
                <dt className="text-muted">{c.assumptions}</dt>
                <dd className="mb-4 wrap-anywhere">
                  {o.assumptions} {o.uncertainty}
                </dd>
              </>
            )}
          </dl>
        </section>
      ))}
      {snapshot.availability.map((a) => (
        <p key={a.metric_key} className="mb-4 max-w-[75ch]">
          {a.metric_key}: {label(a.state)} {label(a.reason)}
        </p>
      ))}
    </div>
  );
}
function Trace({ snapshots, alias }: { snapshots: UsageSnapshotResponse[]; alias: string }) {
  const [index, setIndex] = useState(0);
  const samples = snapshots.filter((s) => s.alias === alias);
  if (!samples.length) return <p className="mb-4 max-w-[75ch]">{c.noHistory}</p>;
  const selected = samples[Math.min(index, samples.length - 1)];
  const firstTime = Date.parse(samples[0].captured_at);
  const span = Math.max(1, Date.parse(samples[samples.length - 1].captured_at) - firstTime);
  const xFor = (sample: UsageSnapshotResponse) =>
    20 + ((Date.parse(sample.captured_at) - firstTime) * 500) / span;
  const value = (s: UsageSnapshotResponse, metric: string) => {
    const r = reading(s, metric);
    return r.state === "available" ? r.value : null;
  };
  return (
    <section className="border-t border-rule py-6">
      <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
        {c.recent} · {alias}
      </h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.recentDetail}</p>
      <div className="grid grid-cols-1 gap-5 md:gap-8 lg:grid-cols-[1.2fr_1fr] [&_svg]:h-auto [&_svg]:w-full">
        <div>
          <svg viewBox="0 0 540 170" aria-hidden="true">
            <path d="M20 10V150H530" fill="none" stroke="var(--rule)" />
            {metrics.map((metric, m) => (
              <g
                key={metric}
                fill="none"
                stroke={m ? "var(--magenta)" : "var(--cyan)"}
                strokeWidth="3"
                strokeDasharray={m ? "7 5" : undefined}
              >
                {samples.map((s, i) => {
                  const v = value(s, metric);
                  if (v === null) return null;
                  const x = xFor(s),
                    y = 150 - v * 1.4;
                  const previous = i ? value(samples[i - 1], metric) : null;
                  const currentObservation = reading(s, metric).observation;
                  const previousObservation = i
                    ? reading(samples[i - 1], metric).observation
                    : undefined;
                  const compatible =
                    currentObservation?.source === previousObservation?.source &&
                    currentObservation?.source_version === previousObservation?.source_version &&
                    currentObservation?.provenance === previousObservation?.provenance &&
                    currentObservation?.window_start === previousObservation?.window_start &&
                    currentObservation?.window_end === previousObservation?.window_end;
                  return (
                    <g key={s.snapshot_id}>
                      <circle cx={x} cy={y} r="3" />
                      {previous !== null && compatible && (
                        <path d={`M${xFor(samples[i - 1])} ${150 - previous * 1.4}L${x} ${y}`} />
                      )}
                    </g>
                  );
                })}
              </g>
            ))}
          </svg>
          <p className="mb-4 max-w-[75ch] flex justify-between gap-4 text-sm text-muted">
            <span>
              {instant(samples[0].captured_at)} — {instant(samples[samples.length - 1].captured_at)}
            </span>
            <span>{zone}</span>
          </p>
          <label className="grid min-w-0 gap-[0.4rem]">
            {c.sample}
            <input
              type="range"
              min="0"
              max={samples.length - 1}
              value={Math.min(index, samples.length - 1)}
              onChange={(e) => setIndex(Number(e.target.value))}
              className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink w-full p-0 caret-accent accent-accent"
            />
          </label>
          <p role="status" className="mb-4 max-w-[75ch]">
            {instant(selected.captured_at)} ·{" "}
            {metrics
              .map(
                (m, i) =>
                  `${i ? c.secondary : c.primary}: ${value(selected, m) === null ? c.noValue : number.format(value(selected, m)!) + "%"} · ${label(reading(selected, m).state)} · ${provenance(reading(selected, m).observation?.provenance ?? selected.availability.find((a) => a.metric_key === m)?.provenance ?? "")}`,
              )
              .join(" · ")}
          </p>
        </div>
        <table className="w-full border-collapse tabular-nums">
          <caption className="pt-[0.6rem] pb-4 text-left text-[0.9rem] text-muted">
            {c.traceTable}
          </caption>
          <thead className="max-md:sr-only">
            <tr className="data-current:bg-panel data-current:outline data-current:outline-accent data-current:-outline-offset-1">
              <th
                scope="col"
                className="border-b border-rule px-1 py-2 text-left font-semibold wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]"
              >
                {c.captured}
              </th>
              <th
                scope="col"
                className="border-b border-rule px-1 py-2 text-left font-semibold wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]"
              >
                {c.primary}
              </th>
              <th
                scope="col"
                className="border-b border-rule px-1 py-2 text-left font-semibold wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]"
              >
                {c.secondary}
              </th>
            </tr>
          </thead>
          <tbody>
            {samples.map((s) => (
              <tr
                key={s.snapshot_id}
                data-current={s === selected || undefined}
                className="data-current:bg-panel data-current:outline data-current:outline-accent data-current:-outline-offset-1"
              >
                <th
                  scope="row"
                  className="max-md:hidden border-b border-rule px-1 py-2 text-left font-semibold wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]"
                >
                  {instant(s.captured_at)}
                  <small className="block text-sm text-muted">
                    {s.source} {s.source_version}
                  </small>
                  <details className="border-b border-rule py-[0.4rem]">
                    <summary className="min-h-11 cursor-pointer px-1 py-[0.8rem] hover:border-accent">
                      {c.window}
                    </summary>
                    {metrics.map((m) => {
                      const o = reading(s, m).observation;
                      return (
                        <p key={m} className="mb-4 max-w-[75ch]">
                          {m}:{" "}
                          {o?.window_start
                            ? `${instant(o.window_start)} — ${instant(o.window_end)} · ${zone} (${o.window_timezone})`
                            : c.unknownReset}
                        </p>
                      );
                    })}
                  </details>
                </th>
                <td colSpan={3} className="border-b border-rule py-2 md:hidden">
                  <details className="py-2">
                    <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                      {instant(s.captured_at)}
                    </summary>
                    <p className="mb-4 text-sm text-muted">
                      {s.source} {s.source_version} · {zone}
                    </p>
                    {metrics.map((m, i) => {
                      const r = reading(s, m);
                      return (
                        <section key={m} className="mb-4">
                          <h3 className="font-semibold">{i ? c.secondary : c.primary}</h3>
                          <p>
                            {value(s, m) === null
                              ? c.noValue
                              : `${number.format(value(s, m)!)}% ${c.remaining}`}
                          </p>
                          <p className="text-sm text-muted">
                            {label(r.state)} ·{" "}
                            {provenance(
                              r.observation?.provenance ??
                                s.availability.find((a) => a.metric_key === m)?.provenance ??
                                "",
                            )}
                          </p>
                          <p className="text-sm text-muted">
                            {r.observation?.window_start
                              ? `${instant(r.observation.window_start)} — ${instant(r.observation.window_end)} · ${r.observation.window_timezone}`
                              : c.unknownReset}
                          </p>
                        </section>
                      );
                    })}
                  </details>
                </td>
                {metrics.map((m) => (
                  <td
                    key={m}
                    className="max-md:hidden border-b border-rule px-1 py-2 text-left wrap-anywhere md:px-[0.65rem] md:py-[0.8rem]"
                  >
                    {value(s, m) === null ? c.noValue : `${number.format(value(s, m)!)}%`}
                    <small className="block text-sm text-muted">
                      {label(reading(s, m).state)} ·{" "}
                      {provenance(
                        reading(s, m).observation?.provenance ??
                          s.availability.find((a) => a.metric_key === m)?.provenance ??
                          "",
                      )}
                    </small>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export function App() {
  const [status, setStatus] = useState("authorizing");
  const [data, setData] = useState<AnalyticsResponse | null>(null);
  const [selection, setSelection] = useState<SelectionResponse | null>(null);
  const [profiles, setProfiles] = useState<ProfileSummary[]>([]);
  const [quarantined, setQuarantined] = useState<ProfileLifecycleRecord[]>([]);
  const [combined, setCombined] = useState(false);
  const [route, setRoute] = useState("Overview");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [warning, setWarning] = useState("");
  const [guidance, setGuidance] = useState("");
  const [details, setDetails] = useState(false);
  const [launch, setLaunch] = useState<{
    target: LaunchTarget;
    projects: ProjectIdentity[];
    projectId: string;
    baseline: string[];
    record?: ActivityRecord;
  } | null>(null);
  const [now, setNow] = useState(Date.now);
  const [theme, setTheme] = useState(() => {
    try {
      const v = localStorage.getItem("codex-folio.appearance.v1");
      return v === "light" || v === "dark" ? v : "system";
    } catch {
      return "system";
    }
  });
  const heading = useRef<HTMLHeadingElement>(null);
  const more = useRef<HTMLDetailsElement>(null);
  const csrf = useRef("");
  const startup = useRef<Promise<void> | null>(null);
  const operation = useRef(false);
  const failure = (error: unknown) => {
    if (error instanceof UsageRefreshError && (error.status === 401 || error.status === 403)) {
      setStatus("expired");
      setData(null);
    } else if (error instanceof TypeError) setStatus("unavailable");
    else setMessage(c.refreshFailed);
  };
  async function load(includeProfiles = false) {
    const [next, selected, inventory] = await Promise.all([
      api.getAnalytics("combined_identity"),
      api.getSelection().catch((e) => {
        if (e instanceof UsageRefreshError && e.status === 409) return null;
        throw e;
      }),
      includeProfiles ? Promise.all([api.getProfiles(), api.listProfileQuarantine()]) : null,
    ]);
    setNow(Date.now());
    setData(next);
    setSelection(selected);
    if (inventory) {
      setProfiles(inventory[0].profiles);
      setQuarantined(inventory[1].quarantined);
    }
    return next;
  }
  async function refresh(trigger: string, current: AnalyticsResponse) {
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    setMessage(c.refreshStart);
    try {
      const results = await Promise.allSettled(
        current.candidates
          .filter((p) => p.eligible)
          .map((p) =>
            api.refreshUsage(
              { alias: p.alias, trigger_reason: trigger },
              { headers: { "X-CodexFolio-CSRF": csrf.current } },
            ),
          ),
      );
      const rejected = results.filter((r) => r.status === "rejected");
      for (const r of rejected)
        if (r.reason instanceof UsageRefreshError && [401, 403].includes(r.reason.status))
          throw r.reason;
      await load();
      setMessage(rejected.length ? c.refreshFailed : c.refreshDone);
    } catch (e) {
      failure(e);
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  const start = useEffectEvent(() => {
    startup.current ??= (async () => {
      const url = new URL(window.location.href),
        token = url.searchParams.get("bootstrap");
      window.history.replaceState(null, "", window.location.pathname);
      if (!token) {
        setStatus("missing");
        return;
      }
      try {
        const auth = await api.exchangeBootstrap({ bootstrap_token: token });
        csrf.current = auth.csrf_token;
        const next = await load(true);
        setStatus("authorized");
        void refresh("dashboard_open", next);
      } catch (e) {
        failure(e);
        setStatus(
          e instanceof UsageRefreshError && [401, 403].includes(e.status)
            ? "expired"
            : "unavailable",
        );
      }
    })();
  });
  useEffect(() => {
    start();
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    if (
      !launch ||
      launch.record?.lifecycle === "exited" ||
      launch.record?.lifecycle === "abandoned"
    )
      return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const next = await api.getAnalytics("combined_identity");
        if (cancelled) return;
        setData(next);
        setLaunch((current) => {
          if (!current) return null;
          const record = current.record
            ? next.activity.find((item) => item.id === current.record?.id)
            : next.activity.find(
                (item) =>
                  item.record_type === "managed_launch" &&
                  item.profile_id === current.target.profile_id &&
                  item.project_id === current.projectId &&
                  !current.baseline.includes(item.id),
              );
          return record ? { ...current, record } : current;
        });
      } catch (error) {
        if (!cancelled) failure(error);
      } finally {
        if (!cancelled) timer = setTimeout(() => void poll(), 500);
      }
    };
    timer = setTimeout(() => void poll(), 500);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [launch]);
  async function choose(value: string) {
    if (operation.current || !data) return;
    setGuidance("");
    setDetails(false);
    if (value === "*") {
      setCombined(true);
      setMessage(c.combinedDetail);
      return;
    }
    operation.current = true;
    setBusy(true);
    try {
      const result = await api.setSelection(
        { alias: value },
        { headers: { "X-CodexFolio-CSRF": csrf.current } },
      );
      setSelection(result);
      setProfiles((current) =>
        current.map((profile) => ({ ...profile, selected: profile.alias === result.alias })),
      );
      setCombined(false);
      setWarning(result.warning);
      await load();
      setMessage(c.selectedDone);
    } catch (e) {
      failure(e);
      throw e;
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  async function editProfile(request: ProfileEditRequest) {
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    try {
      const result = await api.editProfile(request, {
        headers: { "X-CodexFolio-CSRF": csrf.current },
      });
      if (result.updated) {
        setProfiles((current) =>
          current.map((profile) =>
            profile.profile_id === result.updated?.profile_id ? result.updated : profile,
          ),
        );
      }
    } catch (error) {
      if (
        error instanceof TypeError ||
        (error instanceof UsageRefreshError && [401, 403].includes(error.status))
      ) {
        failure(error);
      }
      throw error;
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  async function authenticateProfile(request: ProfileAuthenticationRequest) {
    if (operation.current) throw new Error(c.refreshing);
    operation.current = true;
    setBusy(true);
    try {
      const result = await api.authenticateProfile(request, {
        headers: { "X-CodexFolio-CSRF": csrf.current },
      });
      setProfiles((current) => [
        ...current
          .filter((profile) => profile.profile_id !== result.profile.profile_id)
          .map((profile) => ({
            ...profile,
            selected: result.profile.selected ? false : profile.selected,
          })),
        result.profile,
      ]);
      return result;
    } catch (error) {
      if (
        error instanceof TypeError ||
        (error instanceof UsageRefreshError && [401, 403].includes(error.status))
      ) {
        failure(error);
      }
      if (!(error instanceof UsageRefreshError && [401, 403].includes(error.status))) {
        void api
          .getProfiles()
          .then((inventory) => setProfiles(inventory.profiles))
          .catch(failure);
      }
      throw error;
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  async function manageProfileLifecycle(request: ProfileLifecycleRequest) {
    if (operation.current) throw new Error(c.refreshing);
    operation.current = true;
    setBusy(true);
    try {
      const result = await api.manageProfileLifecycle(request, {
        headers: { "X-CodexFolio-CSRF": csrf.current },
      });
      if (request.action !== "preview") await load(true);
      return result;
    } catch (error) {
      if (
        error instanceof TypeError ||
        (error instanceof UsageRefreshError && [401, 403].includes(error.status))
      ) {
        failure(error);
      }
      throw error;
    } finally {
      operation.current = false;
      setBusy(false);
    }
  }
  async function openLaunch(target: LaunchTarget) {
    try {
      const [projectResult, next] = await Promise.all([
        api.getProjects(),
        api.getAnalytics("combined_identity"),
      ]);
      setData(next);
      setLaunch({
        target,
        projects: projectResult.projects,
        projectId: projectResult.projects[0]?.project_id ?? "",
        baseline: next.activity.map((item) => item.id),
      });
      requestAnimationFrame(() => heading.current?.focus());
    } catch (error) {
      failure(error);
    }
  }
  const selected = data?.candidates.find((p) => p.profile_id === selection?.profile_id);
  const scoped =
    data?.candidates.filter((p) => combined || p.profile_id === selection?.profile_id) ?? [];
  const visible =
    data?.profiles.filter((p) => combined || p.profile_id === selection?.profile_id) ?? [];
  const fresh = (id: string) => {
    const snapshot = data?.profiles.find((p) => p.profile_id === id);
    return (
      snapshot &&
      metrics.every((m) => {
        const o = reading(snapshot, m).observation;
        return (
          o &&
          now - Date.parse(o.captured_at) <= 600000 &&
          now >= Date.parse(o.captured_at) &&
          now >= Date.parse(o.window_start) &&
          now < Date.parse(o.window_end)
        );
      })
    );
  };
  const running =
    data?.activity.filter(
      (a) =>
        a.record_type === "managed_launch" &&
        ["running", "pending"].includes(a.lifecycle) &&
        a.profile_id !== selection?.profile_id,
    ) ?? [];
  const selector = (
    <label className="grid min-w-0 gap-[0.4rem]">
      {c.scope}
      <select
        aria-label={c.scope}
        disabled={busy || !data}
        value={combined ? "*" : (selection?.alias ?? "")}
        onChange={(e) => void choose(e.target.value).catch(() => undefined)}
        className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink w-full min-w-0 cursor-pointer"
      >
        {!selection && <option value="">{c.none}</option>}
        {data?.candidates.map((p) => (
          <option key={p.profile_id} value={p.alias}>
            {p.alias}
          </option>
        ))}
        <option value="*">{c.combined}</option>
      </select>
    </label>
  );
  function navigate(destination: string) {
    setLaunch(null);
    setRoute(destination);
    setGuidance("");
    if (more.current) more.current.open = false;
    requestAnimationFrame(() => heading.current?.focus());
  }
  const link = (destination: string, index: number, narrow = false) => (
    <button
      key={destination}
      aria-label={destination}
      aria-current={route === destination ? "page" : undefined}
      onClick={() => navigate(destination)}
      className={
        narrow
          ? "grid min-h-11 cursor-pointer justify-items-center rounded border-0 bg-transparent p-2 text-[0.85rem] aria-[current=page]:text-accent aria-[current=page]:underline forced-colors:aria-[current=page]:outline-2 forced-colors:aria-[current=page]:outline-[Highlight]"
          : "group relative flex min-h-[50px] cursor-pointer items-center gap-[0.85rem] rounded-none border-0 bg-transparent px-4 py-[0.6rem] text-left max-lg:justify-center aria-[current=page]:bg-panel aria-[current=page]:text-accent aria-[current=page]:shadow-[inset_3px_0_var(--cyan)] forced-colors:aria-[current=page]:outline-2 forced-colors:aria-[current=page]:outline-[Highlight]"
      }
    >
      <Icon index={index} />
      <span
        className={
          narrow
            ? ""
            : "max-lg:hidden max-lg:group-hover:absolute max-lg:group-hover:left-full max-lg:group-hover:z-30 max-lg:group-hover:block max-lg:group-hover:border max-lg:group-hover:border-rule max-lg:group-hover:bg-panel max-lg:group-hover:px-3 max-lg:group-hover:py-2 max-lg:group-hover:text-ink max-lg:group-focus-visible:absolute max-lg:group-focus-visible:left-full max-lg:group-focus-visible:z-30 max-lg:group-focus-visible:block max-lg:group-focus-visible:border max-lg:group-focus-visible:border-rule max-lg:group-focus-visible:bg-panel max-lg:group-focus-visible:px-3 max-lg:group-focus-visible:py-2 max-lg:group-focus-visible:text-ink"
        }
      >
        {destination}
      </span>
    </button>
  );
  return (
    <div className="min-h-screen bg-canvas font-sans text-base leading-normal text-ink scheme-light selection:bg-accent selection:text-canvas dark:scheme-dark [--bg:#f5f8f9] [--surface:#fff] [--text:#17252b] [--muted:#435861] [--rule:#a0afb5] [--cyan:#007584] [--lime:#466614] [--amber:#845300] [--magenta:#99326e] dark:[--bg:#101719] dark:[--surface:#151f22] dark:[--text:#edf3f3] dark:[--muted:#adc0c5] dark:[--rule:#586c73] dark:[--cyan:#50d6e6] dark:[--lime:#b6df73] dark:[--amber:#f1bb5c] dark:[--magenta:#ed91c7] forced-colors:[--bg:Canvas] forced-colors:[--surface:Canvas] forced-colors:[--text:CanvasText] forced-colors:[--muted:CanvasText] forced-colors:[--rule:CanvasText] forced-colors:[--cyan:LinkText] forced-colors:[--lime:CanvasText] forced-colors:[--amber:CanvasText] forced-colors:[--magenta:CanvasText] **:focus-visible:outline-3 **:focus-visible:outline-accent **:focus-visible:outline-offset-4 motion-reduce:**:scroll-auto motion-reduce:**:animate-none motion-reduce:**:transition-none contrast-more:[--rule:currentColor]">
      <a
        className="sr-only fixed top-4 left-4 z-50 bg-panel text-accent underline underline-offset-[0.2em] focus:not-sr-only focus:fixed focus:p-4"
        href="#content"
      >
        {c.skip}
      </a>
      <div className="min-h-screen md:grid md:grid-cols-[80px_minmax(0,1fr)] lg:grid-cols-[216px_minmax(0,1fr)]">
        <aside className="sticky top-0 hidden h-screen self-start border-r border-rule px-3 py-6 md:block lg:px-4 lg:py-8 max-lg:[&>label]:hidden max-lg:[&>p]:hidden">
          <p className="max-w-[75ch] mb-8 text-[1.55rem] font-[650]" title={c.product}>
            {c.brand}
          </p>
          {status === "authorized" && selector}
          <nav className="my-8 -mx-2 grid gap-2 lg:-mx-4" aria-label="Primary">
            {c.destinations.map((d, i) => link(d, i))}
          </nav>
          <p className="mb-4 max-w-[75ch] text-muted">
            {c.local}
            <br />v{__CODEXFOLIO_VERSION__}
          </p>
        </aside>
        <div className="min-w-0 px-4 pt-[1.1rem] pb-28 md:px-[clamp(1.25rem,3vw,3rem)] md:pt-8 md:pb-12">
          <div className="mb-6 block lg:hidden">
            {status === "authorized" && selector}
            <small className="block text-sm text-muted">
              {c.selected}: {selection?.alias ?? c.none}
            </small>
          </div>
          <main id="content">
            {status !== "authorized" ? (
              <>
                <header className="mb-7 border-b border-rule pb-5 [&_p]:mb-0">
                  <h1
                    ref={heading}
                    tabIndex={-1}
                    className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
                  >
                    {status === "authorizing" ? c.authorizing : c.relaunch}
                  </h1>
                </header>
                <p role="status" className="mb-4 max-w-[75ch]">
                  {status === "unavailable"
                    ? c.unavailable
                    : status === "missing"
                      ? c.missing
                      : status === "authorizing"
                        ? c.authorizing
                        : c.expired}
                </p>
                <code className="wrap-anywhere">{c.relaunchCommand}</code>
                <p className="mb-4 max-w-[75ch]">{c.authorizationDetail}</p>
              </>
            ) : launch ? (
              <Launch
                {...launch}
                selectedProfileId={selection?.profile_id}
                heading={heading}
                chooseProject={(projectId) =>
                  setLaunch((current) => (current ? { ...current, projectId } : null))
                }
                close={() => {
                  setLaunch(null);
                  requestAnimationFrame(() => heading.current?.focus());
                }}
              />
            ) : route === "Profiles" ? (
              <Profiles
                profiles={profiles}
                quarantined={quarantined}
                busy={busy}
                heading={heading}
                message={message}
                select={choose}
                edit={editProfile}
                authenticate={authenticateProfile}
                lifecycle={manageProfileLifecycle}
                launch={(profile) => void openLaunch(profile)}
                launchable={(profile) =>
                  Boolean(
                    data?.candidates.find((item) => item.profile_id === profile.profile_id)
                      ?.eligible,
                  )
                }
              />
            ) : (
              <>
                <header className="mb-7 border-b border-rule pb-5 [&_p]:mb-0">
                  <h1
                    ref={heading}
                    tabIndex={-1}
                    className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
                  >
                    {route !== "Overview"
                      ? route
                      : !selection && !combined
                        ? c.empty
                        : !combined &&
                            selected?.capacity_state === "fresh" &&
                            fresh(selected.profile_id) &&
                            visible.every((s) =>
                              metrics.every((m) => Number(reading(s, m).value) > 0),
                            )
                          ? c.title
                          : c.check}
                  </h1>
                  <p className="mb-4 max-w-[75ch] text-muted">
                    {combined ? c.combined : selection?.display_name || selection?.alias || c.none}{" "}
                    · {c.selected}: {selection?.alias ?? c.none}
                  </p>
                </header>
                <p role="status" className="mb-4 max-w-[75ch] min-h-[1.5em] text-muted">
                  {message}
                </p>
                {(warning || running.length > 0) && (
                  <section className="my-4 border-y border-rule py-4 text-warning">
                    <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                      {c.running}
                    </h2>
                    <p className="mb-4 max-w-[75ch]">
                      {warning || running.map((a) => a.profile_alias).join(", ")}
                    </p>
                    <p className="mb-4 max-w-[75ch]">{c.runningDetail}</p>
                  </section>
                )}
                {route === "Settings" ? (
                  <section>
                    <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                      {c.appearance}
                    </h2>
                    <label className="grid min-w-0 gap-[0.4rem]">
                      {c.appearance}
                      <select
                        value={theme}
                        onChange={(e) => {
                          setTheme(e.target.value);
                          try {
                            localStorage.setItem("codex-folio.appearance.v1", e.target.value);
                          } catch {
                            setMessage(c.themeUnavailable);
                          }
                        }}
                        className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink w-full min-w-0 cursor-pointer"
                      >
                        {c.themes.map((t) => (
                          <option key={t} value={t.toLowerCase()}>
                            {t}
                          </option>
                        ))}
                      </select>
                    </label>
                    <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                      {c.locale}
                    </h2>
                    <p className="mb-4 max-w-[75ch]">
                      {c.osLocale} · {navigator.language} · {zone}
                    </p>
                    <p className="mb-4 max-w-[75ch] text-muted">{c.later}</p>
                  </section>
                ) : route !== "Overview" ? (
                  <p className="mb-4 max-w-[75ch]">{c.later}</p>
                ) : (
                  <>
                    {!selection && !combined ? (
                      <p className="mb-4 max-w-[75ch]">{c.emptyDetail}</p>
                    ) : (
                      <>
                        <div className="grid grid-cols-1 gap-5 md:gap-8 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
                          <section>
                            <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                              {c.capacity}
                            </h2>
                            {combined && <p className="mb-4 max-w-[75ch]">{c.combinedDetail}</p>}
                            {scoped.map((p) => (
                              <section key={p.profile_id}>
                                {combined && (
                                  <h3 className="mb-4 text-[1.05rem] font-bold">{p.alias}</h3>
                                )}
                                <Capacity
                                  snapshot={visible.find((s) => s.profile_id === p.profile_id)}
                                  now={now}
                                />
                              </section>
                            ))}
                            <div className="my-5 flex flex-wrap items-end gap-3 max-md:[&_button]:grow max-md:[&_button]:justify-center">
                              <button
                                disabled={busy}
                                onClick={() => data && void refresh("dashboard_refresh", data)}
                                className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted"
                              >
                                {busy ? c.refreshing : c.refresh}
                              </button>
                              <button
                                className="min-h-11 max-w-full rounded border px-[0.8rem] py-[0.55rem] cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted border-accent bg-accent font-semibold text-canvas forced-colors:border-2 forced-colors:border-[ButtonText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText]"
                                disabled={busy || combined || !selected?.eligible || !selection}
                                onClick={() => selection && void openLaunch(selection)}
                              >
                                {c.launch}
                              </button>
                              <button
                                onClick={() => setGuidance("handoff")}
                                className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted"
                              >
                                {c.handoff}
                              </button>
                              <button
                                aria-expanded={details}
                                onClick={() => setDetails(!details)}
                                className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted"
                              >
                                {c.details}
                              </button>
                            </div>
                            {guidance && (
                              <section className="my-4 border-y border-rule py-4" role="status">
                                <h3 className="mb-4 text-[1.05rem] font-bold">{c.guidance}</h3>
                                <p className="mb-4 max-w-[75ch]">
                                  {combined ? c.combinedGuidance : c.handoffGuidance}
                                </p>
                                <button
                                  onClick={() => setGuidance("")}
                                  className="min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted"
                                >
                                  {c.close}
                                </button>
                              </section>
                            )}
                          </section>
                          <section>
                            <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                              {c.alternatives}
                            </h2>
                            {data?.candidates
                              .filter((p) => p.eligible && p.profile_id !== selection?.profile_id)
                              .map((p) => (
                                <section
                                  className="border-b border-rule py-[1.1rem] [&_h3]:flex [&_h3]:flex-wrap [&_h3]:justify-between [&_h3]:gap-2 [&_p]:mb-[0.45rem]"
                                  key={p.profile_id}
                                >
                                  <h3 className="mb-4 text-[1.05rem] font-bold">
                                    {p.alias}
                                    <small
                                      className={`block text-sm ${
                                        data.recommended_profile_id === p.profile_id &&
                                        fresh(p.profile_id)
                                          ? "text-positive"
                                          : "text-warning"
                                      }`}
                                    >
                                      {data.recommended_profile_id === p.profile_id &&
                                      fresh(p.profile_id)
                                        ? c.recommended
                                        : fresh(p.profile_id)
                                          ? label(p.capacity_state)
                                          : c.withheld}
                                    </small>
                                  </h3>
                                  <p className="mb-4 max-w-[75ch]">{c.eligible}</p>
                                  {metrics.map((m, i) => {
                                    const r = reading(
                                      data.profiles.find((s) => s.profile_id === p.profile_id),
                                      m,
                                    );
                                    return (
                                      <p key={m} className="mb-4 max-w-[75ch]">
                                        {i ? c.secondary : c.primary}:{" "}
                                        {r.value === null
                                          ? c.noValue
                                          : `${number.format(r.value)}% ${c.remaining}`}{" "}
                                        ·{" "}
                                        {r.observation
                                          ? `${provenance(r.observation.provenance)} · ${age(r.observation.captured_at, now)}`
                                          : label(r.state)}
                                      </p>
                                    );
                                  })}
                                </section>
                              ))}
                            {!data?.candidates.some(
                              (p) => p.eligible && p.profile_id !== selection?.profile_id,
                            ) && <p className="mb-4 max-w-[75ch]">{c.noAlternatives}</p>}
                            {data?.candidates
                              .filter((p) => !p.eligible)
                              .map((p) => (
                                <p className="mb-4 max-w-[75ch] text-warning" key={p.profile_id}>
                                  {p.alias}: {c.ineligible}
                                </p>
                              ))}
                          </section>
                        </div>
                        {details &&
                          visible.map((s) => (
                            <section className="border-t border-rule py-6" key={s.profile_id}>
                              <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
                                {c.details} · {s.alias}
                              </h2>
                              <Evidence snapshot={s} now={now} />
                            </section>
                          ))}
                        {scoped.map((p) => (
                          <Trace
                            key={p.profile_id}
                            alias={p.alias}
                            snapshots={data?.recent ?? []}
                          />
                        ))}
                      </>
                    )}
                  </>
                )}
              </>
            )}
          </main>
        </div>
      </div>
      <nav
        className="fixed bottom-0 z-30 flex w-full items-end justify-around gap-1 border-t border-rule bg-canvas px-1 py-2 md:hidden"
        aria-label="Primary narrow"
      >
        {[0, 1, 4].map((i) => link(c.destinations[i], i, true))}
        <details ref={more} className="border-b border-rule py-[0.4rem]">
          <summary className="min-h-11 cursor-pointer px-1 py-[0.8rem] hover:border-accent">
            {c.more}
          </summary>
          <div className="absolute right-2 bottom-full min-w-[170px] border border-rule bg-panel p-2 [&_button]:flex [&_button]:w-full [&_button]:gap-2">
            {[2, 3, 5].map((i) => link(c.destinations[i], i, true))}
          </div>
        </details>
      </nav>
    </div>
  );
}
