import { useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import type {
  AnalyticsExportResult,
  HistoryRequest,
  HistoryResponse,
  ProjectIdentity,
  PurgeResult,
  UsageCandidate,
} from "./generated/openapi";
import { analyticsDataCopy as c } from "./copy";
import { encodeAnalyticsExport } from "./analyticsExport";

export type AnalyticsDataView = "export" | "retention" | "purge";
export type HistoryManager = (request: HistoryRequest) => Promise<HistoryResponse>;

const button =
  "min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink cursor-pointer hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const primaryButton = `${button} border-accent bg-accent font-semibold text-canvas forced-colors:border-2 forced-colors:border-[ButtonText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText]`;
const field =
  "min-h-11 max-w-full rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink w-full min-w-0";
const cell = "border-b border-rule px-1 py-3 text-left align-top wrap-anywhere md:px-[0.65rem]";
const datasets = ["usage", "availability", "aggregates", "activity"] as const;
const purgeClasses = [
  ["usage", c.usage],
  ["aggregates", c.aggregates],
  ["observed_sessions", c.observedSessions],
  ["managed_launches", c.managedLaunches],
  ["checkpoints", c.checkpoints],
] as const;
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const number = new Intl.NumberFormat();

function displayBoundary(value: string) {
  return value === "all" ? c.allHistory : date.format(new Date(value));
}

interface AnalyticsDataProps {
  view: AnalyticsDataView;
  heading: RefObject<HTMLHeadingElement | null>;
  candidates: UsageCandidate[];
  projects: ProjectIdentity[];
  profileId: string;
  projectId: string;
  from: string;
  manageHistory: HistoryManager;
  expired: (error: unknown) => void;
  dataChanged: () => void;
  close: () => void;
}

function Heading({
  heading,
  title,
  subtitle,
}: {
  heading: RefObject<HTMLHeadingElement | null>;
  title: string;
  subtitle: string;
}) {
  return (
    <header className="mb-7 border-b border-rule pb-5">
      <h1
        ref={heading}
        tabIndex={-1}
        className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
      >
        {title}
      </h1>
      <p className="mb-0 max-w-[75ch] text-muted">{subtitle}</p>
    </header>
  );
}

function Controls({ children }: { children: ReactNode }) {
  return (
    <div className="mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-3 [&_label]:grid [&_label]:min-w-0 [&_label]:gap-2">
      {children}
    </div>
  );
}

function ExportView(props: AnalyticsDataProps) {
  const [format, setFormat] = useState<"json" | "csv">("json");
  const [selected, setSelected] = useState<string[]>(["usage"]);
  const [includePaths, setIncludePaths] = useState(false);
  const [preview, setPreview] = useState<AnalyticsExportResult | null>(null);
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const invalidate = () => {
    setPreview(null);
    setStatus("");
  };
  const toggleDataset = (dataset: string, checked: boolean) => {
    invalidate();
    setSelected((current) =>
      format === "csv"
        ? checked
          ? [dataset]
          : []
        : checked
          ? [...current, dataset]
          : current.filter((item) => item !== dataset),
    );
  };
  async function loadPreview() {
    setBusy(true);
    setStatus(c.exportLoading);
    try {
      const result = await props.manageHistory({
        action: "export",
        export: {
          format,
          datasets: selected,
          scope: "selected_profile",
          profile_id: props.profileId,
          project_id: props.projectId || "*",
          from: props.from,
          to: "all",
          include_paths: includePaths,
        },
      });
      setPreview(result.export ?? null);
      setStatus(result.export ? c.exportReady : c.exportFailed);
    } catch (error) {
      setPreview(null);
      setStatus(c.exportFailed);
      props.expired(error);
    } finally {
      setBusy(false);
    }
  }
  function download() {
    if (!preview) return;
    const encoded = encodeAnalyticsExport(preview);
    const url = URL.createObjectURL(new Blob([encoded.contents], { type: encoded.mediaType }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `codex-folio-analytics.${format}`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
    setStatus(c.downloadStarted);
  }
  return (
    <>
      <Heading heading={props.heading} title={c.exportTitle} subtitle={c.exportSubtitle} />
      <p className="mb-5 max-w-[75ch]">{c.separation}</p>
      <Controls>
        <label>
          {c.format}
          <select
            className={field}
            value={format}
            onChange={(event) => {
              const next = event.target.value as "json" | "csv";
              setFormat(next);
              setSelected((current) => (next === "csv" ? current.slice(0, 1) : current));
              invalidate();
            }}
          >
            <option value="json">JSON</option>
            <option value="csv">CSV</option>
          </select>
        </label>
      </Controls>
      <fieldset className="mb-6 border-y border-rule py-5">
        <legend className="font-semibold">{c.datasets}</legend>
        <div className="mt-3 flex flex-wrap gap-x-6 gap-y-3">
          {datasets.map((dataset) => (
            <label key={dataset} className="flex min-h-11 items-center gap-3">
              <input
                type={format === "csv" ? "radio" : "checkbox"}
                name={format === "csv" ? "export-dataset" : undefined}
                checked={selected.includes(dataset)}
                onChange={(event) => toggleDataset(dataset, event.target.checked)}
              />
              {c.datasetLabels[dataset]}
            </label>
          ))}
        </div>
      </fieldset>
      <label className="mb-5 flex min-h-11 items-center gap-3">
        <input
          type="checkbox"
          checked={includePaths}
          onChange={(event) => {
            setIncludePaths(event.target.checked);
            invalidate();
          }}
        />
        {c.includePaths}
      </label>
      <p className="mb-5 max-w-[75ch] text-muted">{c.pathBoundary}</p>
      <p className="mb-5 max-w-[75ch] text-muted">{c.exportScope(displayBoundary(props.from))}</p>
      <p role="status" aria-live="polite" className="mb-5 min-h-6 max-w-[75ch]">
        {status}
      </p>
      {preview ? (
        <>
          <table
            className="mb-6 hidden w-full border-collapse table-fixed md:table"
            aria-label={c.previewCaption}
          >
            <caption className="pb-3 text-left text-muted">{c.previewCaption}</caption>
            <thead>
              <tr>
                <th scope="col" className={`${cell} w-1/5 font-semibold`}>
                  {c.dataset}
                </th>
                <th scope="col" className={`${cell} font-semibold`}>
                  {c.fields}
                </th>
                <th scope="col" className={`${cell} w-24 font-semibold`}>
                  {c.records}
                </th>
              </tr>
            </thead>
            <tbody>
              {preview.preview.map((item) => (
                <tr key={item.dataset}>
                  <th scope="row" className={`${cell} font-semibold`}>
                    {c.datasetLabels[item.dataset as keyof typeof c.datasetLabels] ?? item.dataset}
                  </th>
                  <td className={cell}>{item.fields.join(", ")}</td>
                  <td className={`${cell} tabular-nums`}>{number.format(item.record_count)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <section className="mb-6 border-t border-rule md:hidden" aria-label={c.previewCaption}>
            <h2 className="py-3 text-muted">{c.previewCaption}</h2>
            {preview.preview.map((item) => (
              <details key={item.dataset} className="border-b border-rule py-2">
                <summary className="min-h-11 cursor-pointer py-3 font-semibold">
                  {c.previewSummary(
                    c.datasetLabels[item.dataset as keyof typeof c.datasetLabels] ?? item.dataset,
                    item.record_count,
                  )}
                </summary>
                <dl className="grid gap-2 pb-3">
                  <dt className="text-muted">{c.fields}</dt>
                  <dd className="wrap-anywhere">{item.fields.join(", ")}</dd>
                </dl>
              </details>
            ))}
          </section>
        </>
      ) : null}
      <p className="mb-6 max-w-[75ch] text-muted">{c.excluded}</p>
      <div className="flex flex-wrap gap-3">
        <button
          className={primaryButton}
          disabled={busy || selected.length === 0}
          onClick={() => void loadPreview()}
        >
          {c.previewExport}
        </button>
        {preview ? (
          <button className={button} onClick={download}>
            {c.download(format)}
          </button>
        ) : null}
        <button className={button} onClick={props.close}>
          {c.cancelExport}
        </button>
      </div>
    </>
  );
}

function retentionLabel(setting: string) {
  if (setting === "13-months") return c.thirteenMonths;
  if (setting === "unlimited") return c.unlimited;
  return c.days(setting);
}

function RetentionView(props: AnalyticsDataProps) {
  const { expired, manageHistory } = props;
  const [choice, setChoice] = useState("13-months");
  const [days, setDays] = useState("30");
  const [current, setCurrent] = useState("");
  const [run, setRun] = useState(false);
  const [status, setStatus] = useState<string>(c.retentionLoading);
  const [busy, setBusy] = useState(false);
  const initialLoad = useRef({ expired, manageHistory });
  const validCustomDays =
    choice !== "custom" || (/^\d+$/.test(days) && Number(days) >= 30 && Number(days) <= 3652059);
  useEffect(() => {
    let cancelled = false;
    initialLoad.current
      .manageHistory({ action: "retention" })
      .then((result) => {
        if (cancelled || !result.retention) return;
        const setting = result.retention.setting;
        setCurrent(setting);
        if (["13-months", "30", "90", "365", "unlimited"].includes(setting)) setChoice(setting);
        else {
          setChoice("custom");
          setDays(setting);
        }
        setStatus("");
      })
      .catch((error) => {
        if (!cancelled) {
          setStatus(c.retentionFailed);
          initialLoad.current.expired(error);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);
  async function save() {
    if (!validCustomDays) return;
    const setting = choice === "custom" ? days : choice;
    setBusy(true);
    setStatus(c.retentionSaving);
    try {
      const result = await props.manageHistory({ action: "retention", setting, run });
      if (!result.retention) throw new Error("retention result unavailable");
      setCurrent(result.retention.setting);
      setStatus(
        run
          ? c.retentionMaintained(result.retention.processed, result.retention.more)
          : c.retentionSaved,
      );
    } catch (error) {
      setStatus(c.retentionFailed);
      props.expired(error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Heading heading={props.heading} title={c.retentionTitle} subtitle={c.retentionSubtitle} />
      <p className="mb-5">
        {c.currentSetting} <strong>{current ? retentionLabel(current) : c.loading}</strong>
      </p>
      <Controls>
        <label>
          {c.retentionSetting}
          <select
            className={field}
            value={choice}
            onChange={(event) => setChoice(event.target.value)}
          >
            <option value="13-months">{c.thirteenMonths}</option>
            <option value="30">{c.days("30")}</option>
            <option value="90">{c.days("90")}</option>
            <option value="365">{c.days("365")}</option>
            <option value="unlimited">{c.unlimited}</option>
            <option value="custom">{c.customDays}</option>
          </select>
        </label>
        {choice === "custom" ? (
          <label>
            {c.retentionDays}
            <input
              className={field}
              type="number"
              min="30"
              max="3652059"
              value={days}
              onChange={(event) => setDays(event.target.value)}
            />
          </label>
        ) : null}
      </Controls>
      <label className="mb-5 flex min-h-11 items-center gap-3">
        <input type="checkbox" checked={run} onChange={(event) => setRun(event.target.checked)} />
        {c.runMaintenance}
      </label>
      <p className="mb-5 max-w-[75ch] text-muted">{c.retentionBoundary}</p>
      <p role="status" aria-live="polite" className="mb-5 min-h-6">
        {status}
      </p>
      <div className="flex flex-wrap gap-3">
        <button
          className={primaryButton}
          disabled={busy || !validCustomDays}
          onClick={() => void save()}
        >
          {c.saveRetention}
        </button>
        <button className={button} onClick={props.close}>
          {c.back}
        </button>
      </div>
    </>
  );
}

function PurgeView(props: AnalyticsDataProps) {
  const [profile, setProfile] = useState(props.profileId);
  const [project, setProject] = useState(props.projectId || "*");
  const [fromMode, setFromMode] = useState<"all" | "date">("all");
  const [toMode, setToMode] = useState<"all" | "date">("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [classes, setClasses] = useState<string[]>([]);
  const [preview, setPreview] = useState<PurgeResult | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const invalidate = () => {
    setPreview(null);
    setConfirmation("");
    setStatus("");
  };
  const bound = (mode: "all" | "date", value: string) =>
    mode === "all" ? "all" : new Date(value).toISOString();
  const request = (token?: string): HistoryRequest => ({
    action: "purge",
    scope: {
      profile_id: profile,
      project_id: project,
      from: bound(fromMode, from),
      to: bound(toMode, to),
      classes,
    },
    ...(token ? { confirmation: token } : {}),
  });
  async function previewPurge() {
    setBusy(true);
    setStatus(c.purgeLoading);
    try {
      const result = await props.manageHistory(request());
      setPreview(result.purge ?? null);
      setStatus(result.purge ? c.purgeReady : c.purgeFailed);
    } catch (error) {
      setPreview(null);
      setStatus(c.purgeFailed);
      props.expired(error);
    } finally {
      setBusy(false);
    }
  }
  async function applyPurge() {
    if (!preview || confirmation !== preview.confirmation) return;
    setBusy(true);
    setStatus(c.purgeApplying);
    try {
      const result = await props.manageHistory(request(confirmation));
      if (!result.purge?.applied) throw new Error("purge not applied");
      setPreview(result.purge);
      setConfirmation("");
      setStatus(c.purgeApplied);
      props.dataChanged();
    } catch (error) {
      setPreview(null);
      setConfirmation("");
      setStatus(c.purgeStale);
      props.expired(error);
    } finally {
      setBusy(false);
    }
  }
  const validDates = (fromMode === "all" || from) && (toMode === "all" || to);
  return (
    <>
      <Heading heading={props.heading} title={c.purgeTitle} subtitle={c.purgeSubtitle} />
      <p className="mb-5 max-w-[75ch]">{c.purgeIsolation}</p>
      <Controls>
        <label>
          {c.purgeProfile}
          <select
            className={field}
            value={profile}
            disabled={classes.includes("checkpoints")}
            onChange={(event) => {
              setProfile(event.target.value);
              invalidate();
            }}
          >
            <option value="*">{c.allProfiles}</option>
            {props.candidates.map((item) => (
              <option key={item.profile_id} value={item.profile_id}>
                {item.alias}
              </option>
            ))}
          </select>
        </label>
        <label>
          {c.purgeProject}
          <select
            className={field}
            value={project}
            onChange={(event) => {
              setProject(event.target.value);
              invalidate();
            }}
          >
            <option value="*">{c.allProjects}</option>
            <option value="none">{c.noProject}</option>
            {props.projects.map((item) => (
              <option key={item.project_id} value={item.project_id}>
                {item.alias || item.basename}
              </option>
            ))}
          </select>
        </label>
        <label>
          {c.from}
          <select
            className={field}
            value={fromMode}
            onChange={(event) => {
              setFromMode(event.target.value as "all" | "date");
              invalidate();
            }}
          >
            <option value="all">{c.allHistory}</option>
            <option value="date">{c.chooseDate}</option>
          </select>
        </label>
        {fromMode === "date" ? (
          <label>
            {c.fromDate}
            <input
              className={field}
              type="datetime-local"
              value={from}
              onChange={(event) => {
                setFrom(event.target.value);
                invalidate();
              }}
            />
          </label>
        ) : null}
        <label>
          {c.to}
          <select
            className={field}
            value={toMode}
            onChange={(event) => {
              setToMode(event.target.value as "all" | "date");
              invalidate();
            }}
          >
            <option value="all">{c.allHistory}</option>
            <option value="date">{c.chooseDate}</option>
          </select>
        </label>
        {toMode === "date" ? (
          <label>
            {c.toDate}
            <input
              className={field}
              type="datetime-local"
              value={to}
              onChange={(event) => {
                setTo(event.target.value);
                invalidate();
              }}
            />
          </label>
        ) : null}
      </Controls>
      <fieldset className="mb-6 border-y border-rule py-5">
        <legend className="font-semibold">{c.recordClasses}</legend>
        <div className="mt-3 flex flex-wrap gap-x-6 gap-y-3">
          {purgeClasses.map(([value, label]) => (
            <label key={value} className="flex min-h-11 items-center gap-3">
              <input
                type="checkbox"
                checked={classes.includes(value)}
                onChange={(event) => {
                  const checked = event.target.checked;
                  setClasses((current) =>
                    checked ? [...current, value] : current.filter((item) => item !== value),
                  );
                  if (checked && value === "checkpoints") setProfile("*");
                  invalidate();
                }}
              />
              {label}
            </label>
          ))}
        </div>
      </fieldset>
      <p className="mb-5 max-w-[75ch] text-muted">{c.purgeDates}</p>
      <p role="status" aria-live="polite" className="mb-5 min-h-6">
        {status}
      </p>
      {preview ? (
        <>
          <table className="mb-5 w-full border-collapse" aria-label={c.purgeCaption}>
            <caption className="pb-3 text-left text-muted">{c.purgeCaption}</caption>
            <thead>
              <tr>
                <th scope="col" className={`${cell} font-semibold`}>
                  {c.recordClass}
                </th>
                <th scope="col" className={`${cell} font-semibold`}>
                  {c.records}
                </th>
              </tr>
            </thead>
            <tbody>
              {preview.counts.map((item) => (
                <tr key={item.record_class}>
                  <th scope="row" className={cell}>
                    {item.record_class}
                  </th>
                  <td className={`${cell} tabular-nums`}>{number.format(item.count)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="mb-5 max-w-[75ch]">
            {c.purgeLimit(preview.record_limit, preview.executable)}
          </p>
          {!preview.applied ? (
            <div className="mb-5 grid max-w-[42rem] gap-2">
              <label htmlFor="analytics-purge-confirmation">{c.confirmation}</label>
              <code id="analytics-purge-token" className="wrap-anywhere text-sm">
                {preview.confirmation}
              </code>
              <input
                id="analytics-purge-confirmation"
                aria-describedby="analytics-purge-token"
                className={field}
                value={confirmation}
                autoComplete="off"
                onChange={(event) => setConfirmation(event.target.value)}
              />
            </div>
          ) : null}
        </>
      ) : null}
      <div className="flex flex-wrap gap-3">
        <button
          className={primaryButton}
          disabled={busy || classes.length === 0 || !validDates}
          onClick={() => void previewPurge()}
        >
          {c.previewPurge}
        </button>
        {preview && !preview.applied ? (
          <button
            className={button}
            disabled={busy || !preview.executable || confirmation !== preview.confirmation}
            onClick={() => void applyPurge()}
          >
            {c.applyPurge}
          </button>
        ) : null}
        <button className={button} onClick={props.close}>
          {c.back}
        </button>
      </div>
    </>
  );
}

export function AnalyticsData(props: AnalyticsDataProps) {
  const { heading, view } = props;
  useEffect(() => {
    requestAnimationFrame(() => heading.current?.focus());
  }, [heading, view]);
  if (props.view === "export") return <ExportView {...props} />;
  if (props.view === "retention") return <RetentionView {...props} />;
  return <PurgeView {...props} />;
}
