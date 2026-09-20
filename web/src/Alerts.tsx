import { useMemo, useRef, useState, type RefObject } from "react";

import type {
  AlertActionRequest,
  AlertRecord,
  AlertsResponse,
  ProfileSummary,
} from "./generated/openapi";
import { alertsCopy as c, provenanceCopy } from "./copy";

const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

const label = (value: string) => value.replaceAll("_", " ");

export function NotificationPrivacy({
  data,
  busy,
  manage,
}: {
  data: AlertsResponse;
  busy: boolean;
  manage: (request: AlertActionRequest) => Promise<AlertsResponse>;
}) {
  const detailed = data.delivery_health.detailed_content_enabled;
  const [pendingChoice, setPendingChoice] = useState<boolean | null>(null);
  const detailedChoice = pendingChoice ?? detailed;

  function selectDetail(enabled: boolean) {
    setPendingChoice(enabled);
    void manage({ action: "set_notification_detail", detailed_content_enabled: enabled }).then(
      () => setPendingChoice(null),
      () => setPendingChoice(null),
    );
  }
  return (
    <section className="border-b border-rule py-6" aria-labelledby="notification-privacy">
      <h2 id="notification-privacy" className="mb-4 text-[1.4rem] font-bold">
        {c.privacy}
      </h2>
      <p id="notification-privacy-detail" className="mb-4 max-w-[75ch] text-muted">
        {c.privacyDetail}
      </p>
      <fieldset
        className="grid max-w-2xl gap-3"
        aria-describedby="notification-privacy-detail"
        disabled={busy}
      >
        <legend className="sr-only">{c.privacy}</legend>
        <label className="grid min-h-11 cursor-pointer grid-cols-[auto_1fr] gap-x-3 rounded border border-rule bg-panel p-3 hover:border-accent has-checked:border-accent">
          <input
            type="radio"
            name="notification-detail"
            checked={!detailedChoice}
            onChange={() => selectDetail(false)}
            className="mt-1 size-5 accent-accent"
          />
          <span>
            <strong className="block">{c.genericNotifications}</strong>
            <span className="block text-sm text-muted">{c.genericNotificationsDetail}</span>
          </span>
        </label>
        <label className="grid min-h-11 cursor-pointer grid-cols-[auto_1fr] gap-x-3 rounded border border-rule bg-panel p-3 hover:border-accent has-checked:border-accent">
          <input
            type="radio"
            name="notification-detail"
            checked={detailedChoice}
            onChange={() => selectDetail(true)}
            className="mt-1 size-5 accent-accent"
          />
          <span>
            <strong className="block">{c.detailedNotifications}</strong>
            <span className="block text-sm text-warning">{c.detailedNotificationsDetail}</span>
          </span>
        </label>
      </fieldset>
    </section>
  );
}

