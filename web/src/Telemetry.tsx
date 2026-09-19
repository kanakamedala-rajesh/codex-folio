import { useState } from "react";
import type { TelemetryRequest, TelemetryResponse } from "./generated/openapi";
import { telemetryCopy as c } from "./copy";

type Props = {
  data: TelemetryResponse;
  busy: boolean;
  manage: (request: TelemetryRequest) => Promise<TelemetryResponse>;
};

const button =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";

export function Telemetry({ data, busy, manage }: Props) {
  const [message, setMessage] = useState("");
  const prerequisites = [
    [c.endpoint, data.prerequisites.endpoint],
    [c.schema, data.prerequisites.public_schema],
    [c.notice, data.prerequisites.privacy_notice],
    [c.eventRetention, data.prerequisites.event_retention],
    [c.aggregateRetention, data.prerequisites.aggregate_retention],
    [c.deletion, data.prerequisites.deletion],
    [c.reset, data.prerequisites.reset],
  ] as const;

  async function submit(request: TelemetryRequest, success: string) {
    setMessage("");
    try {
      await manage(request);
      setMessage(success);
    } catch {
      setMessage(c.failed);
    }
  }

  return (
    <section className="border-b border-rule py-6" aria-labelledby="telemetry-heading">
      <h2
        id="telemetry-heading"
        className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]"
      >
        {c.title}
      </h2>
      <p className="mb-2 max-w-[75ch] font-semibold">
        {data.available ? c[data.status as "disabled" | "enabled"] : c.unavailable}
      </p>
      <p className="mb-4 max-w-[75ch]" role="status" aria-live="polite">
        {message || data.detail}
      </p>
      <details className="mb-5 max-w-3xl border-y border-rule py-4">
        <summary className="min-h-11 cursor-pointer font-semibold">{c.inspect}</summary>
        <dl className="mt-3 grid grid-cols-1 gap-x-5 gap-y-2 sm:grid-cols-[minmax(190px,0.65fr)_1fr]">
          <dt className="text-muted">{c.publicSchema}</dt>
          <dd>v{data.schema_version}</dd>
          <dt className="text-muted">{c.consentSchema}</dt>
          <dd>v{data.consent_schema_version}</dd>
          <dt className="text-muted">{c.retention}</dt>
          <dd>
            {data.event_retention_days} {c.days} · {data.aggregate_retention_months} {c.months}
          </dd>
          <dt className="text-muted">{c.allowed}</dt>
          <dd className="wrap-anywhere">{data.allowed_fields.join(", ")}</dd>
          <dt className="text-muted">{c.excluded}</dt>
          <dd className="wrap-anywhere">{data.excluded_fields.join(", ")}</dd>
          <dt className="text-muted">{c.features}</dt>
          <dd className="wrap-anywhere">{data.features.join(", ")}</dd>
          <dt className="text-muted">{c.outcomes}</dt>
          <dd className="wrap-anywhere">{data.outcomes.join(", ")}</dd>
          <dt className="text-muted">{c.platforms}</dt>
          <dd className="wrap-anywhere">
            {data.os_families.join(", ")} · {data.architectures.join(", ")}
          </dd>
          <dt className="text-muted">{c.durationBuckets}</dt>
          <dd className="wrap-anywhere">{data.duration_buckets.join(", ")}</dd>
          <dt className="text-muted">{c.installationId}</dt>
          <dd>{data.installation_id_present ? c.present : c.absent}</dd>
        </dl>
        <h3 className="mb-2 mt-5 font-bold">{c.prerequisites}</h3>
        <ul className="grid gap-1 sm:grid-cols-2">
          {prerequisites.map(([name, ready]) => (
            <li key={name}>
              {ready ? c.ready : c.missing} · {name}
            </li>
          ))}
        </ul>
      </details>
      <div className="flex flex-wrap gap-3 max-sm:[&_button]:w-full">
        <button
          type="button"
          className={button}
          disabled={busy || !data.available || data.enabled}
          onClick={() =>
            void submit(
              { action: "enable", schema_version: data.consent_schema_version },
              c.enabledMessage,
            )
          }
        >
          {c.enable} v{data.consent_schema_version}
        </button>
        <button
          type="button"
          className={button}
          disabled={busy || !data.enabled}
          onClick={() => void submit({ action: "revoke" }, c.revokedMessage)}
        >
          {c.revoke}
        </button>
        <button
          type="button"
          className={button}
          disabled={busy || !data.installation_id_present}
          onClick={() => void submit({ action: "reset_id" }, c.resetMessage)}
        >
          {c.resetId}
        </button>
      </div>
    </section>
  );
}
