package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type diagnosticsRunOptions struct {
	selectionOptions
	action         string
	enabled        *bool
	level          string
	retentionDays  *int
	dryRun         bool
	nonInteractive bool
	confirmation   string
	output         string
}

func runDiagnostics(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runDiagnosticsWithDependencies(args, input, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink())
}

func runDiagnosticsWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, sink diagnostics.Sink) int {
	options, err := parseDiagnosticsOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid diagnostics arguments\n", apperrors.CLIUsage)
		writeDiagnosticsUsage(stderr)
		return exitUsage
	}
	if input == nil {
		input = strings.NewReader("")
	}
	buffered := bufio.NewReader(input)
	return withSelectionService(buffered, stderr, resolvePaths, openStore, sink, options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		if options.action == "settings" {
			current, err := client.Diagnostics(context.Background(), httpapi.DiagnosticsRequest{Action: "settings"})
			if err != nil {
				return err
			}
			if options.enabled != nil || options.level != "" || options.retentionDays != nil {
				settings := current.Settings
				if options.enabled != nil {
					settings.Enabled = *options.enabled
				}
				if options.level != "" {
					settings.MinimumLevel = options.level
				}
				if options.retentionDays != nil {
					settings.RetentionDays = int64(*options.retentionDays)
				}
				current, err = client.Diagnostics(context.Background(), httpapi.DiagnosticsRequest{Action: "configure", Settings: &settings})
				if err != nil {
					return err
				}
			}
			if options.json {
				return writeServiceJSON(stdout, current)
			}
			fmt.Fprintf(stdout, "Local diagnostics: %s\nMinimum level: %s\nRetention: %d days\nBundle ceiling: %d bytes\n", enabledLabel(current.Settings.Enabled), current.Settings.MinimumLevel, current.Settings.RetentionDays, current.MaximumEncodedBytes)
			return nil
		}

		confirmation := options.confirmation
		if confirmation == "" {
			previewResponse, err := client.Diagnostics(context.Background(), httpapi.DiagnosticsRequest{Action: "preview"})
			if err != nil {
				return err
			}
			if previewResponse.Preview == nil {
				return apperrors.New(apperrors.DiagnosticsExportFailed, errors.New("diagnostic preview is unavailable"))
			}
			if options.json {
				if err := writeServiceJSON(stdout, previewResponse); err != nil {
					return err
				}
			} else {
				writeDiagnosticPreview(stdout, *previewResponse.Preview)
			}
			if options.dryRun {
				return nil
			}
			if options.nonInteractive {
				return apperrors.New(apperrors.DiagnosticsConfirmationInvalid, errors.New("diagnostic export requires preview confirmation"))
			}
			fmt.Fprintf(stderr, "Type %s to export this local diagnostic bundle: ", previewResponse.Preview.ConfirmationDigest)
			confirmation, err = buffered.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return apperrors.New(apperrors.DiagnosticsConfirmationInvalid, err)
			}
			confirmation = strings.TrimSuffix(strings.TrimSuffix(confirmation, "\n"), "\r")
		}
		exported, err := client.Diagnostics(context.Background(), httpapi.DiagnosticsRequest{Action: "export", Confirmation: &confirmation})
		if err != nil {
			return err
		}
		if exported.Bundle == nil {
			return apperrors.New(apperrors.DiagnosticsExportFailed, errors.New("diagnostic export is unavailable"))
		}
		encoded, err := json.MarshalIndent(exported.Bundle, "", "  ")
		if err != nil {
			return apperrors.New(apperrors.DiagnosticsExportFailed, err)
		}
		if err := writeAnalyticsExport(options.output, append(encoded, '\n')); err != nil {
			return apperrors.New(apperrors.DiagnosticsExportFailed, err)
		}
		if !options.json {
			fmt.Fprintf(stdout, "Diagnostic bundle written: %s\n", options.output)
		}
		return nil
	})
}

