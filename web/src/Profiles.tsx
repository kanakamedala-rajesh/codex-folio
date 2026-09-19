import { useEffect, useId, useState, type RefObject } from "react";
import {
  UsageRefreshError,
  type ConfigurationPackRequest,
  type ConfigurationPackResponse,
  type ConfigurationPackSummary,
  type ProfileAuthenticationRequest,
  type ProfileAuthenticationResponse,
  type ProfileEditRequest,
  type ProfileLifecycleRecord,
  type ProfileLifecycleRequest,
  type ProfileSummary,
} from "./generated/openapi";
import { profileCopy as c, profileErrorCopy, stateCopy } from "./copy";
import { ConfigurationPacks } from "./ConfigurationPacks";

type Props = {
  profiles: ProfileSummary[];
  packs: ConfigurationPackSummary[];
  quarantined: ProfileLifecycleRecord[];
  busy: boolean;
  heading: RefObject<HTMLHeadingElement | null>;
  message: string;
  select: (alias: string) => Promise<void>;
  edit: (request: ProfileEditRequest) => Promise<void>;
  authenticate: (request: ProfileAuthenticationRequest) => Promise<ProfileAuthenticationResponse>;
  lifecycle: (request: ProfileLifecycleRequest) => Promise<ProfileLifecycleRecord>;
  manageConfiguration: (request: ConfigurationPackRequest) => Promise<ConfigurationPackResponse>;
  launch: (profile: ProfileSummary) => void;
  launchable: (profile: ProfileSummary) => boolean;
};

const inputClass =
  "min-h-11 w-full min-w-0 rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink caret-accent disabled:text-muted";
const buttonClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const primaryClass =
  "min-h-11 max-w-full cursor-pointer rounded border border-accent bg-accent px-[0.8rem] py-[0.55rem] font-semibold text-canvas hover:brightness-110 disabled:cursor-not-allowed disabled:opacity-60 forced-colors:border-2 forced-colors:border-[ButtonText] forced-colors:bg-[ButtonFace] forced-colors:text-[ButtonText]";
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

type Form = {
  alias: string;
  displayName: string;
  loginIdentity: string;
  workspace: string;
  homeMode: "managed" | "referenced";
  referencedPath: string;
  codexOverride: string;
  authMethod: "browser" | "device-code";
};

const emptyForm: Form = {
  alias: "",
  displayName: "",
  loginIdentity: "",
  workspace: "",
  homeMode: "managed",
  referencedPath: "",
  codexOverride: "",
  authMethod: "browser",
};

function profileForm(item?: ProfileSummary): Form {
  return item
    ? {
        ...emptyForm,
        alias: item.alias,
        displayName: item.display_name,
        loginIdentity: item.login_identity,
        workspace: item.workspace,
        homeMode: item.identity_home_mode === "referenced" ? "referenced" : "managed",
      }
    : { ...emptyForm };
}

function status(item: ProfileSummary) {
  if (item.status === "pending") return c.pending;
  if (item.selected) return c.selected;
  return stateCopy[item.status] ?? item.status.replaceAll("_", " ");
}

const setupStages = ["discovery", "home", "authentication", "validation"] as const;
const duplicateWarning =
  "Codex-reported Login Identity or Workspace may already be registered; this local profile remains distinct.";

function profileFailure(error: unknown) {
  return error instanceof UsageRefreshError ? (profileErrorCopy[error.code] ?? c.failed) : c.failed;
}

