package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/usage"
)

type analyticsRunOptions struct {
	selectionOptions
	dryRun, nonInteractive bool
	output                 string
}

func runAnalyticsWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, sink diagnostics.Sink) int {
	request, options, err := parseAnalyticsRequest(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid analytics arguments\n", apperrors.CLIUsage)
		writeAnalyticsUsage(stderr)
		return exitUsage
	}
	if input == nil {
		input = strings.NewReader("")
	}
	buffered := bufio.NewReader(input)
	return withSelectionService(buffered, stderr, resolvePaths, openStore, sink, options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		confirmation := request.Confirmation
		request.Confirmation = nil
		result, err := client.History(context.Background(), request)
		if err != nil {
			return err
		}
		if request.Action == "purge" && !options.dryRun {
			if !result.Purge.Executable {
				return apperrors.New(apperrors.AnalyticsScopeTooLarge, usage.ErrInvalid)
			}
			if confirmation == nil {
				if options.nonInteractive {
					return apperrors.New(apperrors.AnalyticsConfirmationInvalid, usage.ErrInvalid)
				}
				writePurgePreview(stderr, *result.Purge)
				fmt.Fprintf(stderr, "Type %s to purge this analytics scope: ", result.Purge.Confirmation)
				text, readErr := buffered.ReadString('\n')
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					return apperrors.New(apperrors.AnalyticsConfirmationInvalid, readErr)
				}
				text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
				confirmation = &text
			}
			if *confirmation != result.Purge.Confirmation {
				return apperrors.New(apperrors.AnalyticsConfirmationInvalid, usage.ErrInvalid)
			}
			request.Confirmation = confirmation
			result, err = client.History(context.Background(), request)
			if err != nil {
				return err
			}
		}
		if request.Action == "export" {
			if options.json {
				if writeErr := writeServiceJSON(stdout, analyticsExportPreview{Filters: result.Export.Filters, Datasets: result.Export.Preview}); writeErr != nil {
					return writeErr
				}
			} else {
				writeExportPreview(stdout, result.Export.Filters, result.Export.Preview)
			}
			if !options.dryRun {
				encoded, encodeErr := encodeAnalyticsExport(*result.Export)
				if encodeErr != nil {
					return apperrors.New(apperrors.AnalyticsExportFailed, encodeErr)
				}
				if writeErr := writeAnalyticsExport(options.output, encoded); writeErr != nil {
					return apperrors.New(apperrors.AnalyticsExportFailed, writeErr)
				}
			}
			return nil
		}
		if options.json {
			return writeServiceJSON(stdout, result)
		}
		switch request.Action {
		case "retention":
			fmt.Fprintf(stdout, "Analytics detail retention: %s\nRecords processed: %d\nMore maintenance: %t\n", result.Retention.Setting, result.Retention.Processed, result.Retention.More)
		case "purge":
			writePurgePreview(stdout, *result.Purge)
		case "aggregates":
			for _, aggregate := range *result.Aggregates {
				fmt.Fprintf(stdout, "%s | %s | %g %s | %s | %s to %s (%s) | %d readings\n", aggregate.ProfileId, aggregate.Metric.MetricKey, aggregate.Value, aggregate.Metric.Unit, aggregate.Provenance, aggregate.BucketStart, aggregate.BucketEnd, aggregate.Timezone, aggregate.Samples)
			}
		}
		return nil
	})
}

