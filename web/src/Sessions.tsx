import { useEffect, useEffectEvent, useRef, useState, type RefObject } from "react";
import {
  UsageRefreshError,
  type ActivityRecord,
  type ActivityResponse,
  type ActivitySource,
  type ActivitySourceImportResponse,
  type ActivitySourcesResponse,
} from "./generated/openapi";
import { sessionsCopy as c, sessionStateCopy, provenanceCopy } from "./copy";

export type SessionFilters = {
  profile: string;
  project: string;
  type: string;
  dates: string;
  from: string;
  to: string;
};
type Props = {
  filters: SessionFilters;
  setFilters: (value: SessionFilters) => void;
  read: () => Promise<ActivityResponse>;
  reviewSources: () => Promise<ActivitySourcesResponse>;
  importSource: (sourceId: string) => Promise<ActivitySourceImportResponse>;
  expired: (error: unknown) => void;
  heading: RefObject<HTMLHeadingElement | null>;
};
const button =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const input =
  "min-h-11 w-full min-w-0 rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink";
const cell = "border-b border-rule px-2 py-4 text-left align-top wrap-anywhere";
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const number = new Intl.NumberFormat();
const zone = date.resolvedOptions().timeZone;
const label = (value: string) => (sessionStateCopy[value] ?? value) || c.unavailable;
const instant = (value: string) =>
  value && Number.isFinite(Date.parse(value)) ? date.format(new Date(value)) : c.unavailable;
const key = (record: ActivityRecord) => `${record.record_type}:${record.id}`;
const managed = (record: ActivityRecord) => record.record_type === "managed_launch";
const ownership = (record: ActivityRecord) =>
  record.profile_id ? record.profile_alias : c.unassigned;
const projectName = (record: ActivityRecord) =>
  record.project_alias || record.project_basename || c.unavailable;
const provenance = (record: ActivityRecord) =>
  managed(record) ? c.locallyDerived : (provenanceCopy[record.provenance] ?? record.provenance);
const hasRelationship = (record: ActivityRecord) =>
  record.correlation_state === "correlated" &&
  record.correlation_evidence_type === "explicit" &&
  record.correlation_confidence === "high";

