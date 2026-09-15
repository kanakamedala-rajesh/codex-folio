import { useMemo, useState, type RefObject } from "react";
import {
  UsageRefreshError,
  type ConfigurationPackRequest,
  type ConfigurationPackResponse,
  type ConfigurationPackSummary,
  type ConfigurationProjectionPlan,
  type ConfigurationPromotionPreview,
  type ProfileSummary,
} from "./generated/openapi";
import { configurationCopy as c, configurationErrorCopy } from "./copy";

type Props = {
  profile: ProfileSummary;
  packs: ConfigurationPackSummary[];
  busy: boolean;
  heading: RefObject<HTMLHeadingElement | null>;
  close: () => void;
  manage: (request: ConfigurationPackRequest) => Promise<ConfigurationPackResponse>;
};

const inputClass =
  "min-h-11 w-full min-w-0 rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink caret-accent disabled:text-muted";
const buttonClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const primaryClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-accent bg-accent px-[0.8rem] py-[0.55rem] font-semibold text-canvas hover:brightness-110 disabled:cursor-not-allowed disabled:opacity-60 forced-colors:border-2 forced-colors:border-[ButtonText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText]";

function failure(error: unknown) {
  return error instanceof UsageRefreshError
    ? (configurationErrorCopy[error.code] ?? c.failed)
    : c.failed;
}