function AlertItem({
  alert,
  busy,
  acknowledge,
}: {
  alert: AlertRecord;
  busy: boolean;
  acknowledge: (id: string) => Promise<void>;
}) {
  return (
    <article className="border-b border-rule py-5" aria-labelledby={`alert-${alert.alert_id}`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="mb-2 text-sm font-semibold uppercase tracking-[0.08em] text-muted">
            {label(alert.severity)} · {alert.profile_alias || c.allProfiles}
          </p>
          <h3 id={`alert-${alert.alert_id}`} className="mb-3 text-[1.12rem] font-bold">
            {alert.title}
          </h3>
        </div>
        <span className="rounded border border-rule px-2 py-1 text-sm forced-colors:border-[CanvasText]">
          {label(alert.state)}
        </span>
      </div>
      {alert.remaining_percent !== undefined ? (
        <p className="mb-3 max-w-[75ch] text-[1.35rem] font-semibold">
          {number.format(alert.remaining_percent)}% {c.remaining}
        </p>
      ) : null}
      <p className="mb-3 max-w-[75ch]">{alert.guidance}</p>
      <p className="mb-3 max-w-[75ch] text-sm text-muted">
        {alert.metric_key ? `${label(alert.metric_key)} · ` : ""}
        {alert.window_end ? `${c.windowEnd} ${date.format(new Date(alert.window_end))} · ` : ""}
        {c.lastSeen} {date.format(new Date(alert.last_seen_at))} · {c.observed}{" "}
        {alert.occurrence_count}
      </p>
      <p className="mb-3 max-w-[75ch] text-sm text-muted">
        {c.evidence}: {alert.source ? label(alert.source) : c.unavailable}
        {alert.source_version ? ` ${alert.source_version}` : ""}
        {alert.provenance ? ` · ${provenanceCopy[alert.provenance] ?? alert.provenance}` : ""}
        {alert.evidence_captured_at
          ? ` · ${c.captured} ${date.format(new Date(alert.evidence_captured_at))}`
          : ""}
        {alert.availability_reason ? ` · ${label(alert.availability_reason)}` : ""}
      </p>
      {alert.state === "open" ? (
        <button
          type="button"
          disabled={busy}
          onClick={() => void acknowledge(alert.alert_id)}
          className="min-h-11 rounded border border-rule bg-panel px-3 py-2 font-semibold hover:border-accent disabled:cursor-not-allowed disabled:text-muted"
        >
          {c.acknowledge}
        </button>
      ) : null}
    </article>
  );
}

export function Alerts({
  data,
  profiles,
  busy,
  heading,
  manage,
}: {
  data: AlertsResponse;
  profiles: ProfileSummary[];
  busy: boolean;
  heading: RefObject<HTMLHeadingElement | null>;
  manage: (request: AlertActionRequest) => Promise<AlertsResponse>;
}) {
  const [tab, setTab] = useState<"active" | "history">("active");
  const tabButtons = useRef<Array<HTMLButtonElement | null>>([]);
  const [actionError, setActionError] = useState("");
  const [profileId, setProfileId] = useState(profiles[0]?.profile_id ?? "");
  const [metricKey, setMetricKey] = useState("codex.primary.used_percent");
  const configured = useMemo(
    () =>
      data.thresholds.find(
        (item) => item.profile_id === profileId && item.metric_key === metricKey,
      ),
    [data.thresholds, profileId, metricKey],
  );
  const [warning, setWarning] = useState<number | null>(null);
  const [critical, setCritical] = useState<number | null>(null);
  const warningValue = warning ?? configured?.warning_percent ?? 20;
  const criticalValue = critical ?? configured?.critical_percent ?? 10;
  const records = tab === "active" ? data.active : data.history;

  async function acknowledge(id: string) {
    setActionError("");
    try {
      await manage({ action: "acknowledge", alert_id: id });
    } catch {
      setActionError(c.failed);
    }
  }

  return (
    <section>
      <header className="mb-7 border-b border-rule pb-5">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {c.title}
        </h1>
        <p className="mb-0 max-w-[75ch] text-muted">{c.subtitle}</p>
      </header>

      <div className="mb-5 flex gap-2 border-b border-rule" role="tablist" aria-label={c.views}>
        {(["active", "history"] as const).map((value, index) => (
          <button
            key={value}
            type="button"
            role="tab"
            id={`alerts-tab-${value}`}
            aria-controls="alerts-panel"
            ref={(element) => {
              tabButtons.current[index] = element;
            }}
            tabIndex={tab === value ? 0 : -1}
            onKeyDown={(event) => {
              let next: number;
              if (event.key === "ArrowRight" || event.key === "ArrowLeft") next = 1 - index;
              else if (event.key === "Home") next = 0;
              else if (event.key === "End") next = 1;
              else return;
              event.preventDefault();
              setTab(next === 0 ? "active" : "history");
              tabButtons.current[next]?.focus();
            }}
            aria-selected={tab === value}
            onClick={() => setTab(value)}
            className="min-h-11 border-0 border-b-3 border-transparent bg-transparent px-3 py-2 font-semibold aria-selected:border-accent aria-selected:text-accent"
          >
            {value === "active" ? `${c.active} (${data.active.length})` : c.history}
          </button>
        ))}
      </div>

      {actionError && <p role="alert">{actionError}</p>}
      <section
        id="alerts-panel"
        role="tabpanel"
        aria-labelledby={`alerts-tab-${tab}`}
        tabIndex={0}
        aria-live="polite"
        aria-atomic="false"
      >
        <h2 className="sr-only">{tab === "active" ? c.active : c.history}</h2>
        {records.length ? (
          records.map((alert) => (
            <AlertItem key={alert.alert_id} alert={alert} busy={busy} acknowledge={acknowledge} />
          ))
        ) : (
          <p className="mb-6 max-w-[75ch]">{tab === "active" ? c.noActive : c.noHistory}</p>
        )}
      </section>

      <details className="border-b border-rule py-6">
        <summary className="min-h-11 cursor-pointer py-2 text-[1.2rem] font-bold">
          {c.thresholds}
        </summary>
        <p className="mb-4 max-w-[75ch] text-muted">{c.thresholdDetail}</p>
        <form
          className="grid max-w-2xl gap-4 sm:grid-cols-2"
          onSubmit={(event) => {
            event.preventDefault();
            setActionError("");
            void manage({
              action: "set_threshold",
              profile_id: profileId,
              metric_key: metricKey,
              warning_percent: warningValue,
              critical_percent: criticalValue,
            })
              .then(() => {
                setWarning(null);
                setCritical(null);
              })
              .catch(() => setActionError(c.failed));
          }}
        >
          <label className="grid gap-2">
            {c.profile}
            <select
              value={profileId}
              onChange={(event) => {
                setProfileId(event.target.value);
                setWarning(null);
                setCritical(null);
              }}
              className="min-h-11 rounded border border-rule bg-panel px-3 py-2"
            >
              {profiles.map((profile) => (
                <option key={profile.profile_id} value={profile.profile_id}>
                  {profile.alias}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-2">
            {c.window}
            <select
              value={metricKey}
              onChange={(event) => {
                setMetricKey(event.target.value);
                setWarning(null);
                setCritical(null);
              }}
              className="min-h-11 rounded border border-rule bg-panel px-3 py-2"
            >
              <option value="codex.primary.used_percent">{c.primary}</option>
              <option value="codex.secondary.used_percent">{c.secondary}</option>
            </select>
          </label>
          <label className="grid gap-2">
            {c.warning}
            <input
              type="number"
              min="1"
              max="100"
              step="1"
              value={warningValue}
              onChange={(event) => setWarning(Number(event.target.value))}
              className="min-h-11 rounded border border-rule bg-panel px-3 py-2"
            />
          </label>
          <label className="grid gap-2">
            {c.critical}
            <input
              type="number"
              min="0"
              max="99"
              step="1"
              value={criticalValue}
              onChange={(event) => setCritical(Number(event.target.value))}
              className="min-h-11 rounded border border-rule bg-panel px-3 py-2"
            />
          </label>
          <button
            type="submit"
            disabled={busy || !profileId || warningValue <= criticalValue}
            className="min-h-11 rounded border border-accent bg-accent px-3 py-2 font-semibold text-canvas disabled:cursor-not-allowed disabled:border-dashed disabled:bg-panel disabled:text-muted sm:col-span-2"
          >
            {c.save}
          </button>
        </form>
      </details>

      <NotificationPrivacy data={data} busy={busy} manage={manage} />

      <section className="py-6">
        <h2 className="mb-4 text-[1.4rem] font-bold">{c.delivery}</h2>
        <p className="mb-2 max-w-[75ch]">
          {c.dashboard}: {label(data.delivery_health.dashboard)}
        </p>
        <p className="mb-2 max-w-[75ch]">
          {c.native}: {label(data.delivery_health.native_notifications)}
        </p>
        {data.delivery_health.mechanism ? (
          <p className="mb-2 max-w-[75ch]">
            {c.mechanism}: {label(data.delivery_health.mechanism)}
          </p>
        ) : null}
        <p className="mb-4 max-w-[75ch] text-muted">{data.delivery_health.detail}</p>
      </section>
    </section>
  );
}