func parseAnalyticsRequest(args []string) (httpapi.HistoryRequest, analyticsRunOptions, error) {
	invalid := func() (httpapi.HistoryRequest, analyticsRunOptions, error) {
		return httpapi.HistoryRequest{}, analyticsRunOptions{}, usage.ErrInvalid
	}
	if len(args) == 0 {
		return invalid()
	}
	request := httpapi.HistoryRequest{Action: args[0]}
	args = args[1:]
	if request.Action == "retention" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		setting := args[0]
		if _, err := usage.ParseRetention(setting); err != nil {
			return invalid()
		}
		request.Setting, args = &setting, args[1:]
	}
	flags := flag.NewFlagSet("analytics", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	values := map[string]string{}
	set := func(name, value string) error {
		if _, exists := values[name]; exists || value == "" {
			return usage.ErrInvalid
		}
		values[name] = value
		return nil
	}
	for _, name := range []string{"profile", "project", "from", "to", "classes", "confirm", "format", "datasets", "scope", "output", "state-root", "vault-mode"} {
		flags.Func(name, "", func(value string) error { return set(name, value) })
	}
	for _, name := range []string{"run", "dry-run", "non-interactive", "include-paths", "json"} {
		flags.BoolFunc(name, "", func(value string) error {
			if value != "true" {
				return usage.ErrInvalid
			}
			return set(name, value)
		})
	}
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return invalid()
	}
	common := []string{}
	for _, name := range []string{"state-root", "vault-mode"} {
		if value, ok := values[name]; ok {
			common = append(common, "--"+name, value)
		}
	}
	if values["json"] != "" {
		common = append(common, "--json")
	}
	selection, err := parseSelectionOptions(common)
	if err != nil {
		return invalid()
	}
	options := analyticsRunOptions{selectionOptions: selection, dryRun: values["dry-run"] != "", nonInteractive: values["non-interactive"] != "", output: values["output"]}
	if request.Action == "retention" {
		for _, name := range []string{"profile", "project", "from", "to", "classes", "confirm", "format", "datasets", "scope", "output", "dry-run", "non-interactive", "include-paths"} {
			if values[name] != "" {
				return invalid()
			}
		}
		if values["run"] != "" {
			run := true
			request.Run = &run
		}
	} else if request.Action == "export" {
		for _, name := range []string{"classes", "confirm", "run", "non-interactive"} {
			if values[name] != "" {
				return invalid()
			}
		}
		if values["format"] == "" || values["datasets"] == "" || values["output"] == "" && !options.dryRun {
			return invalid()
		}
		export := activity.ExportRequest{
			Format: values["format"], Datasets: strings.Split(values["datasets"], ","), Scope: values["scope"],
			ProfileID: values["profile"], ProjectID: values["project"], From: values["from"], To: values["to"], IncludePaths: values["include-paths"] != "",
		}
		if export.Scope == "" {
			export.Scope = usage.ScopeSelectedProfile
		}
		if export.ProfileID == "" {
			export.ProfileID = "selected"
			if export.Scope == usage.ScopeOverallHistory {
				export.ProfileID = "*"
			}
		}
		if export.ProjectID == "" {
			export.ProjectID = "*"
		}
		if export.From == "" {
			export.From = "all"
		}
		if export.To == "" {
			export.To = "all"
		}
		request.Export = &httpapi.AnalyticsExportRequest{Format: export.Format, Datasets: export.Datasets, Scope: export.Scope, ProfileId: export.ProfileID, ProjectId: export.ProjectID, From: export.From, To: export.To, IncludePaths: export.IncludePaths}
	} else {
		if request.Action != "purge" && request.Action != "aggregates" || values["run"] != "" || values["format"] != "" || values["datasets"] != "" || values["scope"] != "" || values["output"] != "" || values["include-paths"] != "" {
			return invalid()
		}
		classes := strings.Split(values["classes"], ",")
		if request.Action == "aggregates" {
			if values["classes"] != "" || values["confirm"] != "" || values["dry-run"] != "" || values["non-interactive"] != "" {
				return invalid()
			}
			classes = []string{"aggregates"}
		}
		scope := usage.HistoryScope{ProfileID: values["profile"], ProjectID: values["project"], From: values["from"], To: values["to"], Classes: classes}
		if err := scope.Validate(); err != nil {
			return invalid()
		}
		request.Scope = &httpapi.HistoryScope{ProfileId: scope.ProfileID, ProjectId: scope.ProjectID, From: scope.From, To: scope.To, Classes: scope.Classes}
		if value, ok := values["confirm"]; ok {
			request.Confirmation = &value
		}
		if values["dry-run"] != "" && request.Confirmation != nil {
			return invalid()
		}
	}
	return request, options, nil
}

