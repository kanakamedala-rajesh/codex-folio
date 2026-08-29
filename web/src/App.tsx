import "./styles.css";

export function App() {
  return (
    <main className="mx-auto grid min-h-screen w-full max-w-4xl content-center gap-12 px-5 py-8 font-sans text-[#18302d] sm:gap-16 sm:px-16 sm:py-28">
      <section className="max-w-2xl" aria-labelledby="app-title">
        <p className="mb-3 text-xs font-bold uppercase leading-[1.4] tracking-[0.12em] text-[#ad4f2d]">
          Local-first companion
        </p>
        <h1
          id="app-title"
          className="mb-4 text-[clamp(3.25rem,10vw,7rem)] font-semibold leading-[0.92] tracking-[-0.075em]"
        >
          VenkataSudha CodexFolio
        </h1>
        <p className="mb-0 max-w-md text-[clamp(1.15rem,2.5vw,1.5rem)] leading-[1.45] text-[#49635d]">
          Switch local Codex identities and see usage in one place.
        </p>
      </section>

      <section
        className="flex flex-col items-start justify-between gap-6 border-y border-[#cbd1c5] py-6 sm:flex-row sm:items-end"
        aria-labelledby="status-title"
      >
        <div>
          <p className="mb-3 text-xs font-bold uppercase leading-[1.4] tracking-[0.12em] text-[#ad4f2d]">
            Foundation status
          </p>
          <h2
            id="status-title"
            className="mb-0 text-[clamp(1.5rem,4vw,2.25rem)] font-semibold leading-tight tracking-[-0.04em]"
          >
            Phase 0 scaffold
          </h2>
        </div>
        <span className="shrink-0 rounded-full border border-[#9db0a3] px-2.5 py-1.5 text-sm tabular-nums text-[#49635d]">
          v{__CODEXFOLIO_VERSION__}
        </span>
      </section>

      <p className="mb-0 max-w-xl text-[0.95rem] leading-[1.6] text-[#49635d]">
        Runtime product capabilities are not enabled in this foundation build. The embedded asset
        boundary is ready for later milestones.
      </p>
    </main>
  );
}