function Facts({ record }: { record: ActivityRecord }) {
  const pairs = [
    [c.type, label(record.record_type)],
    [managed(record) ? c.launchProfile : c.profile, ownership(record)],
    [c.project, projectName(record)],
    [c.basename, record.project_basename || c.unavailable],
    [managed(record) ? c.launchRecorded : c.observedStarted, instant(record.started_at)],
    [
      managed(record) ? (record.lifecycle === "abandoned" ? c.abandonedAt : c.ended) : c.lastSeen,
      managed(record) && !["exited", "abandoned"].includes(record.lifecycle)
        ? c.noEnd
        : instant(record.last_observed_at),
    ],
    [c.state, managed(record) ? label(record.lifecycle) : c.lastSeen],
    ...(managed(record)
      ? [[c.exit, record.exit_status === "" ? c.unavailable : record.exit_status]]
      : []),
  ];
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      {pairs.map(([name, value]) => (
        <div className="contents" key={name}>
          <dt className="text-muted">{name}</dt>
          <dd className="mb-3 wrap-anywhere">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Evidence({ record }: { record: ActivityRecord }) {
  const pairs = [
    [
      c.availability,
      !managed(record) && (!record.model || record.tokens_used === "" || !record.project_id)
        ? c.partial
        : c.available,
    ],
    [c.freshness, c.recorded],
    [c.source, label(record.source)],
    [c.version, record.source_version || c.unavailable],
    [c.provenance, provenance(record)],
    ...(!managed(record)
      ? [
          [
            c.attribution,
            c.attributionState[record.attribution_provenance as keyof typeof c.attributionState] ??
              c.unavailable,
          ],
        ]
      : []),
    [c.correlation, label(record.correlation_state)],
    [c.confidence, label(record.correlation_confidence)],
    [c.evidenceType, label(record.correlation_evidence_type)],
    ...(!managed(record)
      ? [
          [c.model, record.model || c.unavailable],
          [
            c.tokens,
            record.tokens_used === ""
              ? c.unavailable
              : `${number.format(BigInt(record.tokens_used))} · ${c.locallyDerived}`,
          ],
        ]
      : []),
    [c.id, record.id],
    ...(!managed(record) ? [[c.sourceId, record.source_session_id || c.unavailable]] : []),
  ];
  return (
    <>
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        {pairs.map(([name, value]) => (
          <div className="contents" key={name}>
            <dt className="text-muted">{name}</dt>
            <dd className="mb-3 wrap-anywhere">{value}</dd>
          </div>
        ))}
      </dl>
      {managed(record) && <p className="text-muted">{c.noMetrics}</p>}
    </>
  );
}

export function Sessions({
  filters,
  setFilters,
  read,
  reviewSources,
  importSource,
  expired,
  heading,
}: Props) {
  const [records, setRecords] = useState<ActivityRecord[]>([]);
  const [loadedAt, setLoadedAt] = useState(0);
  const [busy, setBusy] = useState(true);
  const [failed, setFailed] = useState(false);
  const [selectedKey, setSelectedKey] = useState("");
  const [page, setPage] = useState(0);
  const root = useRef<HTMLDivElement | null>(null);
  const fetchRecords = useEffectEvent(() => read());
  const reportExpired = useEffectEvent((error: unknown) => expired(error));
  const [reload, setReload] = useState(0);
  const [sources, setSources] = useState<ActivitySource[]>([]);
  const [sourcesBusy, setSourcesBusy] = useState(false);
  const [sourcesError, setSourcesError] = useState(false);
  const [reviewed, setReviewed] = useState(false);
  const [consentedSource, setConsentedSource] = useState("");
  const [importing, setImporting] = useState("");
  const [importResult, setImportResult] = useState("");
  async function review() {
    setSourcesBusy(true);
    setSourcesError(false);
    setImportResult("");
    try {
      const result = await reviewSources();
      setSources(result.sources);
      setReviewed(true);
      setConsentedSource("");
    } catch (error) {
      if (error instanceof UsageRefreshError && [401, 403].includes(error.status)) expired(error);
      else setSourcesError(true);
    } finally {
      setSourcesBusy(false);
    }
  }
  async function importReviewed(source: ActivitySource) {
    if (source.status !== "supported" || consentedSource !== source.source_id || importing) return;
    setImporting(source.source_id);
    setImportResult("");
    try {
      const result = await importSource(source.source_id);
      setImportResult(
        c.importResult
          .replace("{count}", number.format(result.imported_count))
          .replace("{existing}", number.format(result.already_present_count)),
      );
      setConsentedSource("");
      setBusy(true);
      setFailed(false);
      setReload((value) => value + 1);
    } catch (error) {
      if (error instanceof UsageRefreshError && [401, 403].includes(error.status)) expired(error);
      else setImportResult(c.importFailed);
    } finally {
      setImporting("");
    }
  }
  useEffect(() => {
    let cancelled = false;
    void fetchRecords()
      .then((result) => {
        if (cancelled) return;
        setRecords(result.records);
        setLoadedAt(Date.now());
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        if (error instanceof UsageRefreshError && [401, 403].includes(error.status))
          reportExpired(error);
        else setFailed(true);
      })
      .finally(() => {
        if (!cancelled) setBusy(false);
      });
    return () => {
      cancelled = true;
    };
  }, [reload]);
  function change(field: keyof SessionFilters, value: string) {
    setFilters({ ...filters, [field]: value });
    setPage(0);
  }
  const invalidDates =
    filters.dates === "custom" &&
    filters.from !== "" &&
    filters.to !== "" &&
    filters.from > filters.to;
  const lower =
    filters.dates === "custom"
      ? filters.from
        ? new Date(`${filters.from}T00:00:00`).getTime()
        : -Infinity
      : filters.dates === "all"
        ? -Infinity
        : loadedAt - Number(filters.dates) * 86400000;
  const end = filters.to ? new Date(`${filters.to}T00:00:00`) : null;
  if (end) end.setDate(end.getDate() + 1);
  const upper = filters.dates === "custom" ? (end?.getTime() ?? Infinity) : Infinity;
  const filtered = records.filter(
    (record) =>
      !invalidDates &&
      (!filters.profile ||
        (filters.profile === "__unassigned__"
          ? !record.profile_id
          : record.profile_id === filters.profile)) &&
      (!filters.project || record.project_id === filters.project) &&
      (!filters.type || record.record_type === filters.type) &&
      Date.parse(record.started_at) >= lower &&
      Date.parse(record.started_at) < upper,
  );
  const pages = Math.max(1, Math.ceil(filtered.length / 25));
  const currentPage = Math.min(page, pages - 1);
  const visible = filtered.slice(currentPage * 25, (currentPage + 1) * 25);
  const selected = records.find((record) => key(record) === selectedKey);
  const profiles = [
    ...new Map(
      records
        .filter((record) => record.profile_id)
        .map((record) => [record.profile_id, record.profile_alias]),
    ).entries(),
  ];
  const projects = [
    ...new Map(
      records
        .filter((record) => record.project_id)
        .map((record) => [record.project_id, projectName(record)]),
    ).entries(),
  ];
  const related = selected
    ? records.filter(
        (record) =>
          hasRelationship(record) &&
          hasRelationship(selected) &&
          (managed(selected)
            ? record.record_type === "observed_session" &&
              record.correlation_managed_launch_id === selected.id
            : record.record_type === "managed_launch" &&
              record.id === selected.correlation_managed_launch_id),
      )
    : [];
  function open(record: ActivityRecord) {
    setSelectedKey(key(record));
    requestAnimationFrame(() => heading.current?.focus());
  }
  function back() {
    setSelectedKey("");
    requestAnimationFrame(() => {
      const target = [
        ...(root.current?.querySelectorAll<HTMLButtonElement>("button[data-session-key]") ?? []),
      ].find(
        (element) => element.dataset.sessionKey === selectedKey && element.getClientRects().length,
      );
      (target ?? heading.current)?.focus();
    });
  }
  return (
    <div ref={root}>
      <header className="mb-7 border-b border-rule pb-5">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {selected ? `${label(selected.record_type)} · ${ownership(selected)}` : c.title}
        </h1>
        <p className="text-muted">{selected ? c.metadata : c.subtitle}</p>
      </header>
      {selected ? (
        <>
          <button className={`${button} mb-6`} onClick={back}>
            {c.back}
          </button>
          <div className="grid min-w-0 gap-8 lg:grid-cols-2">
            <section className="min-w-0">
              <h2 className="mb-5 text-[1.4rem] font-bold">
                {managed(selected) ? c.launchDetails : c.observedDetails}
              </h2>
              <Facts record={selected} />
              <p className="text-muted">
                {managed(selected) ? c.managedBoundary : c.observedBoundary}
              </p>
            </section>
            <section className="min-w-0">
              <h2 className="mb-5 text-[1.4rem] font-bold">{c.evidence}</h2>
              <Evidence record={selected} />
            </section>
          </div>
          <section className="mt-6 border-t border-rule py-6">
            <h2 className="mb-4 text-[1.4rem] font-bold">{c.related}</h2>
            <p className="mb-4 text-muted">{c.correlationNote}</p>
            {related.length ? (
              related.map((record) => (
                <section key={key(record)} className="border-b border-rule py-4">
                  <h3 className="mb-4 font-bold">
                    {label(record.record_type)} · {ownership(record)}
                  </h3>
                  <Facts record={record} />
                  <Evidence record={record} />
                </section>
              ))
            ) : (
              <p>{hasRelationship(selected) ? c.missingRelated : c.noRelated}</p>
            )}
          </section>
        </>
      ) : null}
      <div hidden={Boolean(selected)}>
        <section className="mb-7 border-b border-rule pb-6" aria-labelledby="source-review-title">
          <h2 id="source-review-title" className="mb-3 text-[1.4rem] font-bold">
            {c.sourceReviewTitle}
          </h2>
          <p className="mb-4 max-w-[75ch] text-muted">{c.sourceReviewDetail}</p>
          <button
            className={button}
            disabled={sourcesBusy || Boolean(importing)}
            onClick={() => void review()}
          >
            {sourcesBusy ? c.reviewingSources : c.reviewSources}
          </button>
          {sourcesBusy && (
            <p role="status" className="mt-3">
              {c.reviewingSources}
            </p>
          )}
          {sourcesError && (
            <p role="alert" className="mt-3 text-warning">
              {c.sourceReviewFailed}
            </p>
          )}
          {reviewed && !sources.length && (
            <p role="status" className="mt-3">
              {c.noSources}
            </p>
          )}
          {reviewed && sources.length > 0 && (
            <div className="mt-5 grid gap-4">
              {sources.map((source) => (
                <section
                  key={source.source_id}
                  className="min-w-0 rounded border border-rule p-4"
                  aria-label={source.label}
                >
                  <h3 className="mb-2 font-bold wrap-anywhere">{source.label}</h3>
                  <p className="mb-3 text-sm text-muted">
                    {c.sourceCount.replace("{count}", number.format(source.session_count))} ·{" "}
                    {c.sourceState[source.status as keyof typeof c.sourceState] ??
                      c.unsupportedSource}
                  </p>
                  {source.status === "supported" ? (
                    <>
                      <label className="mb-3 flex min-h-11 items-center gap-3">
                        <input
                          type="checkbox"
                          checked={consentedSource === source.source_id}
                          disabled={Boolean(importing)}
                          onChange={(event) =>
                            setConsentedSource(event.target.checked ? source.source_id : "")
                          }
                        />
                        <span>{c.importConsent}</span>
                      </label>
                      <button
                        className={button}
                        disabled={consentedSource !== source.source_id || Boolean(importing)}
                        onClick={() => void importReviewed(source)}
                      >
                        {importing === source.source_id ? c.importing : c.importSource}
                      </button>
                    </>
                  ) : (
                    <p className="text-warning">
                      {c.sourceAction[source.status as keyof typeof c.sourceAction] ??
                        c.unsupportedSource}
                    </p>
                  )}
                </section>
              ))}
            </div>
          )}
          {importResult && (
            <p role="status" className="mt-4">
              {importResult}
            </p>
          )}
          {importing && (
            <p role="status" className="mt-4">
              {c.importing}
            </p>
          )}
        </section>
        <div className="mb-4 flex flex-wrap items-end gap-3 [&_label]:grid [&_label]:min-w-0 [&_label]:gap-2 max-sm:[&_label]:w-full">
          <label>
            {c.profile}
            <select
              className={input}
              value={filters.profile}
              onChange={(e) => change("profile", e.target.value)}
            >
              <option value="">{c.allProfiles}</option>
              <option value="__unassigned__">{c.unassigned}</option>
              {profiles.map(([id, alias]) => (
                <option value={id} key={id}>
                  {alias}
                </option>
              ))}
            </select>
          </label>
          <label>
            {c.project}
            <select
              className={input}
              value={filters.project}
              onChange={(e) => change("project", e.target.value)}
            >
              <option value="">{c.allProjects}</option>
              {projects.map(([id, alias]) => (
                <option value={id} key={id}>
                  {alias}
                </option>
              ))}
            </select>
          </label>
          <label>
            {c.dates}
            <select
              className={input}
              value={filters.dates}
              onChange={(e) => change("dates", e.target.value)}
            >
              <option value="all">{c.allDates}</option>
              <option value="7">{c.week}</option>
              <option value="30">{c.month}</option>
              <option value="custom">{c.custom}</option>
            </select>
          </label>
          <label>
            {c.type}
            <select
              className={input}
              value={filters.type}
              onChange={(e) => change("type", e.target.value)}
            >
              <option value="">{c.allTypes}</option>
              <option value="managed_launch">{c.managed}</option>
              <option value="observed_session">{c.observed}</option>
            </select>
          </label>
          {filters.dates === "custom" && (
            <>
              <label>
                {c.from}
                <input
                  className={input}
                  type="date"
                  value={filters.from}
                  onChange={(e) => change("from", e.target.value)}
                />
              </label>
              <label>
                {c.to}
                <input
                  className={input}
                  type="date"
                  value={filters.to}
                  onChange={(e) => change("to", e.target.value)}
                />
              </label>
            </>
          )}
        </div>
        <p className="mb-2 text-sm text-muted">{c.filterNote}</p>
        <p className="mb-4 text-sm text-muted">{c.dateNote}</p>
        <div className="mb-4 flex flex-wrap items-center gap-4">
          <button
            className={button}
            disabled={busy}
            onClick={() => {
              setBusy(true);
              setFailed(false);
              setReload((value) => value + 1);
            }}
          >
            {c.reload}
          </button>
          <p className="text-sm text-muted">{c.reloadNote}</p>
        </div>
        <p role="status" className="mb-4 text-muted">
          {busy
            ? c.loading
            : failed
              ? loadedAt
                ? c.retained
                : c.failed
              : `${c.count.replace("{count}", number.format(filtered.length))} · ${c.loaded} ${date.format(loadedAt)}`}
        </p>
        {invalidDates && (
          <p role="alert" className="mb-4 text-warning">
            {c.invalidDates}
          </p>
        )}
        {!busy && !failed && !filtered.length && <p>{c.noRecords}</p>}
        <p className="mb-4 text-sm text-muted">
          {c.timeline} · {c.zone}: {zone}
        </p>
        <table className="hidden w-full table-fixed border-collapse lg:table">
          <caption className="sr-only">{c.timeline}</caption>
          <thead>
            <tr>
              {[c.time, c.type, c.profile, c.project, c.state, c.action].map((name) => (
                <th key={name} className={cell} scope="col">
                  {name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {visible.map((record) => (
              <tr key={key(record)}>
                <td className={cell}>{instant(record.started_at)}</td>
                <td className={cell}>
                  {label(record.record_type)}
                  <small className="mt-2 block text-sm text-muted">
                    {provenance(record)} · {label(record.source)}
                  </small>
                </td>
                <td className={cell}>{ownership(record)}</td>
                <td className={cell}>{projectName(record)}</td>
                <td className={cell}>
                  {managed(record) ? label(record.lifecycle) : c.lastSeen}
                  <small className="mt-2 block text-sm text-muted">
                    {label(record.correlation_state)} · {c.confidence}:{" "}
                    {label(record.correlation_confidence)}
                  </small>
                </td>
                <td className={cell}>
                  <button
                    className={button}
                    data-session-key={key(record)}
                    onClick={() => open(record)}
                  >
                    {c.details}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="lg:hidden">
          {visible.map((record) => (
            <details className="border-b border-rule py-3" key={key(record)}>
              <summary className="min-h-11 cursor-pointer py-3 font-semibold wrap-anywhere">
                {instant(record.started_at)} · {label(record.record_type)} · {ownership(record)}
              </summary>
              <Facts record={record} />
              <p className="mb-4 text-sm text-muted">
                {provenance(record)} · {label(record.source)} · {label(record.correlation_state)} ·{" "}
                {c.confidence}: {label(record.correlation_confidence)}
              </p>
              <button
                className={button}
                data-session-key={key(record)}
                onClick={() => open(record)}
              >
                {c.details}
              </button>
            </details>
          ))}
        </div>
        {pages > 1 && (
          <nav aria-label={c.timeline} className="mt-6 flex flex-wrap items-center gap-3">
            <button
              className={button}
              disabled={!currentPage}
              onClick={() => setPage(currentPage - 1)}
            >
              {c.previous}
            </button>
            <p role="status">
              {c.page
                .replace("{page}", number.format(currentPage + 1))
                .replace("{pages}", number.format(pages))}
            </p>
            <button
              className={button}
              disabled={currentPage + 1 === pages}
              onClick={() => setPage(currentPage + 1)}
            >
              {c.next}
            </button>
          </nav>
        )}
      </div>
    </div>
  );
}
