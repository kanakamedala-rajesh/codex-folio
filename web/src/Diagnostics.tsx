import { useState } from "react";
import type {
  DiagnosticsRequest,
  DiagnosticsResponse,
  DiagnosticSettings,
} from "./generated/openapi";
import { diagnosticsCopy as c } from "./copy";

type Props = {
  data: DiagnosticsResponse;
  busy: boolean;
  manage: (request: DiagnosticsRequest) => Promise<DiagnosticsResponse>;
};

export function Diagnostics({ data, busy, manage }: Props) {
  const [settings, setSettings] = useState<DiagnosticSettings>(data.settings);
  const [preview, setPreview] = useState(data.preview);
  const [message, setMessage] = useState("");

  async function save() {
    setMessage(c.saving);
    try {
      const result = await manage({ action: "configure", settings });
      setSettings(result.settings);
      setPreview(undefined);
      setMessage(c.saved);
    } catch {
      setMessage(c.failed);
    }
  }

  async function prepare() {
    setMessage(c.preparing);
    try {
      const result = await manage({ action: "preview" });
      setPreview(result.preview);
      setMessage(c.previewReady);
    } catch {
      setMessage(c.failed);
    }
  }

  async function download() {
    if (!preview) return;
    setMessage(c.exporting);
    try {
      const result = await manage({
        action: "export",
        confirmation: preview.confirmation_digest,
      });
      if (!result.bundle) throw new Error("missing confirmed diagnostic bundle");
      const blob = new Blob([JSON.stringify(result.bundle, null, 2) + "\n"], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = "codex-folio-diagnostics.json";
      anchor.click();
      URL.revokeObjectURL(url);
      setPreview(undefined);
      setMessage(c.exported);
    } catch {
      setMessage(c.failed);
    }
  }

  return (
    <section className="border-b border-rule py-6" aria-labelledby="diagnostics-title">
      <h2
        id="diagnostics-title"
        className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]"
      >
        {c.title}
      </h2>
      <p className="mb-2 max-w-[75ch]">
        {settings.enabled ? c.enabled : c.disabled} · {settings.retention_days} {c.days} ·{" "}
        {data.maximum_encoded_bytes / (1024 * 1024)} MB
      </p>
      <p className="mb-4 max-w-[75ch] text-muted">{c.detail}</p>
      <form
        className="grid max-w-xl gap-4 sm:grid-cols-2"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <label className="flex min-h-11 items-center gap-3 sm:col-span-2">
          <input
            type="checkbox"
            checked={settings.enabled}
            onChange={(event) => {
              setSettings({ ...settings, enabled: event.target.checked });
              setPreview(undefined);
            }}
          />
          {c.collect}
        </label>
        <label className="grid gap-2">
          {c.minimumLevel}
          <select
            value={settings.minimum_level}
            onChange={(event) => {
              setSettings({
                ...settings,
                minimum_level: event.target.value as DiagnosticSettings["minimum_level"],
              });
              setPreview(undefined);
            }}
            className="min-h-11 rounded border border-rule bg-panel px-3 py-2 text-ink"
          >
            <option value="info">{c.info}</option>
            <option value="warning">{c.warning}</option>
            <option value="error">{c.error}</option>
          </select>
        </label>
        <label className="grid gap-2">
          {c.retention}
          <input
            type="number"
            min="1"
            max="30"
            value={settings.retention_days}
            onChange={(event) => {
              setSettings({ ...settings, retention_days: Number(event.target.value) });
              setPreview(undefined);
            }}
            className="min-h-11 rounded border border-rule bg-panel px-3 py-2 text-ink"
          />
        </label>
        <button
          type="submit"
          disabled={busy || settings.retention_days < 1 || settings.retention_days > 30}
          className="min-h-11 rounded border border-accent bg-accent px-3 py-2 font-semibold text-canvas disabled:opacity-60 sm:col-span-2"
        >
          {c.save}
        </button>
      </form>
      <div className="mt-5 flex flex-wrap gap-3">
        <button
          type="button"
          disabled={busy}
          onClick={() => void prepare()}
          className="min-h-11 rounded border border-rule bg-panel px-3 py-2 font-semibold hover:border-accent disabled:opacity-60"
        >
          {c.preview}
        </button>
        {preview ? (
          <button
            type="button"
            disabled={busy}
            onClick={() => void download()}
            className="min-h-11 rounded border border-accent bg-accent px-3 py-2 font-semibold text-canvas disabled:opacity-60"
          >
            {c.download}
          </button>
        ) : null}
        {preview ? (
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              setPreview(undefined);
              setMessage(c.cancelled);
            }}
            className="min-h-11 rounded border border-rule bg-panel px-3 py-2"
          >
            {c.cancel}
          </button>
        ) : null}
      </div>
      {preview ? (
        <section
          className="mt-5 border-l-3 border-accent pl-4"
          aria-labelledby="diagnostics-preview"
        >
          <h3 id="diagnostics-preview" className="mb-2 text-[1.05rem] font-bold">
            {c.previewTitle}
          </h3>
          <p className="mb-2 max-w-[75ch]">
            {preview.diagnostic_count} {c.records} · {preview.encoded_bytes} {c.bytes}
          </p>
          <p className="mb-2 max-w-[75ch] text-muted">{preview.fields.join(", ")}</p>
          <p className="mb-2 max-w-[75ch] text-muted">{c.exclusions}</p>
        </section>
      ) : null}
      <p className="mt-4 min-h-6 max-w-[75ch] text-muted" role="status" aria-live="polite">
        {message}
      </p>
    </section>
  );
}
