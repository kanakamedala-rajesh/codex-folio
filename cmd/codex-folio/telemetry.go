package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type telemetryOptions struct {
	selectionOptions
	action        string
	schemaVersion *int64
}

func runTelemetry(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	options, err := parseTelemetryOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid telemetry arguments\n", apperrors.CLIUsage)
		writeTelemetryUsage(stderr)
		return exitUsage
	}
	return withSelectionService(strings.NewReader(""), stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		var result httpapi.TelemetryResponse
		var err error
		switch options.action {
		case "status", "schema":
			result, err = client.TelemetryStatus(context.Background())
		case "enable":
			result, err = client.ManageTelemetry(context.Background(), httpapi.TelemetryRequest{Action: "enable", SchemaVersion: options.schemaVersion})
		case "revoke":
			result, err = client.ManageTelemetry(context.Background(), httpapi.TelemetryRequest{Action: "revoke"})
		case "reset-id":
			result, err = client.ManageTelemetry(context.Background(), httpapi.TelemetryRequest{Action: "reset_id"})
		}
		if err != nil {
			return err
		}
		if options.json {
			return writeServiceJSON(stdout, result)
		}
		writeTelemetryStatus(stdout, result, options.action == "schema")
		return nil
	})
}

func parseTelemetryOptions(args []string) (telemetryOptions, error) {
	var options telemetryOptions
	if len(args) == 0 {
		return options, errors.New("telemetry action is required")
	}
	options.action = args[0]
	if options.action != "status" && options.action != "schema" && options.action != "enable" && options.action != "revoke" && options.action != "reset-id" {
		return telemetryOptions{}, errors.New("telemetry action is invalid")
	}
	common := make([]string, 0, len(args)-1)
	for index := 1; index < len(args); index++ {
		if args[index] != "--schema-version" {
			common = append(common, args[index])
			continue
		}
		if options.action != "enable" || options.schemaVersion != nil || index+1 >= len(args) {
			return telemetryOptions{}, errors.New("schema version is invalid")
		}
		value, err := strconv.ParseInt(args[index+1], 10, 64)
		if err != nil || value <= 0 {
			return telemetryOptions{}, errors.New("schema version is invalid")
		}
		options.schemaVersion = &value
		index++
	}
	if options.action == "enable" && options.schemaVersion == nil {
		return telemetryOptions{}, errors.New("explicit schema version is required")
	}
	selection, err := parseSelectionOptions(common)
	if err != nil {
		return telemetryOptions{}, err
	}
	options.selectionOptions = selection
	return options, nil
}

func writeTelemetryStatus(output io.Writer, result httpapi.TelemetryResponse, schemaOnly bool) {
	if !schemaOnly {
		fmt.Fprintf(output, "Telemetry: %s\nAvailable: %s\nInstallation ID present: %s\n%s\n", result.Status, enabledLabel(result.Available), enabledLabel(result.InstallationIdPresent), result.Detail)
	}
	fmt.Fprintf(output, "Public schema: v%d\nConsent schema: v%d\nIndividual event retention: %d days\nAnonymous aggregate retention: %d months\nAllowed fields: %s\nExcluded fields: %s\n",
		result.SchemaVersion, result.ConsentSchemaVersion, result.EventRetentionDays, result.AggregateRetentionMonths,
		strings.Join(result.AllowedFields, ", "), strings.Join(result.ExcludedFields, ", "))
	fmt.Fprintf(output, "OS families: %s\nArchitectures: %s\nFeatures: %s\nOutcomes: %s\nDuration buckets: %s\n",
		strings.Join(result.OsFamilies, ", "), strings.Join(result.Architectures, ", "), strings.Join(result.Features, ", "), strings.Join(result.Outcomes, ", "), strings.Join(result.DurationBuckets, ", "))
}

func writeTelemetryUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: codex-folio telemetry {status|schema|revoke|reset-id} [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(output, "       codex-folio telemetry enable --schema-version VERSION [--state-root PATH] [--vault-mode MODE] [--json]")
}
