import { useEffect, useMemo, useState, type RefObject } from "react";
import type {
  ActivityRecord,
  HandoffFields,
  HandoffRequest,
  HandoffResponse,
} from "./generated/openapi";
import { handoffCopy as c } from "./copy";

type Props = {
  initial: HandoffResponse;
  heading: RefObject<HTMLHeadingElement | null>;
  manage: (request: HandoffRequest) => Promise<HandoffResponse>;
  readActivity: (alias: string, projectId: string) => Promise<ActivityRecord[]>;
  close: () => void;
};

const buttonClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";

function editable(response: HandoffResponse): HandoffFields {
  return {
    goal: response.fields.goal.value,
    completed_work: response.fields.completed_work.value,
    pending_work: response.fields.pending_work.value,
    known_validation: response.fields.known_validation.map((item) => item.command).join("\n"),
    risks: response.fields.risks.value,
    next_action: response.fields.next_action.value,
  };
}

export function Handoff({ initial, heading, manage, readActivity, close }: Props) {
  const [response, setResponse] = useState(initial);
  const [fields, setFields] = useState<HandoffFields>(() => editable(initial));
  const [redactedPaths, setRedactedPaths] = useState<string[]>([]);
  const [redactedText, setRedactedText] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>(c.captured);
  const [lifecycle, setLifecycle] = useState<ActivityRecord>();
  const serverFields = useMemo(() => editable(response), [response]);
  const dirty =
    JSON.stringify(fields) !== JSON.stringify(serverFields) ||
    redactedPaths.length > 0 ||
    redactedText.trim() !== "";
  const files = [
    ...response.repository.staged,
    ...response.repository.modified,
    ...response.repository.untracked,
  ].filter((value, index, values) => values.indexOf(value) === index);

  useEffect(() => {
    heading.current?.focus();
  }, [heading]);
  useEffect(() => {
    if (!response.terminal_command) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const records = await readActivity(response.target_alias, response.project_id);
        if (cancelled) return;
        const created = Date.parse(response.created_at);
        setLifecycle(
          records.find(
            (record) =>
              record.record_type === "managed_launch" && Date.parse(record.started_at) >= created,
          ),
        );
      } finally {
        if (!cancelled) timer = window.setTimeout(() => void poll(), 500);
      }
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [
    readActivity,
    response.created_at,
    response.project_id,
    response.target_alias,
    response.terminal_command,
  ]);

  const update = (name: keyof HandoffFields, value: string) =>
    setFields((current) => ({ ...current, [name]: value }));
  const act = async (request: HandoffRequest, success: string) => {
    setBusy(true);
    try {
      const next = await manage(request);
      setResponse(next);
      setFields(editable(next));
      setRedactedPaths([]);
      setRedactedText("");
      setMessage(success);
    } catch {
      setMessage(c.operationFailed);
    } finally {
      setBusy(false);
    }
  };
  const save = () =>
    act(
      {
        action: "edit",
        target_alias: response.target_alias,
        checkpoint_id: response.checkpoint_id,
        fields,
        redact_paths: redactedPaths,
        redact_text: redactedText
          .split("\n")
          .map((value) => value.trim())
          .filter(Boolean),
      },
      c.saved,
    );
  const approve = () =>
    act(
      {
        action: "approve",
        target_alias: response.target_alias,
        checkpoint_id: response.checkpoint_id,
        revision: response.revision,
      },
      c.approved,
    );
  const recheck = () =>
    act(
      {
        action: "show",
        target_alias: response.target_alias,
        checkpoint_id: response.checkpoint_id,
      },
      c.checked,
    );

  const status = lifecycle
    ? lifecycle.lifecycle === "running"
      ? c.started
      : lifecycle.lifecycle === "pending"
        ? c.starting
        : lifecycle.lifecycle === "abandoned"
          ? c.failed
          : `${c.exited} · ${lifecycle.exit_status}`
    : response.terminal_command
      ? c.prepared
      : message;
  const sourceReady = response.source_state === "exited";
  return (
    <section>
      <header className="mb-7 border-b border-rule pb-5">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {response.status === "approved" ? c.reviewTitle : c.title}
        </h1>
        <p className="mb-0 max-w-[75ch] text-muted">
          {response.source_alias || c.unknownSource} → {response.target_alias} · {c.project}:{" "}
          {response.project_alias || response.project_basename}
        </p>
      </header>
      <p className="my-5 border-y border-rule py-4" role="status" aria-live="polite">
        {status}
      </p>
      <section
        className={`my-5 border-y py-4 ${sourceReady ? "border-positive" : "border-warning text-warning"}`}
      >
        <h2 className="mb-3 text-[1.4rem] font-bold">{c.readiness}</h2>
        <p className="mb-2 max-w-[75ch]">
          {c.source}:{" "}
          {response.source_state === "running"
            ? c.sourceRunning
            : response.source_state === "uncertain"
              ? c.sourceUncertain
              : c.sourceExited}
        </p>
        <p className="mb-2 max-w-[75ch]">
          {c.target}: {response.target_eligible ? c.targetReady : c.targetBlocked}
        </p>
        <p className="mb-0 max-w-[75ch] text-muted">{response.target_caution}</p>
      </section>
      <div className="grid grid-cols-1 gap-x-5 gap-y-4 lg:grid-cols-2">
        {(
          [
            ["goal", c.goal],
            ["completed_work", c.completed],
            ["pending_work", c.pending],
            ["known_validation", c.validation],
            ["risks", c.risks],
            ["next_action", c.next],
          ] as const
        ).map(([name, label]) => (
          <label className="grid min-w-0 gap-2" htmlFor={`handoff-${name}`} key={name}>
            <span>{label}</span>
            <textarea
              id={`handoff-${name}`}
              rows={3}
              value={fields[name]}
              disabled={busy || response.status === "approved"}
              onChange={(event) => update(name, event.target.value)}
              className="min-h-24 w-full rounded border border-rule bg-panel px-3 py-2 text-ink disabled:text-muted"
            />
          </label>
        ))}
      </div>
      <p className="my-4 max-w-[75ch] text-muted">
        {c.repositoryFirst} · {c.revision} {response.revision.slice(0, 10)} · {c.expires}{" "}
        {response.expires_at
          ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
              new Date(response.expires_at),
            )
          : c.never}
      </p>
      <details className="border-y border-rule py-3">
        <summary className="min-h-11 cursor-pointer py-3 font-semibold">{c.evidence}</summary>
        <p className="mb-3 text-muted">
          {response.repository.provenance} · {response.repository.completeness} ·{" "}
          {response.repository.files_changed} {c.filesChanged}
        </p>
        <dl className="grid grid-cols-[minmax(100px,0.4fr)_1fr] gap-3 max-sm:grid-cols-1">
          <dt className="text-muted">{c.branch}</dt>
          <dd className="wrap-anywhere">{response.repository.branch || c.unknown}</dd>
          <dt className="text-muted">{c.head}</dt>
          <dd className="wrap-anywhere">{response.repository.head || c.unknown}</dd>
          <dt className="text-muted">{c.validationAge}</dt>
          <dd>
            {response.fields.known_validation[0]?.timestamp
              ? new Intl.DateTimeFormat(undefined, {
                  dateStyle: "medium",
                  timeStyle: "short",
                }).format(new Date(response.fields.known_validation[0].timestamp))
              : c.notRecorded}{" "}
            · {response.fields.validation_provenance} · {response.fields.validation_completeness}
          </dd>
        </dl>
        {files.length > 0 && response.status !== "approved" && (
          <fieldset className="mt-4 grid gap-2">
            <legend className="mb-2 font-semibold">{c.redactFiles}</legend>
            {files.map((file) => (
              <label className="flex min-h-11 items-center gap-3" key={file}>
                <input
                  type="checkbox"
                  checked={redactedPaths.includes(file)}
                  onChange={(event) =>
                    setRedactedPaths((current) =>
                      event.target.checked
                        ? [...current, file]
                        : current.filter((item) => item !== file),
                    )
                  }
                />
                <span className="wrap-anywhere">{file}</span>
              </label>
            ))}
          </fieldset>
        )}
        {response.status !== "approved" && (
          <label className="mt-4 grid gap-2">
            <span>{c.redactText}</span>
            <textarea
              rows={2}
              value={redactedText}
              onChange={(event) => setRedactedText(event.target.value)}
              className="rounded border border-rule bg-panel px-3 py-2 text-ink"
            />
          </label>
        )}
      </details>
      <details className="border-b border-rule py-3">
        <summary className="min-h-11 cursor-pointer py-3 font-semibold">{c.assistance}</summary>
        <p className="mb-0 max-w-[75ch] text-muted">{c.assistanceDetail}</p>
      </details>
      {response.terminal_command && (
        <section className="my-6 border-y border-rule py-5">
          <h2 className="mb-3 text-[1.4rem] font-bold">{c.terminal}</h2>
          <p className="mb-4 max-w-[75ch]">{c.terminalDetail}</p>
          <code className="wrap-anywhere">{response.terminal_command}</code>
        </section>
      )}
      <div className="my-5 flex flex-wrap gap-3 max-sm:grid max-sm:grid-cols-2">
        {response.status !== "approved" && (
          <button className={buttonClass} disabled={busy || !dirty} onClick={() => void save()}>
            {c.save}
          </button>
        )}
        {response.status !== "approved" && (
          <button
            className={buttonClass}
            disabled={busy || dirty || !sourceReady || !response.target_eligible}
            onClick={() => void approve()}
          >
            {c.approve}
          </button>
        )}
        {!sourceReady && (
          <button className={buttonClass} disabled={busy} onClick={() => void recheck()}>
            {c.checkAgain}
          </button>
        )}
        <button className={buttonClass} disabled={busy} onClick={close}>
          {c.cancel}
        </button>
      </div>
      <p className="mb-4 max-w-[75ch]">{c.approvalDetail}</p>
      <p className="mb-4 max-w-[75ch] text-muted">{c.boundary}</p>
    </section>
  );
}
