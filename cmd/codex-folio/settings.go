package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type collectionSettingsOptions struct {
	selectionOptions
	activeMinutes *int
	idleMinutes   *int
}

func runSettings(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	options, err := parseCollectionSettingsOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid settings arguments\n", apperrors.CLIUsage)
		writeCollectionSettingsUsage(stderr)
		return exitUsage
	}
	return withSelectionService(os.Stdin, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		current, err := client.CollectionSettings(context.Background())
		if err != nil {
			return err
		}
		if options.activeMinutes != nil || options.idleMinutes != nil {
			activeSeconds, idleSeconds := current.ActiveIntervalSeconds, current.IdleIntervalSeconds
			if options.activeMinutes != nil {
				activeSeconds = int64(*options.activeMinutes * 60)
			}
			if options.idleMinutes != nil {
				idleSeconds = int64(*options.idleMinutes * 60)
			}
			current, err = client.SetCollectionSettings(context.Background(), httpapi.CollectionSettingsRequest{ActiveIntervalSeconds: activeSeconds, IdleIntervalSeconds: idleSeconds})
			if err != nil {
				return err
			}
		}
		if options.json {
			return writeServiceJSON(stdout, current)
		}
		fmt.Fprintf(stdout, "Periodic collection: %s\nActive interval: %d minutes\nIdle interval: %d minutes\nProvider-safe floor: %d minutes\n", current.Consent, current.ActiveIntervalSeconds/60, current.IdleIntervalSeconds/60, current.ProviderMinimumSeconds/60)
		return nil
	})
}

func parseCollectionSettingsOptions(args []string) (collectionSettingsOptions, error) {
	var options collectionSettingsOptions
	if len(args) == 0 || args[0] != "collection" {
		return options, errors.New("collection settings command is required")
	}
	common := make([]string, 0, len(args)-1)
	for index := 1; index < len(args); index++ {
		name := args[index]
		if name == "--active-minutes" || name == "--idle-minutes" {
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return options, errors.New("interval requires a value")
			}
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 5 || value > 1440 {
				return options, errors.New("interval must be between 5 and 1440 minutes")
			}
			index++
			if name == "--active-minutes" {
				if options.activeMinutes != nil {
					return options, errors.New("active interval supplied twice")
				}
				options.activeMinutes = &value
			} else {
				if options.idleMinutes != nil {
					return options, errors.New("idle interval supplied twice")
				}
				options.idleMinutes = &value
			}
			continue
		}
		common = append(common, name)
	}
	selection, err := parseSelectionOptions(common)
	options.selectionOptions = selection
	return options, err
}

func writeCollectionSettingsUsage(output io.Writer) {
	io.WriteString(output, "Usage: codex-folio settings collection [--active-minutes N] [--idle-minutes N] [--state-root PATH] [--vault-mode MODE] [--json]\n")
}
