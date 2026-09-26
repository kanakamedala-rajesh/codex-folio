import { useEffect, useState } from "react";
import {
  UsageRefreshError,
  type ActivitySource,
  type ActivitySourceImportResponse,
  type ActivitySourcesResponse,
} from "./generated/openapi";
import { sessionsCopy as c } from "./copy";

type Props = {
  reviewSources: () => Promise<ActivitySourcesResponse>;
  importSource: (sourceId: string) => Promise<ActivitySourceImportResponse>;
  expired: (error: unknown) => void;
  onImported: () => void;
  automaticReview?: boolean;
};

const button =
  "min-h-11 max-w-full cursor-pointer rounded border border-rule bg-panel px-[0.8rem] py-[0.55rem] text-ink hover:border-accent disabled:cursor-not-allowed disabled:border-dashed disabled:text-muted";
const number = new Intl.NumberFormat();

export function HistorySourceReview({
  reviewSources,
  importSource,
  expired,
  onImported,
  automaticReview = false,
}: Props) {
  const [sources, setSources] = useState<ActivitySource[]>([]);
  const [sourcesBusy, setSourcesBusy] = useState(false);
  const [sourcesError, setSourcesError] = useState(false);
  const [reviewed, setReviewed] = useState(false);
  const [consentedSource, setConsentedSource] = useState("");
  const [importing, setImporting] = useState("");
  const [importResult, setImportResult] = useState("");

  async function review() {
    setSourcesBusy(true);
    setSourcesError(false);
    setImportResult("");
    try {
      const result = await reviewSources();
      setSources(result.sources);
      setReviewed(true);
      setConsentedSource("");
    } catch (error) {
      if (error instanceof UsageRefreshError && [401, 403].includes(error.status)) expired(error);
      else setSourcesError(true);
    } finally {
      setSourcesBusy(false);
    }
  }

  useEffect(() => {
    if (!automaticReview) return;
    const timer = window.setTimeout(() => void review(), 0);
    return () => window.clearTimeout(timer);
    // The automatic offer runs once when the completed onboarding review mounts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [automaticReview]);

  async function importReviewed(source: ActivitySource) {
    if (source.status !== "supported" || consentedSource !== source.source_id || importing) return;
    setImporting(source.source_id);
    setImportResult("");
    try {
      const result = await importSource(source.source_id);
      setImportResult(
        c.importResult
          .replace("{count}", number.format(result.imported_count))
          .replace("{existing}", number.format(result.already_present_count)),
      );
      setConsentedSource("");
      onImported();
    } catch (error) {
      if (error instanceof UsageRefreshError && [401, 403].includes(error.status)) expired(error);
      else setImportResult(c.importFailed);
    } finally {
      setImporting("");
    }
  }

  return (
    <section className="mb-7 border-b border-rule pb-6" aria-labelledby="source-review-title">
      <h2 id="source-review-title" className="mb-3 text-[1.4rem] font-bold">
        {c.sourceReviewTitle}
      </h2>
      <p className="mb-4 max-w-[75ch] text-muted">{c.sourceReviewDetail}</p>
      <button
        className={button}
        disabled={sourcesBusy || Boolean(importing)}
        onClick={() => void review()}
      >
        {sourcesBusy ? c.reviewingSources : c.reviewSources}
      </button>
      {sourcesBusy && (
        <p role="status" className="mt-3">
          {c.reviewingSources}
        </p>
      )}
      {sourcesError && (
        <p role="alert" className="mt-3 text-warning">
          {c.sourceReviewFailed}
        </p>
      )}
      {reviewed && !sources.length && (
        <p role="status" className="mt-3">
          {c.noSources}
        </p>
      )}
      {reviewed && sources.length > 0 && (
        <div className="mt-5 grid gap-4">
          {sources.map((source) => (
            <section
              key={source.source_id}
              className="min-w-0 rounded border border-rule p-4"
              aria-label={source.label}
            >
              <h3 className="mb-2 font-bold wrap-anywhere">{source.label}</h3>
              <p className="mb-3 text-sm text-muted">
                {c.sourceCount.replace("{count}", number.format(source.session_count))} ·{" "}
                {c.sourceState[source.status as keyof typeof c.sourceState] ?? c.unsupportedSource}
              </p>
              {source.status === "supported" ? (
                <>
                  <label className="mb-3 flex min-h-11 items-center gap-3">
                    <input
                      type="checkbox"
                      checked={consentedSource === source.source_id}
                      disabled={Boolean(importing)}
                      onChange={(event) =>
                        setConsentedSource(event.target.checked ? source.source_id : "")
                      }
                    />
                    <span>{c.importConsent}</span>
                  </label>
                  <button
                    className={button}
                    disabled={consentedSource !== source.source_id || Boolean(importing)}
                    onClick={() => void importReviewed(source)}
                  >
                    {importing === source.source_id ? c.importing : c.importSource}
                  </button>
                </>
              ) : (
                <p className="text-warning">
                  {c.sourceAction[source.status as keyof typeof c.sourceAction] ??
                    c.unsupportedSource}
                </p>
              )}
            </section>
          ))}
        </div>
      )}
      {importResult && (
        <p role="status" className="mt-4">
          {importResult}
        </p>
      )}
      {importing && (
        <p role="status" className="mt-4">
          {c.importing}
        </p>
      )}
    </section>
  );
}
