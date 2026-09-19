import { useCallback, useEffect, useState } from "react";
import type {
  HandoffCheckpointSummary,
  HandoffOperationPreview,
  HandoffRequest,
  CheckpointManagementResponse,
} from "./generated/openapi";
import {
  checkpointCopy as c,
  checkpointExportFieldCopy,
  checkpointSourceCopy,
  checkpointStateCopy,
} from "./copy";

type Props = {
  manage: (request: HandoffRequest) => Promise<CheckpointManagementResponse>;
};

type PendingOperation = {
  checkpoint: HandoffCheckpointSummary;
  kind: "export" | "purge";
  format?: "encrypted" | "plaintext";
  preview: HandoffOperationPreview;
};

const buttonClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";

function instant(value: string) {
  if (!value) return c.never;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    new Date(value),
  );
}

function downloadFile(download: NonNullable<CheckpointManagementResponse["download"]>) {
  const binary = window.atob(download.content_base64);
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  const url = URL.createObjectURL(new Blob([bytes], { type: download.media_type }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = download.filename;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function CheckpointManagement({ manage }: Props) {
  const [checkpoints, setCheckpoints] = useState<HandoffCheckpointSummary[]>([]);
  const [repositoryRetention, setRepositoryRetention] = useState("30");
  const [assistedRetention, setAssistedRetention] = useState("7");
  const [pending, setPending] = useState<PendingOperation>();
  const [format, setFormat] = useState<"encrypted" | "plaintext">("encrypted");
  const [passphrase, setPassphrase] = useState("");
  const [plaintextAcknowledged, setPlaintextAcknowledged] = useState(false);
  const [purgeConfirmation, setPurgeConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>(c.loading);

  const load = useCallback(async () => {
    setBusy(true);
    try {
      const [inventory, retention] = await Promise.all([
        manage({ action: "list" }),
        manage({ action: "retention" }),
      ]);
      setCheckpoints(inventory.checkpoints ?? []);
      if (retention.retention_policy) {
        setRepositoryRetention(retention.retention_policy.repository_first);
        setAssistedRetention(retention.retention_policy.transcript_assisted);
      }
      setMessage(c.loaded);
    } catch {
      setMessage(c.retentionFailed);
    } finally {
      setBusy(false);
    }
  }, [manage]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [inventory, retention] = await Promise.all([
          manage({ action: "list" }),
          manage({ action: "retention" }),
        ]);
        if (cancelled) return;
        setCheckpoints(inventory.checkpoints ?? []);
        if (retention.retention_policy) {
          setRepositoryRetention(retention.retention_policy.repository_first);
          setAssistedRetention(retention.retention_policy.transcript_assisted);
        }
        setMessage(c.loaded);
      } catch {
        if (!cancelled) setMessage(c.retentionFailed);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [manage]);

  const saveRetention = async () => {
    const valid = (value: string) =>
      value === "unlimited" ||
      (/^\d+$/.test(value) && Number(value) >= 1 && Number(value) <= 3652059);
    if (!valid(repositoryRetention) || !valid(assistedRetention)) {
      setMessage(c.retentionFailed);
      return;
    }
    setBusy(true);
    try {
      await manage({
        action: "retention",
        source: "repository-first",
        setting: repositoryRetention,
      });
      await manage({
        action: "retention",
        source: "transcript-assisted",
        setting: assistedRetention,
      });
      await load();
      setMessage(c.retentionSaved);
    } catch {
      setMessage(c.retentionFailed);
      setBusy(false);
    }
  };

  const previewExport = async (checkpoint: HandoffCheckpointSummary) => {
    setBusy(true);
    try {
      const response = await manage({
        action: "export-preview",
        checkpoint_id: checkpoint.checkpoint_id,
        revision: checkpoint.revision,
        plaintext_acknowledgement: format === "plaintext",
      });
      if (!response.operation_preview) throw new Error("missing export preview");
      setPending({ checkpoint, kind: "export", format, preview: response.operation_preview });
      setMessage(c.exportReady);
    } catch {
      setMessage(c.retentionFailed);
    } finally {
      setBusy(false);
    }
  };

  const exportCheckpoint = async () => {
    if (!pending || pending.kind !== "export" || !pending.format) return;
    setBusy(true);
    try {
      const response = await manage({
        action: pending.format === "encrypted" ? "export-encrypted" : "export-plaintext",
        checkpoint_id: pending.checkpoint.checkpoint_id,
        revision: pending.checkpoint.revision,
        confirmation: pending.preview.confirmation,
        ...(pending.format === "encrypted"
          ? { passphrase }
          : { plaintext_acknowledgement: plaintextAcknowledged }),
      });
      if (!response.download) throw new Error("missing export download");
      downloadFile(response.download);
      setMessage(c.downloadReady);
      cancelOperation();
    } catch {
      setMessage(c.retentionFailed);
    } finally {
      setBusy(false);
    }
  };

  const previewPurge = async (checkpoint: HandoffCheckpointSummary) => {
    setBusy(true);
    try {
      const response = await manage({
        action: "purge-preview",
        checkpoint_id: checkpoint.checkpoint_id,
        revision: checkpoint.revision,
      });
      if (!response.operation_preview) throw new Error("missing purge preview");
      setPending({ checkpoint, kind: "purge", preview: response.operation_preview });
      setMessage(c.purgeReady);
    } catch {
      setMessage(c.retentionFailed);
    } finally {
      setBusy(false);
    }
  };

  const purge = async () => {
    if (!pending || pending.kind !== "purge") return;
    setBusy(true);
    try {
      await manage({
        action: "purge",
        checkpoint_id: pending.checkpoint.checkpoint_id,
        revision: pending.checkpoint.revision,
        confirmation: pending.preview.confirmation,
      });
      cancelOperation();
      setMessage(c.purged);
      await load();
    } catch {
      setMessage(c.retentionFailed);
      setBusy(false);
    }
  };

  const cancelOperation = () => {
    setPending(undefined);
    setPassphrase("");
    setPlaintextAcknowledged(false);
    setPurgeConfirmation("");
  };

  return (
    <section className="border-t border-rule py-6" aria-labelledby="checkpoint-data-title">
      <h2 id="checkpoint-data-title" className="mb-4 text-[1.4rem] font-bold leading-[1.3]">
        {c.title}
      </h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.detail}</p>
      <p className="mb-4 max-w-[75ch] min-h-6" role="status" aria-live="polite">
        {message}
      </p>

      <div className="mb-6 grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] lg:items-end">
        <label className="grid gap-2">
          <span>{c.repositoryRetention}</span>
          <input
            className="min-h-11 rounded border border-rule bg-panel px-3 text-ink"
            inputMode="numeric"
            value={repositoryRetention}
            onChange={(event) => setRepositoryRetention(event.target.value)}
            aria-describedby="retention-hint"
          />
        </label>
        <label className="grid gap-2">
          <span>{c.assistedRetention}</span>
          <input
            className="min-h-11 rounded border border-rule bg-panel px-3 text-ink"
            inputMode="numeric"
            value={assistedRetention}
            onChange={(event) => setAssistedRetention(event.target.value)}
            aria-describedby="retention-hint"
          />
        </label>
        <button className={buttonClass} disabled={busy} onClick={() => void saveRetention()}>
          {c.saveRetention}
        </button>
      </div>
      <p id="retention-hint" className="-mt-3 mb-6 text-sm text-muted">
        {c.retentionHint}
      </p>

      <fieldset className="mb-5 flex flex-wrap gap-5">
        <legend className="mb-2 font-semibold">{c.export}</legend>
        <label className="flex min-h-11 items-center gap-3">
          <input
            type="radio"
            name="checkpoint-export-format"
            checked={format === "encrypted"}
            onChange={() => {
              cancelOperation();
              setFormat("encrypted");
            }}
          />
          {c.encrypted}
        </label>
        <label className="flex min-h-11 items-center gap-3">
          <input
            type="radio"
            name="checkpoint-export-format"
            checked={format === "plaintext"}
            onChange={() => {
              cancelOperation();
              setFormat("plaintext");
            }}
          />
          {c.plaintext}
        </label>
      </fieldset>

      <div className="grid gap-4">
        {checkpoints.length === 0 ? (
          <p>{c.empty}</p>
        ) : (
          checkpoints.map((checkpoint) => (
            <article
              key={checkpoint.checkpoint_id}
              className="grid gap-4 border-y border-rule py-5 xl:grid-cols-[minmax(0,1fr)_auto] xl:items-start"
            >
              <div className="min-w-0">
                <h3 className="mb-2 text-lg font-semibold">
                  {checkpoint.project_alias || checkpoint.project_basename || c.unavailable}
                </h3>
                <dl className="grid grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 max-sm:grid-cols-1">
                  <dt className="text-muted">{c.status}</dt>
                  <dd>{checkpointStateCopy[checkpoint.state] ?? c.unavailable}</dd>
                  <dt className="text-muted">{c.source}</dt>
                  <dd>{checkpointSourceCopy[checkpoint.source] ?? c.unavailable}</dd>
                  <dt className="text-muted">{c.created}</dt>
                  <dd>{instant(checkpoint.created_at)}</dd>
                  <dt className="text-muted">{c.expires}</dt>
                  <dd>{instant(checkpoint.expires_at)}</dd>
                  <dt className="text-muted">{c.revision}</dt>
                  <dd className="wrap-anywhere font-mono text-sm">
                    {checkpoint.revision.slice(0, 12)}
                  </dd>
                </dl>
              </div>
              <div className="flex flex-wrap gap-3 xl:max-w-72 xl:justify-end">
                <button
                  className={buttonClass}
                  disabled={busy || !checkpoint.exportable}
                  onClick={() => void previewExport(checkpoint)}
                >
                  {c.previewExport}
                </button>
                <button
                  className={buttonClass}
                  disabled={busy || !checkpoint.purgeable}
                  onClick={() => void previewPurge(checkpoint)}
                >
                  {c.previewPurge}
                </button>
              </div>
            </article>
          ))
        )}
      </div>

      {pending?.kind === "export" && (
        <section
          className="mt-6 border-y border-accent py-5"
          aria-labelledby="export-preview-title"
        >
          <h3 id="export-preview-title" className="mb-3 text-lg font-semibold">
            {c.export} · {pending.checkpoint.project_alias || pending.checkpoint.project_basename}
          </h3>
          <p className="mb-2 font-semibold">{c.included}</p>
          <p className="mb-4 max-w-[75ch] text-muted">
            {pending.preview.included_fields
              .map((field) => checkpointExportFieldCopy[field] ?? c.unavailable)
              .join(" · ")}
          </p>
          <p className="mb-2 font-semibold">{c.excluded}</p>
          <p className="mb-4 max-w-[75ch] text-muted">
            {pending.preview.excluded_fields
              .map((field) => checkpointExportFieldCopy[field] ?? c.unavailable)
              .join(" · ")}
          </p>
          {pending.format === "encrypted" ? (
            <label className="mb-4 grid max-w-xl gap-2">
              <span>{c.passphrase}</span>
              <input
                type="password"
                autoComplete="new-password"
                className="min-h-11 rounded border border-rule bg-panel px-3 text-ink"
                value={passphrase}
                onChange={(event) => setPassphrase(event.target.value)}
              />
            </label>
          ) : (
            <label className="mb-4 flex max-w-[75ch] items-start gap-3">
              <input
                className="mt-1 size-5 shrink-0"
                type="checkbox"
                checked={plaintextAcknowledged}
                onChange={(event) => setPlaintextAcknowledged(event.target.checked)}
              />
              <span>{c.plaintextWarning}</span>
            </label>
          )}
          <div className="flex flex-wrap gap-3">
            <button
              className={buttonClass}
              disabled={
                busy || (pending.format === "encrypted" ? !passphrase : !plaintextAcknowledged)
              }
              onClick={() => void exportCheckpoint()}
            >
              {c.downloadExport}
            </button>
            <button className={buttonClass} disabled={busy} onClick={cancelOperation}>
              {c.cancelExport}
            </button>
          </div>
        </section>
      )}

      {pending?.kind === "purge" && (
        <section
          className="mt-6 border-y border-warning py-5"
          aria-labelledby="purge-preview-title"
        >
          <h3 id="purge-preview-title" className="mb-3 text-lg font-semibold">
            {c.purge} · {pending.checkpoint.project_alias || pending.checkpoint.project_basename}
          </h3>
          <p className="mb-4 max-w-[75ch]">{c.purgeWarning}</p>
          <label className="mb-4 grid max-w-xl gap-2">
            <span>{c.purgeConfirmation}</span>
            <input
              className="min-h-11 rounded border border-rule bg-panel px-3 text-ink"
              value={purgeConfirmation}
              onChange={(event) => setPurgeConfirmation(event.target.value)}
            />
          </label>
          <div className="flex flex-wrap gap-3">
            <button
              className={buttonClass}
              disabled={busy || purgeConfirmation !== "PURGE"}
              onClick={() => void purge()}
            >
              {c.applyPurge}
            </button>
            <button className={buttonClass} disabled={busy} onClick={cancelOperation}>
              {c.cancelPurge}
            </button>
          </div>
        </section>
      )}

      <button className={`${buttonClass} mt-6`} disabled={busy} onClick={() => void load()}>
        {c.refresh}
      </button>
    </section>
  );
}