func writePurgePreview(output io.Writer, result httpapi.PurgeResult) {
	fmt.Fprintf(output, "Analytics scope: profile=%s project=%s from=%s to=%s classes=%s\n", result.Scope.ProfileId, result.Scope.ProjectId, result.Scope.From, result.Scope.To, strings.Join(result.Scope.Classes, ","))
	for _, count := range result.Counts {
		fmt.Fprintf(output, "%s: %d\n", count.RecordClass, count.Count)
	}
	fmt.Fprintf(output, "Confirmation: %s\nExecutable: %t (limit %d affected records)\nApplied: %t\n", result.Confirmation, result.Executable, result.RecordLimit, result.Applied)
}

type analyticsExportPreview struct {
	Filters  httpapi.AnalyticsExportRequest          `json:"filters"`
	Datasets []httpapi.AnalyticsExportDatasetPreview `json:"datasets"`
}

func writeExportPreview(output io.Writer, filters httpapi.AnalyticsExportRequest, preview []httpapi.AnalyticsExportDatasetPreview) {
	fmt.Fprintf(output, "Export filters: scope=%s profile=%s project=%s from=%s to=%s include_paths=%t\n", filters.Scope, filters.ProfileId, filters.ProjectId, filters.From, filters.To, filters.IncludePaths)
	for _, dataset := range preview {
		fmt.Fprintf(output, "Export dataset: %s\nFields: %s\nRecords: %d\n", dataset.Dataset, strings.Join(dataset.Fields, ","), dataset.RecordCount)
	}
}

