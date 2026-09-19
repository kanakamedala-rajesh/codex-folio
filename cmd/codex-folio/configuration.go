package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configbundle"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type configurationOptions struct {
	selectionOptions
	action, path, digest                          string
	apply, reviewed, write, includeProjectAliases bool
	resolutions                                   map[string]string
}

func runConfiguration(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	options, err := parseConfigurationOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: invalid configuration arguments\n", apperrors.CLIUsage)
		writeConfigurationUsage(stderr)
		return exitUsage
	}
	return withSelectionService(strings.NewReader(""), stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		if options.action == "export" {
			response, err := client.ConfigurationExportPreview(context.Background(), options.includeProjectAliases)
			if err != nil {
				return err
			}
			if response.Preview == nil || response.Preview.Bundle == nil {
				return errors.New("configuration export preview is incomplete")
			}
			if options.json {
				if err = writeServiceJSON(stdout, response); err != nil {
					return err
				}
			} else {
				writeConfigurationPreview(stdout, *response.Preview)
			}
			if !options.write {
				return nil
			}
			if options.digest != response.Preview.ConfirmationDigest {
				return configbundle.ErrReviewRequired
			}
			encoded, err := configbundle.CanonicalJSON(*response.Preview.Bundle)
			if err != nil {
				return err
			}
			return writeExclusiveConfiguration(options.path, append(encoded, '\n'))
		}
		file, err := os.Open(options.path)
		if err != nil {
			return err
		}
		source, err := io.ReadAll(io.LimitReader(file, configbundle.MaxBundleBytes+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(source) > configbundle.MaxBundleBytes {
			return configbundle.ErrInvalid
		}
		bundle, err := configbundle.Decode(source)
		if err != nil {
			return err
		}
		request := httpapi.ConfigurationBundleRequest{Action: "import_preview", Bundle: &bundle}
		if options.apply {
			request.Action = "import_apply"
			request.ConfirmationDigest = options.digest
			request.Reviewed = options.reviewed
			request.Resolutions = options.resolutions
		}
		response, err := client.ConfigurationImport(context.Background(), request)
		if err != nil {
			return err
		}
		if options.json {
			return writeServiceJSON(stdout, response)
		}
		if response.Preview != nil {
			writeConfigurationPreview(stdout, *response.Preview)
		} else if response.Result != nil {
			fmt.Fprintf(stdout, "Portable configuration applied. Profiles: %d, packs: %d, thresholds: %d, preferences: %d\n", response.Result.Counts.Profiles, response.Result.Counts.ConfigurationPacks, response.Result.Counts.AlertThresholds, response.Result.Counts.OperationalPreferences)
		}
		return nil
	})
}
func parseConfigurationOptions(args []string) (configurationOptions, error) {
	var o configurationOptions
	if len(args) == 0 || (args[0] != "export" && args[0] != "import") {
		return o, errors.New("action required")
	}
	o.action = args[0]
	o.resolutions = map[string]string{}
	common := []string{}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--output", "--input", "--confirmation-digest", "--resolve":
			if i+1 >= len(args) {
				return o, errors.New("value required")
			}
			value := args[i+1]
			i++
			switch args[i-1] {
			case "--output", "--input":
				if o.path != "" {
					return o, errors.New("path repeated")
				}
				o.path = value
			case "--confirmation-digest":
				o.digest = value
			case "--resolve":
				parts := strings.SplitN(value, "=", 2)
				if len(parts) != 2 || (parts[1] != configbundle.ResolutionSkip && parts[1] != configbundle.ResolutionKeepLocal && parts[1] != configbundle.ResolutionUseImported) {
					return o, errors.New("resolution invalid")
				}
				o.resolutions[parts[0]] = parts[1]
			}
		case "--apply":
			o.apply = true
		case "--reviewed":
			o.reviewed = true
		case "--write":
			o.write = true
		case "--include-project-aliases":
			o.includeProjectAliases = true
		default:
			common = append(common, args[i])
		}
	}
	if o.action == "export" && (o.apply || o.reviewed || len(o.resolutions) > 0 || o.write && (o.path == "" || len(o.digest) != 64) || !o.write && (o.path != "" || o.digest != "")) || o.action == "import" && (o.path == "" || o.write || o.includeProjectAliases || !o.apply && (o.reviewed || o.digest != "" || len(o.resolutions) > 0) || o.apply && (!o.reviewed || len(o.digest) != 64)) {
		return o, errors.New("invalid review arguments")
	}
	selection, err := parseSelectionOptions(common)
	if err != nil {
		return o, err
	}
	o.selectionOptions = selection
	return o, nil
}
func writeConfigurationPreview(out io.Writer, p configbundle.Preview) {
	fmt.Fprintf(out, "Configuration %s preview\nSchema: v%d\nProfiles: %d\nApproved pack versions: %d\nAlert thresholds: %d\nProject aliases: %d\nOperational preferences: %d\nConflicts: %d\nConfirmation digest: %s\nExcluded: %s\n", p.Direction, p.SchemaVersion, p.Counts.Profiles, p.Counts.ConfigurationPacks, p.Counts.AlertThresholds, p.Counts.ProjectAliases, p.Counts.OperationalPreferences, len(p.Conflicts), p.ConfirmationDigest, strings.Join(p.ExcludedFields, ", "))
	for _, c := range p.Conflicts {
		fmt.Fprintf(out, "Conflict %s (%s): %s Resolve with %s\n", c.Key, c.Kind, c.Detail, strings.Join(c.Resolutions, " or "))
	}
	if p.Bundle != nil {
		encoded, err := json.MarshalIndent(p.Bundle, "", "  ")
		if err == nil {
			fmt.Fprintf(out, "Included records:\n%s\n", encoded)
		}
	}
}
func writeConfigurationUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: codex-folio configuration export [--include-project-aliases] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(out, "       codex-folio configuration export --write --output FILE --confirmation-digest DIGEST [--include-project-aliases] ...")
	fmt.Fprintln(out, "       codex-folio configuration import --input FILE [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(out, "       codex-folio configuration import --input FILE --apply --reviewed --confirmation-digest DIGEST [--resolve KEY=skip|keep_local|use_imported] ...")
}

func writeExclusiveConfiguration(path string, content []byte) error {
	path = filepath.Clean(path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err = file.Write(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