func parseDiagnosticsOptions(args []string) (diagnosticsRunOptions, error) {
	var options diagnosticsRunOptions
	if len(args) == 0 || (args[0] != "settings" && args[0] != "export") {
		return options, errors.New("diagnostics action is required")
	}
	options.action = args[0]
	values := map[string]string{}
	set := func(name, value string) error {
		if _, exists := values[name]; exists || value == "" {
			return errors.New("diagnostics option is invalid")
		}
		values[name] = value
		return nil
	}
	flags := flag.NewFlagSet("diagnostics", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	for _, name := range []string{"enabled", "level", "retention-days", "confirm", "output", "state-root", "vault-mode"} {
		flags.Func(name, "", func(value string) error { return set(name, value) })
	}
	for _, name := range []string{"dry-run", "non-interactive", "json"} {
		flags.BoolFunc(name, "", func(value string) error {
			if value != "true" {
				return errors.New("diagnostics switch is invalid")
			}
			return set(name, value)
		})
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return diagnosticsRunOptions{}, errors.New("diagnostics flags are invalid")
	}
	common := make([]string, 0, 5)
	for _, name := range []string{"state-root", "vault-mode"} {
		if value := values[name]; value != "" {
			common = append(common, "--"+name, value)
		}
	}
	if values["json"] != "" {
		common = append(common, "--json")
	}
	selection, err := parseSelectionOptions(common)
	if err != nil {
		return diagnosticsRunOptions{}, err
	}
	options.selectionOptions = selection
	if options.action == "settings" {
		for _, name := range []string{"confirm", "output", "dry-run", "non-interactive"} {
			if values[name] != "" {
				return diagnosticsRunOptions{}, errors.New("export option supplied to settings")
			}
		}
		if value := values["enabled"]; value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return diagnosticsRunOptions{}, parseErr
			}
			options.enabled = &parsed
		}
		if value := values["level"]; value != "" {
			if value != string(diagnostics.LevelInfo) && value != string(diagnostics.LevelWarning) && value != string(diagnostics.LevelError) {
				return diagnosticsRunOptions{}, errors.New("diagnostic level is invalid")
			}
			options.level = value
		}
		if value := values["retention-days"]; value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < diagnostics.MinimumRetentionDays || parsed > diagnostics.MaximumRetentionDays {
				return diagnosticsRunOptions{}, errors.New("diagnostic retention is invalid")
			}
			options.retentionDays = &parsed
		}
		return options, nil
	}
	if values["enabled"] != "" || values["level"] != "" || values["retention-days"] != "" || values["output"] == "" && values["dry-run"] == "" || values["output"] != "" && values["dry-run"] != "" || values["confirm"] != "" && values["dry-run"] != "" {
		return diagnosticsRunOptions{}, errors.New("diagnostic export options are invalid")
	}
	options.output = values["output"]
	options.confirmation = values["confirm"]
	options.dryRun = values["dry-run"] != ""
	options.nonInteractive = values["non-interactive"] != ""
	return options, nil
}

func writeDiagnosticPreview(output io.Writer, preview httpapi.DiagnosticPreview) {
	fmt.Fprintf(output, "Diagnostic bundle fields: %s\nDiagnostic records: %d\nEncoded bytes: %d\nConfirmation: %s\n", strings.Join(preview.Fields, ","), preview.DiagnosticCount, preview.EncodedBytes, preview.ConfirmationDigest)
	fmt.Fprintln(output, "Excluded: identities, workspaces, projects, paths, usage, sessions, Codex/repository content, credentials, command arguments, analytics, checkpoints, and configuration payloads")
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func writeDiagnosticsUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: codex-folio diagnostics settings [--enabled true|false] [--level info|warning|error] [--retention-days 1..30] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(output, "       codex-folio diagnostics export [--output FILE|--dry-run] [--confirm DIGEST] [--non-interactive] [--state-root PATH] [--vault-mode MODE] [--json]")
}
