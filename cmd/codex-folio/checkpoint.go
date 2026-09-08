package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	gitadapter "venkatasudha.com/codex-folio/internal/adapters/git"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func runCheckpoint(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	request, options, err := parseCheckpointRequest(args)
	if err != nil {
		return writeCheckpointUsage(stderr, newServiceDiagnosticSink())
	}
	return withSelectionService(os.Stdin, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		result, err := client.Checkpoint(context.Background(), request)
		if err != nil {
			return err
		}
		if options.json {
			return writeServiceJSON(stdout, result.Checkpoint)
		}
		writeCheckpoint(stdout, result.Checkpoint)
		return nil
	})
}

func parseCheckpointRequest(args []string) (httpapi.CommandCheckpointRequest, selectionOptions, error) {
	if len(args) == 0 || (args[0] != "capture" && args[0] != "show") {
		return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("checkpoint action is required")
	}
	request := httpapi.CommandCheckpointRequest{Action: args[0]}
	values, seen, operands, common := map[string]string{}, map[string]bool{}, []string{}, []string{}
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if arg == "--json" {
			common = append(common, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if name == "--state-root" || name == "--vault-mode" {
			if !hasValue {
				if index+1 >= len(args) {
					return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("common option requires a value")
				}
				index++
				common = append(common, name, args[index])
			} else {
				common = append(common, arg)
			}
			continue
		}
		if !strings.HasPrefix(name, "--") {
			operands = append(operands, arg)
			continue
		}
		if !checkpointOption(name) {
			return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("unknown checkpoint option")
		}
		if !hasValue {
			if index+1 >= len(args) {
				return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("checkpoint option requires a value")
			}
			index++
			value = args[index]
		}
		if value == "" || (seen[name] && name != "--project-command" && name != "--redact-path" && name != "--redact-text") {
			return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("checkpoint option is invalid")
		}
		seen[name] = true
		if name == "--redact-path" {
			request.RedactPaths = append(request.RedactPaths, value)
		} else if name == "--redact-text" {
			request.RedactText = append(request.RedactText, value)
		} else if name == "--project-command" {
			request.ProjectCommands = append(request.ProjectCommands, value)
		} else {
			values[name] = value
		}
	}
	options, err := parseSelectionOptions(common)
	if err != nil {
		return httpapi.CommandCheckpointRequest{}, selectionOptions{}, err
	}
	if request.Action == "show" {
		if len(operands) != 1 || len(values) != 0 || len(request.RedactPaths) != 0 || len(request.RedactText) != 0 || len(request.ProjectCommands) != 0 {
			return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("show requires one checkpoint ID")
		}
		request.ID = operands[0]
		return request, options, nil
	}
	if len(operands) > 1 {
		return httpapi.CommandCheckpointRequest{}, selectionOptions{}, errors.New("capture accepts one repository path")
	}
	request.Path = "."
	if len(operands) == 1 {
		request.Path = operands[0]
	}
	request.Alias, request.Goal, request.CompletedWork = values["--alias"], values["--goal"], values["--completed-work"]
	request.PendingWork, request.Risks, request.NextAction = values["--pending-work"], values["--risks"], values["--next-action"]
	validation, err := parseValidation(values)
	if err != nil {
		return httpapi.CommandCheckpointRequest{}, selectionOptions{}, err
	}
	request.Validation = validation
	return request, options, nil
}

func checkpointOption(name string) bool {
	switch name {
	case "--alias", "--goal", "--completed-work", "--pending-work", "--risks", "--next-action", "--validation-command", "--validation-at", "--validation-exit", "--validation-source", "--validation-freshness", "--project-command", "--redact-path", "--redact-text":
		return true
	default:
		return false
	}
}

func parseValidation(values map[string]string) (*continuation.ValidationEvidence, error) {
	command := values["--validation-command"]
	if command == "" {
		for name := range values {
			if strings.HasPrefix(name, "--validation-") {
				return nil, errors.New("validation metadata requires a command")
			}
		}
		return nil, nil
	}
	result := &continuation.ValidationEvidence{Command: command, Source: values["--validation-source"], Freshness: values["--validation-freshness"]}
	if result.Source == "" {
		result.Source = continuation.ProvenanceUnknown
	}
	if result.Freshness == "" {
		result.Freshness = continuation.FreshnessUnknown
	}
	if result.Freshness != continuation.FreshnessFresh && result.Freshness != continuation.FreshnessStale && result.Freshness != continuation.FreshnessUnknown {
		return nil, errors.New("validation freshness is invalid")
	}
	if value := values["--validation-at"]; value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, err
		}
		result.Timestamp = &parsed
	}
	if value := values["--validation-exit"]; value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		result.ExitStatus = &parsed
	}
	return result, nil
}