func encodeAnalyticsExport(result httpapi.AnalyticsExportResult) ([]byte, error) {
	if result.Filters.Format == "json" {
		encoded, err := json.MarshalIndent(result, "", "  ")
		return append(encoded, '\n'), err
	}
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if len(result.Preview) != 1 {
		return nil, usage.ErrInvalid
	}
	if err := writer.Write(result.Preview[0].Fields); err != nil {
		return nil, err
	}
	switch result.Preview[0].Dataset {
	case "usage":
		if result.Records.Usage != nil {
			for _, record := range *result.Records.Usage {
				row := []string{record.ObservationId, record.ProfileId, record.ProfileAlias, record.ProjectId, record.ProjectAlias, record.ProjectBasename,
					record.Metric.MetricKey, strconv.FormatFloat(record.Value, 'g', -1, 64), record.Metric.ValueKind, record.Metric.Unit, record.Metric.Scope, record.Metric.Aggregation,
					record.Source, record.SourceVersion, record.Provenance, record.Freshness, record.Availability, record.LoginIdentity, record.Workspace,
					record.WindowStart, record.WindowEnd, record.WindowTimezone, record.ObservedAt, record.CapturedAt, strconv.FormatInt(record.CaptureAgeSeconds, 10), record.Assumptions, record.Uncertainty}
				if result.Filters.IncludePaths {
					row = append(row, optionalString(record.CanonicalPath))
				}
				if err := writer.Write(spreadsheetSafeCSVRow(row)); err != nil {
					return nil, err
				}
			}
		}
	case "availability":
		if result.Records.Availability != nil {
			for _, record := range *result.Records.Availability {
				row := []string{record.MetricAvailabilityId, record.ProfileId, record.ProfileAlias, record.ProjectId, record.ProjectAlias, record.ProjectBasename,
					record.Metric.MetricKey, record.Metric.ValueKind, record.Metric.Unit, record.Metric.Scope, record.Metric.Aggregation,
					record.State, record.Reason, record.CheckedAt, record.Source, record.SourceVersion, record.Provenance, record.Freshness,
					strconv.FormatInt(record.CaptureAgeSeconds, 10), record.LoginIdentity, record.Workspace}
				if result.Filters.IncludePaths {
					row = append(row, optionalString(record.CanonicalPath))
				}
				if err := writer.Write(spreadsheetSafeCSVRow(row)); err != nil {
					return nil, err
				}
			}
		}
	case "aggregates":
		if result.Records.Aggregates != nil {
			for _, record := range *result.Records.Aggregates {
				row := []string{record.Id, record.ProfileId, optionalString(record.ProfileAlias), record.ProjectId, optionalString(record.ProjectAlias), optionalString(record.ProjectBasename),
					record.Metric.MetricKey, strconv.FormatFloat(record.Value, 'g', -1, 64), record.Metric.ValueKind, record.Metric.Unit, record.Metric.Scope, record.Metric.Aggregation,
					record.Source, record.SourceVersion, record.Provenance, optionalString(record.Freshness), record.Availability, optionalString(record.LoginIdentity), optionalString(record.Workspace),
					record.BucketKind, record.BucketStart, record.BucketEnd, record.Timezone, record.FirstObservedAt, record.LastObservedAt,
					record.FirstCapturedAt, record.LastCapturedAt, strconv.FormatInt(record.Samples, 10), record.Assumptions, record.Uncertainty}
				if result.Filters.IncludePaths {
					row = append(row, optionalString(record.CanonicalPath))
				}
				if err := writer.Write(spreadsheetSafeCSVRow(row)); err != nil {
					return nil, err
				}
			}
		}
	case "activity":
		if result.Records.Activity != nil {
			for _, record := range *result.Records.Activity {
				row := []string{record.RecordType, record.Id, optionalString(record.SourceSessionId), record.ProfileId, record.ProfileAlias,
					optionalString(record.OriginalProfileId), optionalString(record.AttributionProvenance), optionalString(record.OriginalAttributionProvenance),
					optionalString(record.ProjectId), optionalString(record.ProjectAlias), optionalString(record.ProjectBasename), record.Source,
					optionalString(record.SourceVersion), record.Provenance, record.StartedAt, record.LastObservedAt, optionalString(record.Lifecycle), optionalInt(record.ExitStatus),
					optionalString(record.Model), optionalInt(record.TokensUsed), optionalString(record.MetricKey), optionalString(record.Unit), optionalString(record.Availability), optionalString(record.Freshness), optionalString(record.CoverageStartAt), optionalString(record.CoverageEndAt), record.Correlation.State, optionalString(record.Correlation.ManagedLaunchId), optionalString(record.Correlation.EvidenceType), optionalString(record.Correlation.Confidence)}
				if result.Filters.IncludePaths {
					row = append(row, optionalString(record.CanonicalPath))
				}
				if err := writer.Write(spreadsheetSafeCSVRow(row)); err != nil {
					return nil, err
				}
			}
		}
	default:
		return nil, usage.ErrInvalid
	}
	writer.Flush()
	return []byte(output.String()), writer.Error()
}

func spreadsheetSafeCSVRow(row []string) []string {
	for index, cell := range row {
		if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
			row[index] = "'" + cell
		}
	}
	return row
}

func writeAnalyticsExport(path string, contents []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	_, err = file.Write(contents)
	return err
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalInt(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func writeAnalyticsUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: codex-folio analytics retention [13-months|DAYS|unlimited] [--run]")
	fmt.Fprintln(output, "       codex-folio analytics purge --profile ID|* --project ID|*|none --from RFC3339|all --to RFC3339|all --classes usage,aggregates,observed_sessions,managed_launches,checkpoints [--dry-run|--confirm TOKEN] [--non-interactive]")
	fmt.Fprintln(output, "       codex-folio analytics aggregates --profile ID|* --project ID|*|none --from RFC3339|all --to RFC3339|all")
	fmt.Fprintln(output, "       codex-folio analytics export --format json|csv --datasets usage,availability,aggregates,activity [--scope selected_profile|combined_identity|overall_history] [--profile selected|ID|*] [--project ID|*|none] [--from RFC3339|all] [--to RFC3339|all] [--include-paths] [--output FILE|--dry-run]")
	fmt.Fprintln(output, "All analytics commands accept --state-root PATH, --vault-mode MODE, and --json. Quote * in your shell.")
}
