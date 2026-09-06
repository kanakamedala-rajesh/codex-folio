package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

func runConfigurationPack(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	if len(args) == 0 {
		return writeConfigurationPackUsage(stderr, "a configuration-pack action is required")
	}
	action := args[0]
	args = args[1:]
	var positional []string
	var options selectionOptions
	var fileSpecs []string
	reviewed := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			if options.json {
				return writeConfigurationPackUsage(stderr, "--json may be supplied only once")
			}
			options.json = true
		case arg == "--state-root":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return writeConfigurationPackUsage(stderr, "--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options.serviceOptions, args[index]); err != nil {
				return writeConfigurationPackUsage(stderr, err.Error())
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options.serviceOptions, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return writeConfigurationPackUsage(stderr, err.Error())
			}
		case arg == "--vault-mode":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return writeConfigurationPackUsage(stderr, "--vault-mode requires a value")
			}
			index++
			if err := setServiceVaultMode(&options.serviceOptions, args[index]); err != nil {
				return writeConfigurationPackUsage(stderr, err.Error())
			}
		case strings.HasPrefix(arg, "--vault-mode="):
			if err := setServiceVaultMode(&options.serviceOptions, strings.TrimPrefix(arg, "--vault-mode=")); err != nil {
				return writeConfigurationPackUsage(stderr, err.Error())
			}
		case arg == "--reviewed":
			if reviewed {
				return writeConfigurationPackUsage(stderr, "--reviewed may be supplied only once")
			}
			reviewed = true
		case arg == "--file":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return writeConfigurationPackUsage(stderr, "--file requires PATH=CONTENT or PATH=@FILE")
			}
			index++
			fileSpecs = append(fileSpecs, args[index])
		case strings.HasPrefix(arg, "--file="):
			fileSpecs = append(fileSpecs, strings.TrimPrefix(arg, "--file="))
		default:
			if strings.HasPrefix(arg, "--") {
				return writeConfigurationPackUsage(stderr, "unexpected configuration-pack argument")
			}
			positional = append(positional, arg)
		}
	}

	request := httpapi.CommandConfigurationPackRequest{Action: action, Reviewed: reviewed}
	switch action {
	case "create":
		if len(positional) != 2 || len(fileSpecs) == 0 {
			return writeConfigurationPackUsage(stderr, "create requires PACK_ID VERSION and at least one --file")
		}
		files, err := readConfigurationPackFiles(fileSpecs)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ConfigurationPackInvalid, err), nil)
		}
		request.PackID, request.Version, request.Files = positional[0], positional[1], files
	case "approve":
		if len(positional) != 2 || len(fileSpecs) != 0 || reviewed {
			return writeConfigurationPackUsage(stderr, "approve requires PACK_ID VERSION")
		}
		request.PackID, request.Version = positional[0], positional[1]
	case "assign":
		if len(positional) != 3 || len(fileSpecs) != 0 || reviewed {
			return writeConfigurationPackUsage(stderr, "assign requires ALIAS PACK_ID VERSION")
		}
		request.Alias, request.PackID, request.Version = positional[0], positional[1], positional[2]
	case "override":
		if len(positional) != 3 || len(fileSpecs) != 0 || reviewed {
			return writeConfigurationPackUsage(stderr, "override requires ALIAS PATH CONTENT or CONTENT=@FILE")
		}
		request.Alias, request.Path = positional[0], positional[1]
		content, err := readConfigurationPackContent(positional[2])
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ConfigurationPackInvalid, err), nil)
		}
		request.Content = content
	case "preview", "project":
		if len(positional) != 1 || len(fileSpecs) != 0 || reviewed {
			return writeConfigurationPackUsage(stderr, action+" requires ALIAS")
		}
		request.Alias = positional[0]
	case "promotion-preview":
		if len(positional) != 2 || len(fileSpecs) != 0 || reviewed {
			return writeConfigurationPackUsage(stderr, "promotion-preview requires ALIAS VERSION")
		}
		request.Alias, request.Version = positional[0], positional[1]
	case "promote":
		if len(positional) != 2 || len(fileSpecs) != 0 {
			return writeConfigurationPackUsage(stderr, "promote requires ALIAS VERSION --reviewed")
		}
		request.Alias, request.Version = positional[0], positional[1]
		if !reviewed {
			return writeConfigurationPackUsage(stderr, "promote requires explicit --reviewed")
		}
	default:
		return writeConfigurationPackUsage(stderr, "unknown configuration-pack action")
	}

	return withSelectionService(input, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		response, err := client.ConfigurationPack(context.Background(), request)
		if err != nil {
			return err
		}
		return writeConfigurationPackResponse(stdout, response, action, options.json)
	})
}