export function ConfigurationPacks({ profile, packs, busy, heading, close, manage }: Props) {
  const [message, setMessage] = useState("");
  const [packID, setPackID] = useState("");
  const [version, setVersion] = useState("");
  const [config, setConfig] = useState("");
  const [agents, setAgents] = useState("");
  const [plugins, setPlugins] = useState("");
  const [selectedVersion, setSelectedVersion] = useState("");
  const [plan, setPlan] = useState<ConfigurationProjectionPlan | null>(null);
  const [promotionVersion, setPromotionVersion] = useState("");
  const [promotion, setPromotion] = useState<ConfigurationPromotionPreview | null>(null);
  const approved = useMemo(() => packs.filter((pack) => pack.state === "approved"), [packs]);
  const drafts = useMemo(() => packs.filter((pack) => pack.state === "draft"), [packs]);

  async function run(request: ConfigurationPackRequest, done: string) {
    try {
      const response = await manage(request);
      setMessage(done);
      return response;
    } catch (error) {
      setMessage(failure(error));
      return null;
    }
  }

  async function createDraft() {
    const documents = [
      config ? { kind: "config", content: config } : null,
      agents ? { kind: "agents", content: agents } : null,
      plugins ? { kind: "plugins", content: plugins } : null,
    ].filter((document): document is { kind: string; content: string } => document !== null);
    await run({ action: "create", pack_id: packID, version, documents }, c.draftCreated);
    setSelectedVersion(`${packID}@${version}`);
  }

  function selectedParts() {
    const split = selectedVersion.lastIndexOf("@");
    return split < 1 ? null : [selectedVersion.slice(0, split), selectedVersion.slice(split + 1)];
  }

  async function approveDraft(value: string) {
    const split = value.lastIndexOf("@");
    if (split < 1) return;
    await run(
      {
        action: "approve",
        pack_id: value.slice(0, split),
        version: value.slice(split + 1),
        reviewed: true,
      },
      c.approved,
    );
  }

  async function assign() {
    const selected = selectedParts();
    if (!selected) return;
    await run(
      {
        action: "assign",
        alias: profile.alias,
        pack_id: selected[0],
        version: selected[1],
        reviewed: true,
      },
      c.assigned,
    );
    setPlan(null);
    setPromotion(null);
  }

  async function previewProjection() {
    const response = await run({ action: "preview", alias: profile.alias }, c.previewReady);
    setPlan(response?.plan ?? null);
  }

  async function applyProjection() {
    if (!plan) return;
    await run(
      { action: "apply", alias: profile.alias, reviewed: true, expected_digest: plan.digest },
      c.applied,
    );
    setPlan(null);
  }

  async function previewPromotion() {
    const response = await run(
      { action: "promotion-preview", alias: profile.alias, version: promotionVersion },
      c.promotionReady,
    );
    setPromotion(response?.promotion_preview ?? null);
  }

  async function promote() {
    if (!promotion) return;
    await run(
      {
        action: "promote",
        alias: profile.alias,
        version: promotion.to_version,
        reviewed: true,
        expected_digest: promotion.digest,
      },
      c.promoted,
    );
    setPromotion(null);
    setPromotionVersion("");
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
        <p className="max-w-[75ch] text-muted">
          {profile.display_name} · {c.localOnly}
        </p>
      </header>
      <p role="status" className="mb-5 min-h-[1.5em] max-w-[75ch] text-muted">
        {message}
      </p>
      <button className={buttonClass} disabled={busy} onClick={close}>
        {c.back}
      </button>

      <section className="mt-7 border-y border-rule py-6">
        <h2 className="mb-3 text-[1.4rem] font-bold">{c.assignment}</h2>
        <p className="mb-5">{profile.configuration_pack || c.noneAssigned}</p>
        <label className="grid max-w-2xl gap-2">
          {c.approvedVersion}
          <select
            className={inputClass}
            value={selectedVersion}
            onChange={(event) => setSelectedVersion(event.target.value)}
          >
            <option value="">{c.chooseVersion}</option>
            {approved.map((pack) => (
              <option key={`${pack.id}@${pack.version}`} value={`${pack.id}@${pack.version}`}>
                {pack.id} · {pack.version}
              </option>
            ))}
          </select>
        </label>
        <button
          className={`${primaryClass} mt-4`}
          disabled={
            busy ||
            !selectedVersion ||
            profile.identity_home_mode !== "managed" ||
            profile.status !== "ready"
          }
          onClick={() => void assign()}
        >
          {c.assign}
        </button>
        {profile.identity_home_mode !== "managed" && (
          <p className="mt-3 text-warning">{c.managedOnly}</p>
        )}
      </section>

      <section className="border-b border-rule py-6">
        <h2 className="mb-3 text-[1.4rem] font-bold">{c.projection}</h2>
        <p className="mb-4 max-w-[75ch] text-muted">{c.projectionDetail}</p>
        <button
          className={buttonClass}
          disabled={busy || !profile.configuration_pack}
          onClick={() => void previewProjection()}
        >
          {c.preview}
        </button>
        {plan && (
          <Review
            title={c.projectionReview}
            digest={plan.digest}
            paths={plan.files}
            changes={plan.conflicts}
            empty={c.noConflicts}
            warning={c.conflictsPreserved}
            action={c.apply}
            busy={busy}
            apply={() => void applyProjection()}
            cancel={() => setPlan(null)}
          />
        )}
      </section>

      <section className="border-b border-rule py-6">
        <h2 className="mb-3 text-[1.4rem] font-bold">{c.promotion}</h2>
        <p className="mb-4 max-w-[75ch] text-muted">{c.promotionDetail}</p>
        <label className="grid max-w-sm gap-2">
          {c.newVersion}
          <input
            className={inputClass}
            value={promotionVersion}
            onChange={(event) => setPromotionVersion(event.target.value)}
          />
        </label>
        <button
          className={`${buttonClass} mt-4`}
          disabled={busy || !profile.configuration_pack || !promotionVersion}
          onClick={() => void previewPromotion()}
        >
          {c.previewPromotion}
        </button>
        {promotion && (
          <Review
            title={c.promotionReview}
            digest={promotion.digest}
            paths={[]}
            changes={promotion.changes}
            empty={c.noLocalChanges}
            warning={c.promotionWarning}
            action={c.promote}
            busy={busy}
            apply={() => void promote()}
            cancel={() => setPromotion(null)}
          />
        )}
      </section>

      <details className="border-b border-rule py-5">
        <summary className="min-h-11 cursor-pointer py-3 text-[1.2rem] font-bold">
          {c.create}
        </summary>
        <p className="mb-5 max-w-[75ch] text-muted">{c.createDetail}</p>
        <div className="grid max-w-3xl gap-5 md:grid-cols-2">
          <Field label={c.packID} value={packID} set={setPackID} />
          <Field label={c.version} value={version} set={setVersion} />
        </div>
        <div className="mt-5 grid max-w-4xl gap-5">
          <Document label={c.configDocument} value={config} set={setConfig} />
          <Document label={c.agentsDocument} value={agents} set={setAgents} />
          <Document label={c.pluginsDocument} value={plugins} set={setPlugins} />
        </div>
        <button
          className={`${primaryClass} mt-5`}
          disabled={busy || !packID || !version || (!config && !agents && !plugins)}
          onClick={() => void createDraft()}
        >
          {c.saveDraft}
        </button>
      </details>

      <section className="py-6">
        <h2 className="mb-4 text-[1.4rem] font-bold">{c.versions}</h2>
        {packs.length === 0 ? (
          <p className="text-muted">{c.noVersions}</p>
        ) : (
          <ul className="divide-y divide-rule border-y border-rule">
            {packs.map((pack) => (
              <li className="py-4" key={`${pack.id}@${pack.version}`}>
                <strong>
                  {pack.id} · {pack.version}
                </strong>
                <span className="ml-3 text-muted">
                  {pack.state} · {pack.files.join(", ")}
                </span>
                {pack.state === "draft" && (
                  <button
                    className={`${buttonClass} mt-3 block`}
                    disabled={busy}
                    onClick={() => void approveDraft(`${pack.id}@${pack.version}`)}
                  >
                    {c.approve}
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
        {drafts.length > 0 && (
          <p className="mt-4 text-muted">
            {c.draftCount.replace("{count}", String(drafts.length))}
          </p>
        )}
      </section>
    </section>
  );
}

function Review({
  title,
  digest,
  paths,
  changes,
  empty,
  warning,
  action,
  busy,
  apply,
  cancel,
}: {
  title: string;
  digest: string;
  paths: string[];
  changes: { path: string; kind: string }[];
  empty: string;
  warning: string;
  action: string;
  busy: boolean;
  apply: () => void;
  cancel: () => void;
}) {
  return (
    <section className="mt-5 max-w-4xl border-y border-rule py-4 forced-colors:border-[CanvasText]">
      <h3 className="mb-2 font-bold">{title}</h3>
      <p className="wrap-anywhere text-sm text-muted">
        {c.digest} {digest}
      </p>
      {paths.length > 0 && (
        <ul className="mt-3 list-disc pl-5">
          {paths.map((path) => (
            <li key={path}>{path}</li>
          ))}
        </ul>
      )}
      <p className="mt-3 font-semibold">{changes.length === 0 ? empty : warning}</p>
      {changes.length > 0 && (
        <ul className="mt-2 list-disc pl-5">
          {changes.map((change) => (
            <li key={`${change.kind}:${change.path}`}>
              {change.path} · {change.kind}
            </li>
          ))}
        </ul>
      )}
      <div className="mt-4 flex flex-wrap gap-3">
        <button className={primaryClass} disabled={busy} onClick={apply}>
          {action}
        </button>
        <button className={buttonClass} disabled={busy} onClick={cancel}>
          {c.cancel}
        </button>
      </div>
    </section>
  );
}

function Field({
  label,
  value,
  set,
}: {
  label: string;
  value: string;
  set: (value: string) => void;
}) {
  return (
    <label className="grid gap-2">
      {label}
      <input className={inputClass} value={value} onChange={(event) => set(event.target.value)} />
    </label>
  );
}

function Document({
  label,
  value,
  set,
}: {
  label: string;
  value: string;
  set: (value: string) => void;
}) {
  return (
    <label className="grid gap-2">
      {label}
      <textarea
        className={`${inputClass} min-h-28 font-mono text-sm`}
        value={value}
        onChange={(event) => set(event.target.value)}
      />
    </label>
  );
}