export function Profiles({
  profiles,
  packs,
  quarantined,
  busy,
  heading,
  message,
  select,
  edit,
  authenticate,
  lifecycle,
  manageConfiguration,
  launch,
  launchable,
}: Props) {
  const [view, setView] = useState<
    "inventory" | "setup" | "edit" | "reauthenticate" | "remove" | "purge" | "configuration"
  >("inventory");
  const [selectedAlias, setSelectedAlias] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [replacement, setReplacement] = useState("");
  const [form, setForm] = useState<Form>(emptyForm);
  const [result, setResult] = useState<ProfileAuthenticationResponse | null>(null);
  const [localMessage, setLocalMessage] = useState("");
  const current =
    profiles.find((item) => item.alias === selectedAlias) ??
    profiles.find((item) => item.selected) ??
    profiles[0];
  const quarantineRecord = quarantined.find((item) => item.profile.alias === selectedAlias);

  useEffect(() => {
    heading.current?.focus();
  }, [heading, view]);

  function open(next: typeof view, item?: ProfileSummary) {
    setSelectedAlias(item?.alias ?? "");
    setForm(profileForm(item));
    setResult(null);
    setConfirmation("");
    setReplacement("");
    setLocalMessage(
      next === "setup" ? c.setupGuidance : next === "reauthenticate" ? c.reauthGuidance : "",
    );
    setView(next);
  }

  async function openRemoval(item: ProfileSummary) {
    open("remove", item);
    try {
      await lifecycle({ action: "preview", alias: item.alias });
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  async function applyRemoval() {
    if (!current) return;
    try {
      const response = await lifecycle({
        action: "remove",
        alias: current.alias,
        replacement: replacement || undefined,
        confirmation,
      });
      setLocalMessage(
        response.action === "deregistered" ? c.deregisteredMessage : c.quarantinedMessage,
      );
      close();
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  async function restore(alias: string) {
    try {
      await lifecycle({ action: "restore", alias });
      setLocalMessage(c.restoredMessage);
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  async function purge() {
    try {
      await lifecycle({ action: "purge", alias: selectedAlias, confirmation });
      setLocalMessage(c.purgedMessage);
      close();
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  function close() {
    setView("inventory");
  }

  async function saveEdit() {
    try {
      await edit({
        alias: selectedAlias,
        new_alias: form.alias,
        display_name: form.displayName,
        login_identity: form.loginIdentity,
        workspace: form.workspace,
      });
      setSelectedAlias(form.alias);
      setLocalMessage(c.updated);
      close();
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  async function selectProfile(alias: string) {
    try {
      await select(alias);
      setLocalMessage(c.selectedMessage);
    } catch {
      setLocalMessage(c.selectionFailed);
    }
  }

  async function runAuthentication(action: "add" | "prepare" | "reauthenticate") {
    try {
      const response = await authenticate({
        action,
        alias: form.alias,
        display_name: form.displayName,
        codex_override: form.codexOverride || undefined,
        identity_home_mode: form.homeMode,
        referenced_home_path: form.referencedPath || undefined,
        auth_method: form.authMethod,
      });
      setResult(response);
      setSelectedAlias(response.profile.alias);
      setLocalMessage(
        response.outcome === "ready"
          ? c.readyMessage
          : response.outcome === "pending"
            ? c.pendingMessage
            : c.terminalRequired,
      );
      if (response.outcome === "pending") close();
    } catch (error) {
      setLocalMessage(profileFailure(error));
    }
  }

  if (view === "configuration" && current) {
    return (
      <ConfigurationPacks
        profile={current}
        packs={packs}
        busy={busy}
        heading={heading}
        close={close}
        manage={manageConfiguration}
      />
    );
  }

  if (view === "edit") {
    return (
      <section>
        <PageHeader
          heading={heading}
          title={`${c.edit} ${current?.display_name ?? form.displayName}`}
          detail={c.localOnly}
        />
        <p role="status" className="mb-4 min-h-[1.5em] max-w-[75ch] text-muted">
          {localMessage || message}
        </p>
        <div className="grid max-w-3xl gap-5 md:grid-cols-2">
          <Field
            label={c.displayName}
            value={form.displayName}
            onChange={(displayName) => setForm({ ...form, displayName })}
          />
          <Field
            label={c.alias}
            value={form.alias}
            onChange={(alias) => setForm({ ...form, alias })}
          />
          <Field
            label={c.loginIdentity}
            value={form.loginIdentity}
            onChange={(loginIdentity) => setForm({ ...form, loginIdentity })}
          />
          <Field
            label={c.workspace}
            value={form.workspace}
            onChange={(workspace) => setForm({ ...form, workspace })}
          />
        </div>
        <div className="mt-6 flex flex-wrap gap-3">
          <button className={primaryClass} disabled={busy} onClick={() => void saveEdit()}>
            {c.save}
          </button>
          <button className={buttonClass} disabled={busy} onClick={close}>
            {c.cancel}
          </button>
        </div>
      </section>
    );
  }

  if (view === "remove" && current) {
    const replacements = profiles.filter(
      (item) => item.status === "ready" && item.alias !== current.alias,
    );
    return (
      <section>
        <PageHeader heading={heading} title={c.reviewRemoval} detail={c.remoteUnaffected} />
        <p role="status" className="mb-4 min-h-[1.5em] max-w-[75ch] text-warning">
          {localMessage || c.removalWarning}
        </p>
        <div className="grid max-w-5xl gap-5 md:grid-cols-2">
          <Field
            label={c.confirmAlias}
            value={confirmation}
            onChange={setConfirmation}
            help={c.confirmAliasHelp}
          />
          {current.selected && (
            <label className="grid min-w-0 gap-[0.4rem]">
              {c.replacement}
              <select
                aria-label={c.replacement}
                className={inputClass}
                value={replacement}
                onChange={(event) => setReplacement(event.target.value)}
              >
                <option value="">{c.chooseReplacement}</option>
                {replacements.map((item) => (
                  <option key={item.profile_id} value={item.alias}>
                    {item.display_name} · {item.alias}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        <div className="my-6 max-w-5xl border-y border-rule py-5">
          <p className="mb-3">{c.managedRemovalDetail}</p>
          <p>{c.referencedRemovalDetail}</p>
        </div>
        <div className="flex flex-wrap gap-3 max-md:[&_button]:grow">
          <button
            className={primaryClass}
            disabled={busy || confirmation !== current.alias || (current.selected && !replacement)}
            onClick={() => void applyRemoval()}
          >
            {c.confirmRemoval}
          </button>
          <button className={buttonClass} disabled={busy} onClick={close}>
            {c.cancel}
          </button>
        </div>
      </section>
    );
  }

  if (view === "purge" && quarantineRecord) {
    return (
      <section>
        <PageHeader heading={heading} title={c.reviewPurge} detail={c.remoteUnaffected} />
        <p className="mb-5 max-w-[75ch] text-warning">{c.purgeWarning}</p>
        <Field
          label={c.confirmAlias}
          value={confirmation}
          onChange={setConfirmation}
          help={c.confirmAliasHelp}
        />
        <div className="mt-6 flex flex-wrap gap-3 max-md:[&_button]:grow">
          <button
            className={primaryClass}
            disabled={busy || confirmation !== quarantineRecord.profile.alias}
            onClick={() => void purge()}
          >
            {c.confirmPurge}
          </button>
          <button className={buttonClass} disabled={busy} onClick={close}>
            {c.cancel}
          </button>
        </div>
      </section>
    );
  }

  if (view === "setup" || view === "reauthenticate") {
    const reauthentication = view === "reauthenticate";
    const title = reauthentication
      ? `${c.reauthTitle} ${current?.display_name ?? form.displayName}`
      : form.alias && current?.status === "pending"
        ? `${c.finish} ${form.displayName || form.alias}`
        : c.add;
    return (
      <section>
        <PageHeader
          heading={heading}
          title={title}
          detail={reauthentication ? `${form.alias} · ${c.reauthPending}` : c.setupPending}
        />
        <p role="status" className="mb-4 min-h-[1.5em] max-w-[75ch] text-muted">
          {localMessage || message}
        </p>
        {!reauthentication && (
          <ol
            className="mb-10 flex flex-wrap gap-x-8 gap-y-3 border-b border-rule pb-6"
            aria-label={c.setupProgress}
          >
            {[c.identity, c.identityHome, c.authenticate, c.ready].map((step, index) => (
              <li key={step} className={result?.stages[setupStages[index]] ? "text-accent" : ""}>
                {index + 1}. {step}
              </li>
            ))}
          </ol>
        )}
        <div className="grid gap-8 lg:grid-cols-[minmax(0,1.15fr)_minmax(260px,0.85fr)]">
          <div>
            {!reauthentication && (
              <div className="grid gap-5 md:grid-cols-2">
                <Field
                  label={c.displayName}
                  value={form.displayName}
                  onChange={(displayName) => setForm({ ...form, displayName })}
                />
                <Field
                  label={c.alias}
                  value={form.alias}
                  onChange={(alias) => setForm({ ...form, alias })}
                />
              </div>
            )}
            {reauthentication ? (
              <section className="mt-7 border-y border-rule py-6">
                <h2 className="mb-3 text-[1.4rem] font-bold">{c.identityHome}</h2>
                <p>
                  {form.homeMode === "referenced" ? c.referenced : c.managed} · {c.reusedHome}
                </p>
              </section>
            ) : (
              <fieldset className="mt-7 border-y border-rule py-6" disabled={busy}>
                <legend className="text-[1.4rem] font-bold">{c.identityHome}</legend>
                <Radio
                  name="home-mode"
                  checked={form.homeMode === "managed"}
                  label={c.managedOption}
                  onChange={() => setForm({ ...form, homeMode: "managed" })}
                />
                <Radio
                  name="home-mode"
                  checked={form.homeMode === "referenced"}
                  label={c.referencedOption}
                  onChange={() => setForm({ ...form, homeMode: "referenced" })}
                />
                {form.homeMode === "referenced" && (
                  <Field
                    label={c.referencedPath}
                    value={form.referencedPath}
                    onChange={(referencedPath) => setForm({ ...form, referencedPath })}
                  />
                )}
              </fieldset>
            )}
            <div className="border-b border-rule py-6">
              <h2 className="mb-3 text-[1.4rem] font-bold">{c.authenticateWithCodex}</h2>
              <p className="mb-5 max-w-[75ch]">{c.credentialsStay}</p>
              <Field
                label={c.codexOverride}
                value={form.codexOverride}
                onChange={(codexOverride) => setForm({ ...form, codexOverride })}
                help={c.codexOverrideHelp}
              />
              <fieldset className="mt-5 flex flex-wrap gap-3" disabled={busy}>
                <legend className="sr-only">{c.authentication}</legend>
                <Radio
                  name="authentication-method"
                  checked={form.authMethod === "browser"}
                  label={c.browser}
                  onChange={() => setForm({ ...form, authMethod: "browser" })}
                />
                <Radio
                  name="authentication-method"
                  checked={form.authMethod === "device-code"}
                  label={c.device}
                  onChange={() => setForm({ ...form, authMethod: "device-code" })}
                />
              </fieldset>
              <div className="mt-5 flex flex-wrap gap-3">
                <button
                  className={primaryClass}
                  disabled={busy || !form.alias || (!reauthentication && !form.displayName)}
                  onClick={() =>
                    void runAuthentication(reauthentication ? "reauthenticate" : "add")
                  }
                >
                  {c.continue}
                </button>
                {!reauthentication && (
                  <button
                    className={buttonClass}
                    disabled={busy || !form.alias || !form.displayName}
                    onClick={() => void runAuthentication("prepare")}
                  >
                    {c.saveClose}
                  </button>
                )}
                <button className={buttonClass} disabled={busy} onClick={close}>
                  {c.cancel}
                </button>
              </div>
              {result?.terminal_command && (
                <section className="mt-5 max-w-[75ch] border-y border-warning py-4 text-warning">
                  <code className="wrap-anywhere text-ink">{result.terminal_command}</code>
                  <button
                    className={`${buttonClass} mt-4 block`}
                    disabled={busy}
                    onClick={() =>
                      void runAuthentication(reauthentication ? "reauthenticate" : "add")
                    }
                  >
                    {c.checkTerminal}
                  </button>
                </section>
              )}
            </div>
          </div>
          <section>
            <h2 className="mb-4 text-[1.4rem] font-bold">{c.setupStatus}</h2>
            <dl className="grid grid-cols-[minmax(130px,0.8fr)_1fr] gap-x-5 gap-y-4">
              <dt className="text-muted">{c.installedCodex}</dt>
              <dd className={result?.codex_found ? "text-positive" : "text-muted"}>
                {result?.codex_found ? `${c.found} · ${result.codex_version}` : c.checkedOnStart}
              </dd>
              <dt className="text-muted">{c.identityHome}</dt>
              <dd className={result?.stages.home ? "text-positive" : "text-muted"}>
                {result?.stages.home ? c.validated : c.waiting}
              </dd>
              <dt className="text-muted">{c.authentication}</dt>
              <dd className={result?.stages.authentication ? "text-positive" : "text-warning"}>
                {result?.stages.authentication ? c.ready : c.waiting}
              </dd>
              <dt className="text-muted">{c.configuration}</dt>
              <dd>{c.optionalPack}</dd>
            </dl>
            <p className="mt-7 border-y border-warning py-4">
              {reauthentication ? c.reauthSaved : c.setupSaved}
            </p>
            {result?.warnings.map((warning) => (
              <p className="mt-4 text-warning" key={warning}>
                {warning === duplicateWarning ? c.duplicateWarning : c.warning}
              </p>
            ))}
          </section>
        </div>
      </section>
    );
  }

  return (
    <section>
      <PageHeader heading={heading} title={c.title} detail={c.localOnly} />
      <p role="status" className="mb-4 min-h-[1.5em] max-w-[75ch] text-muted">
        {localMessage || message}
      </p>
      <button className={primaryClass} disabled={busy} onClick={() => open("setup")}>
        {c.add}
      </button>
      <p className="mt-3 mb-4 text-sm text-muted">{c.inventory}</p>
      {quarantined.length > 0 && (
        <section className="mb-7 border-y border-rule py-5">
          <h2 className="mb-3 text-[1.4rem] font-bold">{c.recovery}</h2>
          {quarantined.map((record) => (
            <div
              className="flex flex-wrap items-center justify-between gap-4 border-t border-rule py-4 first:border-0"
              key={record.profile.profile_id}
            >
              <p className="m-0 min-w-0 wrap-anywhere">
                <strong>{record.profile.display_name || record.profile.alias}</strong>
                <span className="block text-sm text-muted">
                  {c.recoverableUntil} {date.format(new Date(record.purge_after))}
                </span>
              </p>
              <div className="flex flex-wrap gap-3 max-md:w-full max-md:[&_button]:grow">
                <button
                  className={buttonClass}
                  disabled={busy || Date.parse(record.purge_after) <= Date.now()}
                  onClick={() => void restore(record.profile.alias)}
                >
                  {c.restore}
                </button>
                <button
                  className={buttonClass}
                  disabled={busy}
                  onClick={() => open("purge", record.profile)}
                >
                  {c.purge}
                </button>
              </div>
            </div>
          ))}
        </section>
      )}
      <div className="hidden overflow-x-auto lg:block">
        <table className="w-full border-collapse text-left">
          <thead>
            <tr>
              {[
                c.displayName,
                c.alias,
                c.authentication,
                c.identityHome,
                c.configuration,
                c.lastRefresh,
              ].map((item) => (
                <th className="border-b border-rule px-2 py-3" key={item}>
                  {item}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {profiles.map((item) => (
              <tr key={item.profile_id}>
                <td className="border-b border-rule px-2 py-3">
                  <button
                    className="cursor-pointer bg-transparent text-left text-ink underline-offset-4 hover:underline"
                    onClick={() => setSelectedAlias(item.alias)}
                  >
                    {item.display_name}
                  </button>
                </td>
                <td className="border-b border-rule px-2 py-3">{item.alias}</td>
                <td className="border-b border-rule px-2 py-3">{status(item)}</td>
                <td className="border-b border-rule px-2 py-3">
                  {item.identity_home_mode === "referenced" ? c.referenced : c.managed}
                </td>
                <td className="border-b border-rule px-2 py-3">
                  {item.configuration_pack || c.noPack}
                </td>
                <td className="border-b border-rule px-2 py-3">
                  {item.last_successful_refresh
                    ? date.format(new Date(item.last_successful_refresh))
                    : c.never}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="border-y border-rule lg:hidden">
        {profiles.map((item) => (
          <button
            className="flex min-h-16 w-full cursor-pointer items-center gap-3 border-b border-rule bg-transparent px-1 py-4 text-left text-ink last:border-0"
            key={item.profile_id}
            onClick={() => setSelectedAlias(item.alias)}
          >
            <svg
              className="size-4 shrink-0 fill-none stroke-current"
              viewBox="0 0 16 16"
              aria-hidden="true"
            >
              <path d="m5 3 5 5-5 5" />
            </svg>
            <span>
              {item.display_name} · {item.alias}
              <span className="mt-1 block text-sm text-muted">{status(item)}</span>
            </span>
          </button>
        ))}
      </div>
      {current && (
        <section className="border-b border-rule py-6">
          <h2 className="mb-4 text-[1.4rem] font-bold">{current.display_name}</h2>
          <dl className="mb-5 grid max-w-3xl grid-cols-[minmax(130px,0.7fr)_1fr] gap-x-5 gap-y-3">
            <dt className="text-muted">{c.alias}</dt>
            <dd>{current.alias}</dd>
            <dt className="text-muted">{c.loginIdentity}</dt>
            <dd>{current.login_identity || c.unknown}</dd>
            <dt className="text-muted">{c.workspace}</dt>
            <dd>{current.workspace || c.unknown}</dd>
            <dt className="text-muted">{c.authentication}</dt>
            <dd>
              {status(current)} ·
              {current.authentication_method
                ? ` ${stateCopy[current.authentication_method] ?? current.authentication_method}`
                : ` ${c.unknown}`}
            </dd>
          </dl>
          <div className="flex flex-wrap gap-3 max-md:[&_button]:grow">
            <button
              className={buttonClass}
              disabled={busy || current.status !== "ready" || current.selected}
              onClick={() => void selectProfile(current.alias)}
            >
              {c.select}
            </button>
            <button className={buttonClass} disabled={busy} onClick={() => open("edit", current)}>
              {c.edit}
            </button>
            {current.status === "pending" ? (
              <button
                className={primaryClass}
                disabled={busy}
                onClick={() => open("setup", current)}
              >
                {c.pending}
              </button>
            ) : (
              <button
                className={buttonClass}
                disabled={busy}
                onClick={() => open("reauthenticate", current)}
              >
                {c.reauthenticate}
              </button>
            )}
            <button
              className={buttonClass}
              disabled={busy || !launchable(current)}
              onClick={() => launch(current)}
            >
              {c.launch}
            </button>
            <button
              className={buttonClass}
              disabled={busy}
              onClick={() => open("configuration", current)}
            >
              {c.manageConfiguration}
            </button>
          </div>
          <details className="mt-6 py-2">
            <summary className="min-h-11 cursor-pointer py-3">{c.removal}</summary>
            <p className="mb-2">{c.managedRemoval}</p>
            <p>{c.referencedRemoval}</p>
            <button
              className={`${buttonClass} mt-4`}
              disabled={busy}
              onClick={() => void openRemoval(current)}
            >
              {c.reviewRemoval}
            </button>
          </details>
        </section>
      )}
    </section>
  );
}

function PageHeader({
  heading,
  title,
  detail,
}: {
  heading: RefObject<HTMLHeadingElement | null>;
  title: string;
  detail: string;
}) {
  return (
    <header className="mb-7 border-b border-rule pb-5">
      <h1
        ref={heading}
        tabIndex={-1}
        className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
      >
        {title}
      </h1>
      <p className="mb-0 max-w-[75ch] text-muted">{detail}</p>
    </header>
  );
}

function Field({
  label,
  value,
  onChange,
  help,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  help?: string;
}) {
  const id = useId();
  const helpId = help ? `${id}-help` : undefined;
  return (
    <div className="grid min-w-0 gap-[0.4rem]">
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        aria-describedby={helpId}
        className={inputClass}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      {help && (
        <small id={helpId} className="text-muted">
          {help}
        </small>
      )}
    </div>
  );
}

function Radio({
  name,
  checked,
  label,
  onChange,
}: {
  name: string;
  checked: boolean;
  label: string;
  onChange: () => void;
}) {
  return (
    <label className="flex min-h-11 cursor-pointer items-center gap-2">
      <input
        type="radio"
        name={name}
        checked={checked}
        onChange={onChange}
        className="size-4 accent-accent"
      />
      {label}
    </label>
  );
}