func readConfigurationPackFiles(specs []string) (map[string]string, error) {
	files := make(map[string]string, len(specs))
	for _, spec := range specs {
		path, content, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(path) == "" {
			return nil, errors.New("--file must use PATH=CONTENT or PATH=@FILE")
		}
		if _, exists := files[path]; exists {
			return nil, errors.New("a configuration-pack path was supplied more than once")
		}
		loaded, err := readConfigurationPackContent(content)
		if err != nil {
			return nil, err
		}
		files[path] = loaded
	}
	return files, nil
}

func readConfigurationPackContent(content string) (string, error) {
	if !strings.HasPrefix(content, "@") {
		return content, nil
	}
	if len(content) == 1 {
		return "", errors.New("content file path must not be empty")
	}
	encoded, err := os.ReadFile(strings.TrimPrefix(content, "@"))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func writeConfigurationPackResponse(stdout io.Writer, response httpapi.CommandConfigurationPackResponse, action string, jsonOutput bool) error {
	if jsonOutput {
		return writeServiceJSON(stdout, response)
	}
	switch action {
	case "create", "approve", "promote":
		if response.Pack == nil {
			return errors.New("configuration-pack response did not include a pack")
		}
		_, err := fmt.Fprintf(stdout, "configuration pack %s version %s (%s)\n", response.Pack.ID, response.Pack.Version, response.Pack.Digest)
		return err
	case "assign":
		if response.Assignment == nil {
			return errors.New("configuration-pack response did not include an assignment")
		}
		_, err := fmt.Fprintf(stdout, "assigned %s to %s\n", response.Assignment.PackID+"@"+response.Assignment.Version, response.Assignment.Alias)
		return err
	case "override":
		_, err := io.WriteString(stdout, "profile-local configuration override saved\n")
		return err
	case "preview":
		if response.Plan == nil {
			return errors.New("configuration-pack response did not include a projection plan")
		}
		_, err := fmt.Fprintf(stdout, "projection %s@%s (%s): %s\n", response.Plan.Assignment.PackID, response.Plan.Assignment.Version, response.Plan.Digest, strings.Join(response.Plan.Files, ", "))
		return err
	case "project":
		if response.Projection == nil {
			return errors.New("configuration-pack response did not include a projection result")
		}
		_, err := fmt.Fprintf(stdout, "projected %s@%s (%s)\n", response.Projection.PackID, response.Projection.Version, response.Projection.Digest)
		return err
	case "promotion-preview":
		if response.Promotion == nil {
			return errors.New("configuration-pack response did not include a promotion preview")
		}
		_, err := fmt.Fprintf(stdout, "promotion %s@%s -> %s (%s): %d change(s)\n", response.Promotion.PackID, response.Promotion.FromVersion, response.Promotion.ToVersion, response.Promotion.Digest, len(response.Promotion.Changes))
		return err
	default:
		return errors.New("unknown configuration-pack response")
	}
}

func writeConfigurationPackUsage(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", apperrors.CLIUsage, message)
	_, _ = io.WriteString(stderr, "Usage:\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack create PACK_ID VERSION --file PATH=CONTENT [--file PATH=@FILE] [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack approve PACK_ID VERSION [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack assign ALIAS PACK_ID VERSION [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack override ALIAS PATH CONTENT [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack {preview|project} ALIAS [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack promotion-preview ALIAS VERSION [--state-root PATH] [--vault-mode MODE] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio configuration-pack promote ALIAS VERSION --reviewed [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
