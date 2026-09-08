package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

func runProject(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runProjectWithDependencies(args, os.Stdin, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink())
}

func runProjectWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink) int {
	request, options, err := parseProjectRequest(args)
	if err != nil {
		return writeProjectUsage(stderr, diagnosticSink)
	}
	return withSelectionService(input, stderr, resolvePaths, openStore, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		result, err := client.Project(context.Background(), request)
		if err != nil {
			return err
		}
		if options.json {
			if request.Action == "list" {
				return writeServiceJSON(stdout, result.Projects)
			}
			if result.Project == nil {
				return apperrors.New(apperrors.ProjectIdentityInvalid, activity.ErrProjectInvalid)
			}
			return writeServiceJSON(stdout, result.Project)
		}
		if request.Action == "list" {
			for _, project := range result.Projects {
				writeProject(stdout, project)
			}
			return nil
		}
		if result.Project == nil {
			return apperrors.New(apperrors.ProjectIdentityInvalid, activity.ErrProjectInvalid)
		}
		writeProject(stdout, *result.Project)
		return nil
	})
}

func parseProjectRequest(args []string) (httpapi.CommandProjectRequest, selectionOptions, error) {
	if len(args) == 0 {
		return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("project action is required")
	}
	request := httpapi.CommandProjectRequest{Action: args[0]}
	operands := []string{}
	common := []string{}
	for index := 1; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--alias":
			if request.Alias != "" || index+1 >= len(args) {
				return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("--alias requires one value")
			}
			index++
			request.Alias = args[index]
		case strings.HasPrefix(arg, "--alias="):
			if request.Alias != "" {
				return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("--alias may be supplied once")
			}
			request.Alias = strings.TrimPrefix(arg, "--alias=")
		case arg == "--state-root" || arg == "--vault-mode":
			if index+1 >= len(args) {
				return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("common option requires a value")
			}
			common = append(common, arg, args[index+1])
			index++
		case arg == "--json" || strings.HasPrefix(arg, "--state-root=") || strings.HasPrefix(arg, "--vault-mode="):
			common = append(common, arg)
		case strings.HasPrefix(arg, "--"):
			return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("unknown project option")
		default:
			operands = append(operands, arg)
		}
	}
	options, err := parseSelectionOptions(common)
	if err != nil {
		return httpapi.CommandProjectRequest{}, selectionOptions{}, err
	}
	switch request.Action {
	case "resolve":
		if len(operands) != 1 {
			return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("resolve requires a path")
		}
		request.Path = operands[0]
	case "list":
		if len(operands) != 0 || request.Alias != "" {
			return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("list accepts no project fields")
		}
	case "edit":
		if len(operands) != 1 || strings.TrimSpace(request.Alias) == "" {
			return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("edit requires an ID and alias")
		}
		request.ID = operands[0]
	case "reconcile":
		if len(operands) != 2 || request.Alias != "" {
			return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("reconcile requires an ID and path")
		}
		request.ID, request.Path = operands[0], operands[1]
	default:
		return httpapi.CommandProjectRequest{}, selectionOptions{}, errors.New("unknown project action")
	}
	return request, options, nil
}

func writeProject(output io.Writer, project activity.ProjectIdentity) {
	fmt.Fprintf(output, "%s (%s) %s\n", project.Alias, project.Basename, project.ID)
}

func writeProjectUsage(stderr io.Writer, diagnosticSink diagnostics.Sink) int {
	recordServiceDiagnostic(diagnosticSink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: invalid project arguments\n", apperrors.CLIUsage)
	io.WriteString(stderr, "Usage: codex-folio project {resolve PATH [--alias NAME]|list|edit ID --alias NAME|reconcile ID PATH} [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
