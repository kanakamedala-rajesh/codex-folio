package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/usage"
)

func runAnalyticsWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, sink diagnostics.Sink) int {
	request, options, dryRun, nonInteractive, err := parseAnalyticsRequest(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid analytics arguments\n", apperrors.CLIUsage)
		writeAnalyticsUsage(stderr)
		return exitUsage
	}
	if input == nil {
		input = strings.NewReader("")
	}
	buffered := bufio.NewReader(input)
	return withSelectionService(buffered, stderr, resolvePaths, openStore, sink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		confirmation := request.Confirmation
		request.Confirmation = nil
		result, err := client.History(context.Background(), request)
		if err != nil {
			return err
		}
		if request.Action == "purge" && !dryRun {
			if !result.Purge.Executable {
				return apperrors.New(apperrors.AnalyticsScopeTooLarge, usage.ErrInvalid)
			}
			if confirmation == nil {
				if nonInteractive {
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

func parseAnalyticsRequest(args []string) (httpapi.HistoryRequest, selectionOptions, bool, bool, error) {
	invalid := func() (httpapi.HistoryRequest, selectionOptions, bool, bool, error) {
		return httpapi.HistoryRequest{}, selectionOptions{}, false, false, usage.ErrInvalid
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
	for _, name := range []string{"profile", "project", "from", "to", "classes", "confirm", "state-root", "vault-mode"} {
		flags.Func(name, "", func(value string) error { return set(name, value) })
	}
	for _, name := range []string{"run", "dry-run", "non-interactive", "json"} {
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
	options, err := parseSelectionOptions(common)
	if err != nil {
		return invalid()
	}
	if request.Action == "retention" {
		for _, name := range []string{"profile", "project", "from", "to", "classes", "confirm", "dry-run", "non-interactive"} {
			if values[name] != "" {
				return invalid()
			}
		}
		if values["run"] != "" {
			run := true
			request.Run = &run
		}
	} else {
		if request.Action != "purge" && request.Action != "aggregates" || values["run"] != "" {
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
	return request, options, values["dry-run"] != "", values["non-interactive"] != "", nil
}

func writePurgePreview(output io.Writer, result httpapi.PurgeResult) {
	fmt.Fprintf(output, "Analytics scope: profile=%s project=%s from=%s to=%s classes=%s\n", result.Scope.ProfileId, result.Scope.ProjectId, result.Scope.From, result.Scope.To, strings.Join(result.Scope.Classes, ","))
	for _, count := range result.Counts {
		fmt.Fprintf(output, "%s: %d\n", count.RecordClass, count.Count)
	}
	fmt.Fprintf(output, "Confirmation: %s\nExecutable: %t (limit %d affected records)\nApplied: %t\n", result.Confirmation, result.Executable, result.RecordLimit, result.Applied)
}

func writeAnalyticsUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: codex-folio analytics retention [13-months|DAYS|unlimited] [--run]")
	fmt.Fprintln(output, "       codex-folio analytics purge --profile ID|* --project ID|*|none --from RFC3339|all --to RFC3339|all --classes usage,aggregates,observed_sessions,managed_launches,checkpoints [--dry-run|--confirm TOKEN] [--non-interactive]")
	fmt.Fprintln(output, "       codex-folio analytics aggregates --profile ID|* --project ID|*|none --from RFC3339|all --to RFC3339|all")
	fmt.Fprintln(output, "All analytics commands accept --state-root PATH, --vault-mode MODE, and --json. Quote * in your shell.")
}
