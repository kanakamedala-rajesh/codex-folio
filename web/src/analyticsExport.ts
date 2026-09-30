import type { AnalyticsExportResult } from "./generated/openapi";

function scalar(record: Record<string, unknown>, field: string) {
  const metric = record.metric as Record<string, unknown> | undefined;
  const correlation = record.correlation as Record<string, unknown> | undefined;
  if (field === "metric_key") return metric?.metric_key ?? record.metric_key;
  if (field === "value_kind") return metric?.value_kind;
  if (field === "unit") return metric?.unit ?? record.unit;
  if (field === "metric_scope") return metric?.scope;
  if (field === "aggregation") return metric?.aggregation;
  if (field.startsWith("correlation_")) return correlation?.[field.slice("correlation_".length)];
  return record[field];
}

function csvCell(value: unknown) {
  let text = value === undefined || value === null ? "" : String(value);
  if (text && "=+-@\t\r".includes(text[0])) text = `'${text}`;
  return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

export function encodeAnalyticsExport(result: AnalyticsExportResult) {
  if (result.filters.format === "json") {
    return { contents: `${JSON.stringify(result, null, 2)}\n`, mediaType: "application/json" };
  }
  const dataset = result.preview[0];
  if (result.preview.length !== 1 || !dataset) throw new Error("CSV export requires one dataset");
  const records = (result.records[dataset.dataset as keyof typeof result.records] ??
    []) as unknown[];
  const rows = [
    dataset.fields,
    ...records.map((record) =>
      dataset.fields.map((field) => scalar(record as Record<string, unknown>, field)),
    ),
  ];
  return {
    contents: `${rows.map((row) => row.map(csvCell).join(",")).join("\n")}\n`,
    mediaType: "text/csv;charset=utf-8",
  };
}