func writeCheckpoint(output io.Writer, checkpoint continuation.Checkpoint) {
	fmt.Fprintf(output, "Checkpoint: %s (%s, %s, %d bytes)\n", checkpoint.ID, checkpoint.Status, checkpoint.Source, checkpoint.SizeBytes)
	fmt.Fprintf(output, "Project: %s (%s)\n", checkpoint.Project.Alias, checkpoint.Project.Basename)
	fmt.Fprintf(output, "Repository: branch=%s head=%s upstream=%s\n", evidenceValue(checkpoint.Repository.Branch), evidenceValue(checkpoint.Repository.Head), upstreamValue(checkpoint.Repository.Upstream))
	fmt.Fprintf(output, "Changes: staged=%s modified=%s untracked=%s; %d files, +%d/-%d, %d binary\n", strings.Join(checkpoint.Repository.Staged.Value, ", "), strings.Join(checkpoint.Repository.Modified.Value, ", "), strings.Join(checkpoint.Repository.Untracked.Value, ", "), checkpoint.Repository.Diff.Value.FilesChanged, checkpoint.Repository.Diff.Value.Insertions, checkpoint.Repository.Diff.Value.Deletions, checkpoint.Repository.Diff.Value.BinaryFiles)
	fmt.Fprintf(output, "Project commands [%s/%s]: %s\n", checkpoint.Repository.ConfiguredCommands.Provenance, checkpoint.Repository.ConfiguredCommands.Completeness, strings.Join(checkpoint.Repository.ConfiguredCommands.Value, ", "))
	fmt.Fprintf(output, "Goal [%s/%s]: %s\n", checkpoint.Fields.Goal.Provenance, checkpoint.Fields.Goal.Completeness, checkpoint.Fields.Goal.Value)
	fmt.Fprintf(output, "Completed work [%s/%s]: %s\n", checkpoint.Fields.CompletedWork.Provenance, checkpoint.Fields.CompletedWork.Completeness, checkpoint.Fields.CompletedWork.Value)
	fmt.Fprintf(output, "Pending work [%s/%s]: %s\n", checkpoint.Fields.PendingWork.Provenance, checkpoint.Fields.PendingWork.Completeness, checkpoint.Fields.PendingWork.Value)
	fmt.Fprintf(output, "Known validation [%s/%s]: %s\n", checkpoint.Fields.Validation.Provenance, checkpoint.Fields.Validation.Completeness, validationValue(checkpoint.Fields.Validation.Value))
	fmt.Fprintf(output, "Risks [%s/%s]: %s\n", checkpoint.Fields.Risks.Provenance, checkpoint.Fields.Risks.Completeness, checkpoint.Fields.Risks.Value)
	fmt.Fprintf(output, "Next action [%s/%s]: %s\n", checkpoint.Fields.NextAction.Provenance, checkpoint.Fields.NextAction.Completeness, checkpoint.Fields.NextAction.Value)
	fmt.Fprintf(output, "Created: %s; expires: %s\n", checkpoint.CreatedAt.Format(time.RFC3339), checkpoint.ExpiresAt.Format(time.RFC3339))
}

func evidenceValue(value continuation.Evidence[string]) string {
	if value.Value == "" {
		return "unknown"
	}
	return value.Value
}
func upstreamValue(value continuation.Evidence[*continuation.UpstreamDivergence]) string {
	if value.Value == nil {
		return "unknown"
	}
	return fmt.Sprintf("ahead %d, behind %d", value.Value.Ahead, value.Value.Behind)
}
func validationValue(values []continuation.ValidationEvidence) string {
	if len(values) == 0 {
		return "unknown"
	}
	value := values[0]
	result := value.Command + " (source " + value.Source + ", freshness " + value.Freshness
	if value.Timestamp == nil {
		result += ", at unknown"
	} else {
		result += ", at " + value.Timestamp.Format(time.RFC3339)
	}
	if value.ExitStatus == nil {
		result += ", exit unknown"
	} else {
		result += fmt.Sprintf(", exit %d", *value.ExitStatus)
	}
	return result + ")"
}

func newCheckpointService(stateStore *store.Store, projects continuation.Projects) (*continuation.Service, error) {
	home, _ := os.UserHomeDir()
	return continuation.NewService(continuation.ServiceOptions{Repository: stateStore, Projects: projects, Inspector: gitadapter.NewInspector(), HomeDirectory: home})
}

func writeCheckpointUsage(stderr io.Writer, sink diagnostics.Sink) int {
	recordServiceDiagnostic(sink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: invalid checkpoint arguments\n", apperrors.CLIUsage)
	io.WriteString(stderr, "Usage: codex-folio checkpoint {capture [PATH] [--goal TEXT] [--completed-work TEXT] [--pending-work TEXT] [--validation-command COMMAND] [--validation-at RFC3339] [--validation-exit STATUS] [--validation-source SOURCE] [--validation-freshness fresh|stale|unknown] [--risks TEXT] [--next-action TEXT] [--project-command DESCRIPTION] [--redact-path PATH] [--redact-text TEXT]|show ID} [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
