import type { RefObject } from "react";
import type { ActivityRecord, ProjectIdentity } from "./generated/openapi";
import { launchCopy as c } from "./copy";

export type LaunchTarget = {
  profile_id: string;
  alias: string;
  display_name: string;
};

type Props = {
  target: LaunchTarget;
  projects: ProjectIdentity[];
  projectId: string;
  record?: ActivityRecord;
  selectedProfileId?: string;
  heading: RefObject<HTMLHeadingElement | null>;
  chooseProject: (projectId: string) => void;
  close: () => void;
  commandBase: string;
  commandSuffix: string;
};

const buttonClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent";

function lifecycle(record?: ActivityRecord) {
  if (!record) return c.prepared;
  if (record.lifecycle === "pending") return c.waiting;
  if (record.lifecycle === "running") return c.running;
  if (record.lifecycle === "abandoned") return c.failed;
  return `${c.exited} · ${c.exitStatus} ${record.exit_status}`;
}

export function Launch({
  target,
  projects,
  projectId,
  record,
  selectedProfileId,
  heading,
  chooseProject,
  close,
  commandBase,
  commandSuffix,
}: Props) {
  const project = projects.find((item) => item.project_id === projectId);
  const active = record?.lifecycle === "pending" || record?.lifecycle === "running";
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
        <p className="mb-0 max-w-[75ch] text-muted">{c.detail}</p>
      </header>
      <p className="my-5 border-y border-warning py-4" role="status" aria-live="polite">
        {lifecycle(record)}
      </p>
      {active && selectedProfileId !== target.profile_id && (
        <section className="my-5 border-y border-rule py-4 text-warning">
          <h2 className="mb-3 text-[1.4rem] font-bold">{c.unchanged}</h2>
          <p className="mb-0 max-w-[75ch]">{c.unchangedDetail}</p>
        </section>
      )}
      <dl className="grid max-w-3xl grid-cols-[minmax(130px,0.7fr)_1fr] gap-x-5 gap-y-4 max-sm:grid-cols-1 max-sm:gap-y-1">
        <dt className="text-muted">{c.profile}</dt>
        <dd>{target.display_name || target.alias}</dd>
        <dt className="text-muted">{c.project}</dt>
        <dd>
          {projects.length ? (
            <select
              aria-label={c.project}
              className="min-h-11 w-full max-w-md rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink disabled:text-muted"
              disabled={Boolean(record)}
              value={projectId}
              onChange={(event) => chooseProject(event.target.value)}
            >
              {projects.map((item) => (
                <option key={item.project_id} value={item.project_id}>
                  {item.alias} · {item.basename}
                </option>
              ))}
            </select>
          ) : (
            c.noProjects
          )}
        </dd>
        <dt className="text-muted">{c.workingDirectory}</dt>
        <dd>{project ? `${project.alias} · ${project.basename}` : c.notSelected}</dd>
        <dt className="text-muted">{c.arguments}</dt>
        <dd>{c.argumentsDetail}</dd>
      </dl>
      {project ? (
        <section className="my-6 border-y border-rule py-5">
          <h2 className="mb-3 text-[1.4rem] font-bold">{c.terminal}</h2>
          <p className="mb-4 max-w-[75ch]">{c.terminalDetail}</p>
          <code className="wrap-anywhere">
            {commandBase} launch {target.alias} --project {project.project_id}
            {commandSuffix ? ` ${commandSuffix}` : ""} --
          </code>
        </section>
      ) : (
        <p className="my-6 border-y border-warning py-5 text-warning">
          {c.registerProject}{" "}
          <code>
            {commandBase} project resolve .{commandSuffix ? ` ${commandSuffix}` : ""}
          </code>
        </p>
      )}
      <p className="mb-4 max-w-[75ch]">{c.lifecycleDetail}</p>
      <p className="mb-5 max-w-[75ch] text-muted">{c.boundary}</p>
      <button className={buttonClass} onClick={close}>
        {c.back}
      </button>
    </section>
  );
}
