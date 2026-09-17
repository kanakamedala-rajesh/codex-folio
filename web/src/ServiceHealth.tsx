import type { RefObject } from "react";
import type { MetadataResponse } from "./generated/openapi";
import { serviceHealthCopy as c } from "./copy";

type Props = {
  health: MetadataResponse;
  heading: RefObject<HTMLHeadingElement | null>;
};

export function ServiceHealth({ health, heading }: Props) {
  const recovery = health.service_state === "recovery_required";
  return (
    <section>
      <header className="mb-7 border-b border-rule pb-5">
        <h1
          ref={heading}
          tabIndex={-1}
          className="mb-4 max-w-[30ch] text-[clamp(1.8rem,3.3vw,2.75rem)] font-bold leading-[1.16] tracking-[-0.025em]"
        >
          {recovery ? c.recoveryTitle : c.lockedTitle}
        </h1>
        <p className="mb-0 max-w-[75ch] text-muted">{recovery ? c.recoveryState : c.lockedState}</p>
      </header>
      <p
        role="status"
        aria-live="polite"
        className="my-5 max-w-[75ch] border-y border-warning py-4 text-warning"
      >
        {recovery ? c.recoveryNotice : c.lockedNotice}
      </p>
      {recovery ? (
        <div className="grid gap-6 lg:grid-cols-[1.1fr_0.9fr] lg:gap-10">
          <section>
            <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
              {c.recoveryGuidance}
            </h2>
            <ol className="mb-4 max-w-[75ch] list-decimal pl-7">
              {c.recoverySteps.map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
            <p className="mb-4 max-w-[75ch]">{c.recoveryUnavailable}</p>
            <div className="grid gap-2">
              {health.guidance_commands.map((command) => (
                <code className="wrap-anywhere" key={command}>
                  {command}
                </code>
              ))}
            </div>
          </section>
          <section>
            <h2 className="mb-4 text-[1.4rem] font-bold leading-[1.3] tracking-[-0.015em]">
              {c.checkpointRecovery}
            </h2>
            <p className="mb-4 max-w-[75ch]">{c.checkpointRetention}</p>
            <p className="mb-4 max-w-[75ch]">{c.checkpointBoundary}</p>
          </section>
        </div>
      ) : (
        <>
          <p className="mb-4 max-w-[75ch]">
            {c.runTerminal} <code className="wrap-anywhere">codex-folio vault unlock</code>{" "}
            {c.privatePrompt}
          </p>
          <p className="mb-4 max-w-[75ch]">{c.neverExpose}</p>
          <p className="mb-4 max-w-[75ch]">{c.onDemand}</p>
        </>
      )}
    </section>
  );
}
