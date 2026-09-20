import { useState } from "react";
import type {
  PortableConfigurationBundle,
  PortableConfigurationPreview,
  PortableConfigurationRequest,
  PortableConfigurationResponse,
} from "./generated/openapi";
import { portableConfigurationCopy as c } from "./copy";

type Props = {
  busy: boolean;
  manage: (request: PortableConfigurationRequest) => Promise<PortableConfigurationResponse>;
  applied: () => Promise<void>;
};

const button =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-3 py-2 font-semibold text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";

export function ConfigurationTransfer({ busy, manage, applied }: Props) {
  const [includeProjects, setIncludeProjects] = useState(false);
  const [exportPreview, setExportPreview] = useState<PortableConfigurationPreview>();
  const [importBundle, setImportBundle] = useState<PortableConfigurationBundle>();
  const [importPreview, setImportPreview] = useState<PortableConfigurationPreview>();
  const [resolutions, setResolutions] = useState<Record<string, string>>({});
  const [message, setMessage] = useState("");

  async function previewExport() {
    setMessage(c.preparingExport);
    try {
      const response = await manage({
        action: "export_preview",
        include_project_aliases: includeProjects,
      });
      if (!response.preview?.bundle) throw new Error("missing export bundle");
      setExportPreview(response.preview);
      setMessage(c.exportReady);
    } catch {
      setMessage(c.failed);
    }
  }

  function download() {
    if (!exportPreview?.bundle) return;
    const blob = new Blob([JSON.stringify(exportPreview.bundle, null, 2) + "\n"], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "codex-folio-configuration-v1.json";
    anchor.click();
    URL.revokeObjectURL(url);
    setExportPreview(undefined);
    setMessage(c.exported);
  }

  async function chooseImport(file?: File) {
    setImportBundle(undefined);
    setImportPreview(undefined);
    setResolutions({});
    if (!file) return;
    if (file.size > 10 * 1024 * 1024) {
      setMessage(c.invalid);
      return;
    }
    try {
      const bundle = JSON.parse(await file.text()) as PortableConfigurationBundle;
      setImportBundle(bundle);
      setMessage(c.fileReady(file.name));
    } catch {
      setMessage(c.invalid);
    }
  }

  async function previewImport() {
    if (!importBundle) return;
    setMessage(c.preparingImport);
    try {
      const response = await manage({ action: "import_preview", bundle: importBundle });
      if (!response.preview) throw new Error("missing import preview");
      setImportPreview(response.preview);
      setResolutions({});
      setMessage(response.preview.conflicts.length ? c.conflictsReady : c.importReady);
    } catch {
      setMessage(c.invalid);
    }
  }

  async function applyImport() {
    if (!importBundle || !importPreview) return;
    setMessage(c.applying);
    try {
      const response = await manage({
        action: "import_apply",
        bundle: importBundle,
        confirmation_digest: importPreview.confirmation_digest,
        reviewed: true,
        resolutions,
      });
      if (!response.result?.applied) throw new Error("configuration was not applied");
      await applied();
      setImportBundle(undefined);
      setImportPreview(undefined);
      setResolutions({});
      setMessage(c.applied);
    } catch {
      setMessage(c.stale);
    }
  }

  const allResolved =
    importPreview?.conflicts.every((conflict) =>
      conflict.resolutions.includes(resolutions[conflict.key]),
    ) ?? false;

  return (
    <section className="border-b border-rule py-6" aria-labelledby="portable-configuration-title">
      <h2
        id="portable-configuration-title"
        className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]"
      >
        {c.title}
      </h2>
      <p className="mb-2 max-w-[75ch]">{c.detail}</p>
      <p className="mb-5 max-w-[75ch] text-muted">{c.boundary}</p>

      <div className="grid gap-6 lg:grid-cols-2">
        <section
          className="min-w-0 border-t border-rule pt-4"
          aria-labelledby="configuration-export-title"
        >
          <h3 id="configuration-export-title" className="mb-2 text-[1.05rem] font-bold">
            {c.exportTitle}
          </h3>
          <label className="mb-4 flex min-h-11 items-center gap-3">
            <input
              type="checkbox"
              checked={includeProjects}
              onChange={(event) => {
                setIncludeProjects(event.target.checked);
                setExportPreview(undefined);
              }}
            />
            {c.includeProjects}
          </label>
          <div className="flex flex-wrap gap-3">
            <button
              className={button}
              type="button"
              disabled={busy}
              onClick={() => void previewExport()}
            >
              {c.previewExport}
            </button>
            {exportPreview ? (
              <button
                className={`${button} border-accent bg-accent text-canvas`}
                type="button"
                disabled={busy}
                onClick={download}
              >
                {c.download}
              </button>
            ) : null}
            {exportPreview ? (
              <button
                className={button}
                type="button"
                disabled={busy}
                onClick={() => {
                  setExportPreview(undefined);
                  setMessage(c.cancelled);
                }}
              >
                {c.cancel}
              </button>
            ) : null}
          </div>
          {exportPreview ? (
            <PreviewDetails preview={exportPreview} heading={c.exportPreviewTitle} />
          ) : null}
        </section>

        <section
          className="min-w-0 border-t border-rule pt-4"
          aria-labelledby="configuration-import-title"
        >
          <h3 id="configuration-import-title" className="mb-2 text-[1.05rem] font-bold">
            {c.importTitle}
          </h3>
          <label className="mb-4 grid min-w-0 gap-2">
            {c.file}
            <input
              type="file"
              accept="application/json,.json"
              className="block min-h-11 w-full min-w-0 max-w-full rounded border border-rule bg-panel px-3 py-2 file:mr-3 file:rounded file:border file:border-rule file:bg-canvas file:px-3 file:py-1 file:text-ink"
              onChange={(event) => void chooseImport(event.target.files?.[0])}
            />
          </label>
          <div className="flex flex-wrap gap-3">
            <button
              className={button}
              type="button"
              disabled={busy || !importBundle}
              onClick={() => void previewImport()}
            >
              {c.previewImport}
            </button>
            {importPreview ? (
              <button
                className={button}
                type="button"
                disabled={busy}
                onClick={() => {
                  setImportPreview(undefined);
                  setResolutions({});
                  setMessage(c.cancelled);
                }}
              >
                {c.cancel}
              </button>
            ) : null}
          </div>
          {importPreview ? (
            <>
              <PreviewDetails preview={importPreview} heading={c.importPreviewTitle} />
              {importPreview.conflicts.length ? (
                <fieldset className="mt-4 grid gap-4">
                  <legend className="font-bold">{c.resolve}</legend>
                  {importPreview.conflicts.map((conflict) => (
                    <label className="grid gap-2" key={conflict.key}>
                      <span>
                        <strong>{conflict.kind.replaceAll("_", " ")}</strong> · {conflict.detail}
                      </span>
                      <select
                        aria-label={c.resolution(conflict.key)}
                        value={resolutions[conflict.key] ?? ""}
                        onChange={(event) =>
                          setResolutions((current) => ({
                            ...current,
                            [conflict.key]: event.target.value,
                          }))
                        }
                        className="min-h-11 rounded border border-rule bg-panel px-3 py-2 text-ink"
                      >
                        <option value="">{c.chooseResolution}</option>
                        {conflict.resolutions.map((resolution) => (
                          <option key={resolution} value={resolution}>
                            {c.resolutionLabel[resolution] ?? resolution}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                </fieldset>
              ) : null}
              <button
                className={`${button} mt-4 border-accent bg-accent text-canvas`}
                type="button"
                disabled={busy || (importPreview.conflicts.length > 0 && !allResolved)}
                onClick={() => void applyImport()}
              >
                {c.apply}
              </button>
            </>
          ) : null}
        </section>
      </div>
      <p className="mt-4 min-h-6 max-w-[75ch] text-muted" role="status" aria-live="polite">
        {message}
      </p>
    </section>
  );
}

function PreviewDetails({
  preview,
  heading,
}: {
  preview: PortableConfigurationPreview;
  heading: string;
}) {
  return (
    <section className="mt-5 border-l-3 border-accent pl-4" aria-label={heading}>
      <h4 className="mb-2 font-bold">{heading}</h4>
      <p className="mb-2">
        {preview.counts.profiles} {c.profiles} · {preview.counts.configuration_packs} {c.packs} ·{" "}
        {preview.counts.alert_thresholds} {c.thresholds} · {preview.counts.project_aliases}{" "}
        {c.projects}
      </p>
      <p className="mb-2 text-muted">{preview.fields.join(", ")}</p>
      <p className="wrap-anywhere text-muted">
        {c.excluded}: {preview.excluded_fields.join(", ")}
      </p>
      {preview.bundle ? (
        <details className="mt-3">
          <summary className="min-h-11 cursor-pointer py-2 font-semibold">{c.records}</summary>
          <pre className="wrap-anywhere max-h-80 overflow-y-auto whitespace-pre-wrap rounded border border-rule bg-canvas p-3 text-sm">
            {JSON.stringify(preview.bundle, null, 2)}
          </pre>
        </details>
      ) : null}
    </section>
  );
}
