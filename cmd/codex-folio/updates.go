package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type updateOptions struct {
	selectionOptions
	action    string
	automatic *bool
}

func runUpdates(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	options, err := parseUpdateOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid updates arguments\n", apperrors.CLIUsage)
		writeUpdateUsage(stderr)
		return exitUsage
	}
	return withSelectionService(strings.NewReader(""), stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		var result httpapi.UpdateResponse
		var err error
		switch options.action {
		case "status":
			result, err = client.UpdatesStatus(context.Background())
		case "check":
			result, err = client.ManageUpdates(context.Background(), httpapi.UpdateRequest{Action: "check"})
		case "settings":
			result, err = client.ManageUpdates(context.Background(), httpapi.UpdateRequest{Action: "configure", AutomaticChecks: options.automatic})
		}
		if err != nil {
			return err
		}
		if options.json {
			return writeServiceJSON(stdout, result)
		}
		writeUpdateStatus(stdout, result)
		return nil
	})
}

func parseUpdateOptions(args []string) (updateOptions, error) {
	var options updateOptions
	if len(args) == 0 || (args[0] != "status" && args[0] != "check" && args[0] != "settings") {
		return options, errors.New("update action is required")
	}
	options.action = args[0]
	common := make([]string, 0, len(args)-1)
	for index := 1; index < len(args); index++ {
		if args[index] != "--automatic" {
			common = append(common, args[index])
			continue
		}
		if options.action != "settings" || options.automatic != nil || index+1 >= len(args) {
			return updateOptions{}, errors.New("automatic preference is invalid")
		}
		value := args[index+1]
		if value != "true" && value != "false" {
			return updateOptions{}, errors.New("automatic preference must be true or false")
		}
		enabled := value == "true"
		options.automatic = &enabled
		index++
	}
	if options.action == "settings" && options.automatic == nil {
		return updateOptions{}, errors.New("automatic preference is required")
	}
	selection, err := parseSelectionOptions(common)
	if err != nil {
		return updateOptions{}, err
	}
	options.selectionOptions = selection
	return options, nil
}

func writeUpdateStatus(output io.Writer, result httpapi.UpdateResponse) {
	fmt.Fprintf(output, "Automatic update checks: %s\nStatus: %s\nCurrent version: %s\n", enabledLabel(result.AutomaticChecks), result.Status, result.CurrentVersion)
	if result.Status == "update_available" {
		fmt.Fprintf(output, "Available version: %s\nRelease notes: %s\nVerified download location: %s\nInstaller guidance: %s\n", result.AvailableVersion, result.ReleaseNotes, result.DownloadUrl, result.InstallerGuidance)
	}
	if result.CheckedAt != "" {
		fmt.Fprintf(output, "Checked at: %s\n", result.CheckedAt)
	}
	if result.ErrorCode != "" {
		fmt.Fprintf(output, "Check detail: %s\n", result.ErrorCode)
	}
}

func writeUpdateUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: codex-folio updates status [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(output, "       codex-folio updates check [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(output, "       codex-folio updates settings --automatic true|false [--state-root PATH] [--vault-mode MODE] [--json]")
}
