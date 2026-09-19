import { useState } from "react";
import type { UpdateRequest, UpdateResponse } from "./generated/openapi";
import { updatesCopy as c } from "./copy";

type Props = {
  data: UpdateResponse;
  busy: boolean;
  manage: (request: UpdateRequest) => Promise<UpdateResponse>;
};

const button =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";

export function Updates({ data, busy, manage }: Props) {
  const [automatic, setAutomatic] = useState(data.automatic_checks);
  const [message, setMessage] = useState("");

  async function submit(request: UpdateRequest, success = "") {
    setMessage(request.action === "check" ? c.checking : "");
    try {
      const result = await manage(request);
      setAutomatic(result.automatic_checks);
      setMessage(success);
    } catch {
      setMessage(c.failed);
    }
  }

  const status = c[data.status as keyof typeof c] ?? data.status.replaceAll("_", " ");
  return (
    <section className="border-b border-rule py-6" aria-labelledby="updates-heading">
      <h2
        id="updates-heading"
        className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]"
      >
        {c.title}
      </h2>
      <p className="mb-4 max-w-[75ch]" role="status" aria-live="polite">
        {message || status}
      </p>
      {data.checked_at ? (
        <p className="mb-4 text-sm text-muted">
          {c.checked}: {new Date(data.checked_at).toLocaleString()}
        </p>
      ) : null}
      {data.status === "update_available" ? (
        <dl className="mb-5 grid max-w-3xl grid-cols-1 gap-x-5 gap-y-2 sm:grid-cols-[minmax(150px,0.6fr)_1fr]">
          <dt className="text-muted">{c.available}</dt>
          <dd>{data.available_version}</dd>
          <dt className="text-muted">{c.notes}</dt>
          <dd className="whitespace-pre-wrap wrap-anywhere">{data.release_notes}</dd>
          <dt className="text-muted">{c.download}</dt>
          <dd>
            <code className="wrap-anywhere">{data.download_url}</code>
          </dd>
          <dt className="text-muted">{c.guidance}</dt>
          <dd className="whitespace-pre-wrap wrap-anywhere">{data.installer_guidance}</dd>
        </dl>
      ) : null}
      <div className="mb-5 max-w-3xl border-y border-rule py-4">
        <label className="flex min-h-11 cursor-pointer items-center gap-3 font-semibold">
          <input
            type="checkbox"
            className="size-4 accent-accent"
            checked={automatic}
            disabled={busy}
            onChange={(event) => setAutomatic(event.target.checked)}
          />
          {c.automatic}
        </label>
        <p className="mb-0 mt-2 max-w-[75ch] text-sm text-muted">{c.automaticDetail}</p>
      </div>
      <div className="flex flex-wrap gap-3 max-sm:[&_button]:w-full">
        <button
          type="button"
          className={button}
          disabled={busy || automatic === data.automatic_checks}
          onClick={() => void submit({ action: "configure", automatic_checks: automatic }, c.saved)}
        >
          {c.save}
        </button>
        <button
          type="button"
          className={button}
          disabled={busy}
          onClick={() => void submit({ action: "check" })}
        >
          {c.check}
        </button>
      </div>
    </section>
  );
}
