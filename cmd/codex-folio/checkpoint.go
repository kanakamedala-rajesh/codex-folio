package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	gitadapter "venkatasudha.com/codex-folio/internal/adapters/git"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

type checkpointOptions struct {
	selectionOptions
	nonInteractive bool
}

func runCheckpoint(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runCheckpointWithDependencies(args, os.Stdin, stdout, stderr, resolvePaths, func(path string) error {
		return editCheckpointFile(path, os.Stdin, stdout, stderr)
	})
}

func runCheckpointWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, editor func(string) error) int {
	request, options, err := parseCheckpointRequest(args)
	if err != nil {
		return writeCheckpointUsage(stderr, newServiceDiagnosticSink())
	}
	if request.Action == "review" && options.nonInteractive {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ContinuationCheckpointInvalid, errors.New("checkpoint review requires interactive approval")), newServiceDiagnosticSink())
	}
	return withSelectionService(input, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink(), options.selectionOptions, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		if request.Action == "review" {
			_, _, err := reviewCheckpoint(input, stdout, stderr, client, request, editor)
			return err
		}
		result, err := client.Checkpoint(context.Background(), request)
		if err != nil {
			return err
		}
		if request.Action == "retention" {
			if options.json {
				return writeServiceJSON(stdout, result.Retention)
			}
			fmt.Fprintf(stdout, "Repository-first: %s\nTranscript-assisted: %s\n", result.Retention.RepositoryFirst, result.Retention.TranscriptAssisted)
			return nil
		}
		if options.json {
			return writeServiceJSON(stdout, result.Checkpoint)
		}
		writeCheckpoint(stdout, result.Checkpoint)
		return nil
	})
}

func parseCheckpointRequest(args []string) (httpapi.CommandCheckpointRequest, checkpointOptions, error) {
	if len(args) == 0 || (args[0] != "capture" && args[0] != "show" && args[0] != "review" && args[0] != "retention") {
		return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("checkpoint action is required")
	}
	request := httpapi.CommandCheckpointRequest{Action: args[0]}
	values, seen, operands, common := map[string]string{}, map[string]bool{}, []string{}, []string{}
	nonInteractive := false
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if arg == "--json" {
			common = append(common, arg)
			continue
		}
		if arg == "--non-interactive" {
			if nonInteractive {
				return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("non-interactive may be supplied only once")
			}
			nonInteractive = true
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		if name == "--state-root" || name == "--vault-mode" {
			if !hasValue {
				if index+1 >= len(args) {
					return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("common option requires a value")
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
			return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("unknown checkpoint option")
		}
		if !hasValue {
			if index+1 >= len(args) {
				return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("checkpoint option requires a value")
			}
			index++
			value = args[index]
		}
		if value == "" || (seen[name] && name != "--project-command" && name != "--redact-path" && name != "--redact-text") {
			return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("checkpoint option is invalid")
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
		return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, err
	}
	resultOptions := checkpointOptions{selectionOptions: options, nonInteractive: nonInteractive}
	if request.Action == "retention" {
		if len(values) != 0 || len(request.RedactPaths) != 0 || len(request.RedactText) != 0 || len(request.ProjectCommands) != 0 || nonInteractive || (len(operands) != 0 && len(operands) != 2) {
			return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("retention accepts an origin and setting")
		}
		if len(operands) == 2 {
			request.Source, request.Setting = operands[0], operands[1]
			if _, _, err := continuation.ParseRetention(request.Setting); err != nil || (request.Source != continuation.SourceRepositoryFirst && request.Source != continuation.SourceTranscriptAssisted) {
				return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("checkpoint retention is invalid")
			}
		}
		return request, resultOptions, nil
	}
	if request.Action == "show" {
		if len(operands) != 1 || len(values) != 0 || len(request.RedactPaths) != 0 || len(request.RedactText) != 0 || len(request.ProjectCommands) != 0 || nonInteractive {
			return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("show requires one checkpoint ID")
		}
		request.ID = operands[0]
		return request, resultOptions, nil
	}
	if request.Action == "review" {
		if len(operands) != 1 || len(values) != 0 || len(request.ProjectCommands) != 0 || options.json {
			return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("review requires one checkpoint ID")
		}
		request.ID = operands[0]
		return request, resultOptions, nil
	}
	if nonInteractive {
		return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("capture is not interactive")
	}
	if len(operands) > 1 {
		return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, errors.New("capture accepts one repository path")
	}
	request.Path = "."
	if len(operands) == 1 {
		request.Path = operands[0]
	}
	request.Alias, request.Goal, request.CompletedWork = values["--alias"], values["--goal"], values["--completed-work"]
	request.PendingWork, request.Risks, request.NextAction = values["--pending-work"], values["--risks"], values["--next-action"]
	validation, err := parseValidation(values)
	if err != nil {
		return httpapi.CommandCheckpointRequest{}, checkpointOptions{}, err
	}
	request.Validation = validation
	return request, resultOptions, nil
}

func reviewCheckpoint(input io.Reader, stdout, stderr io.Writer, client *httpapi.CommandClient, request httpapi.CommandCheckpointRequest, editor func(string) error) (continuation.Checkpoint, bool, error) {
	shown, err := client.Checkpoint(context.Background(), httpapi.CommandCheckpointRequest{Action: "show", ID: request.ID})
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	writeCheckpoint(stdout, shown.Checkpoint)
	fields, err := editCheckpointFields(shown.Checkpoint.Fields, editor)
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	edited, err := client.Checkpoint(context.Background(), httpapi.CommandCheckpointRequest{Action: "edit", ID: request.ID, Fields: &fields, RedactPaths: request.RedactPaths, RedactText: request.RedactText})
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	io.WriteString(stdout, "Sanitized revision:\n")
	writeCheckpoint(stdout, edited.Checkpoint)
	io.WriteString(stderr, "Type 'approve' to approve this sanitized revision; anything else cancels: ")
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return continuation.Checkpoint{}, false, err
		}
		io.WriteString(stdout, "Checkpoint remains draft.\n")
		return edited.Checkpoint, false, nil
	}
	if strings.TrimSpace(scanner.Text()) != "approve" {
		io.WriteString(stdout, "Checkpoint remains draft.\n")
		return edited.Checkpoint, false, nil
	}
	approved, err := client.Checkpoint(context.Background(), httpapi.CommandCheckpointRequest{Action: "approve", ID: request.ID, Revision: edited.Checkpoint.Revision})
	if err != nil {
		return continuation.Checkpoint{}, false, err
	}
	io.WriteString(stdout, "Approved checkpoint:\n")
	writeCheckpoint(stdout, approved.Checkpoint)
	return approved.Checkpoint, true, nil
}

func editCheckpointFields(fields continuation.CheckpointFields, editor func(string) error) (continuation.CheckpointFields, error) {
	if editor == nil {
		return continuation.CheckpointFields{}, errors.New("checkpoint editor is unavailable")
	}
	file, err := os.CreateTemp("", "codex-folio-checkpoint-*.json")
	if err != nil {
		return continuation.CheckpointFields{}, err
	}
	path := file.Name()
	defer os.Remove(path)
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(fields); err != nil {
		file.Close()
		return continuation.CheckpointFields{}, err
	}
	if err := file.Close(); err != nil {
		return continuation.CheckpointFields{}, err
	}
	if err := editor(path); err != nil {
		return continuation.CheckpointFields{}, fmt.Errorf("checkpoint editor failed: %w", err)
	}
	file, err = os.Open(path)
	if err != nil {
		return continuation.CheckpointFields{}, err
	}
	defer file.Close()
	limited := io.LimitReader(file, 4*continuation.MaxCheckpointBytes+1)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var edited continuation.CheckpointFields
	if err := decoder.Decode(&edited); err != nil {
		return continuation.CheckpointFields{}, continuation.ErrCheckpointInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return continuation.CheckpointFields{}, continuation.ErrCheckpointInvalid
	}
	return edited, nil
}

func editCheckpointFile(path string, input io.Reader, stdout, stderr io.Writer) error {
	command, err := splitEditorCommand(os.Getenv("EDITOR"))
	if err != nil {
		return err
	}
	if len(command) == 0 {
		return errors.New("EDITOR is not set")
	}
	editor := foregroundCommand(command[0], append(command[1:], path)...)
	editor.Stdin, editor.Stdout, editor.Stderr = input, stdout, stderr
	return editor.Run()
}

func splitEditorCommand(value string) ([]string, error) {
	var arguments []string
	var current strings.Builder
	var quote rune
	token := false
	runes := []rune(strings.TrimSpace(value))
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		if quote == 0 && unicode.IsSpace(character) {
			if token {
				arguments = append(arguments, current.String())
				current.Reset()
				token = false
			}
			continue
		}
		if character == '\'' || character == '"' {
			if quote == 0 {
				quote, token = character, true
				continue
			}
			if quote == character {
				quote = 0
				continue
			}
		}
		if character == '\\' && index+1 < len(runes) {
			next := runes[index+1]
			if next == '\\' || next == quote || (quote == 0 && (next == '\'' || next == '"' || unicode.IsSpace(next))) {
				current.WriteRune(next)
				token = true
				index++
				continue
			}
		}
		current.WriteRune(character)
		token = true
	}
	if quote != 0 {
		return nil, errors.New("EDITOR has an unmatched quote")
	}
	if token {
		arguments = append(arguments, current.String())
	}
	return arguments, nil
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
	expires := "unlimited"
	if checkpoint.ExpiresAt != nil {
		expires = checkpoint.ExpiresAt.Format(time.RFC3339)
	}
	fmt.Fprintf(output, "Created: %s; retention: %s; expires: %s\n", checkpoint.CreatedAt.Format(time.RFC3339), checkpoint.Retention, expires)
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
	result := make([]string, 0, len(values))
	for _, value := range values {
		entry := value.Command + " (source " + value.Source + ", freshness " + value.Freshness
		if value.Timestamp == nil {
			entry += ", at unknown"
		} else {
			entry += ", at " + value.Timestamp.Format(time.RFC3339)
		}
		if value.ExitStatus == nil {
			entry += ", exit unknown"
		} else {
			entry += fmt.Sprintf(", exit %d", *value.ExitStatus)
		}
		result = append(result, entry+")")
	}
	return strings.Join(result, "; ")
}

func newCheckpointService(stateStore *store.Store, projects continuation.Projects) (*continuation.Service, error) {
	home, _ := os.UserHomeDir()
	return continuation.NewService(continuation.ServiceOptions{Repository: stateStore, Projects: projects, Inspector: gitadapter.NewInspector(), HomeDirectory: home})
}

func writeCheckpointUsage(stderr io.Writer, sink diagnostics.Sink) int {
	recordServiceDiagnostic(sink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: invalid checkpoint arguments\n", apperrors.CLIUsage)
	io.WriteString(stderr, "Usage: codex-folio checkpoint {retention [repository-first|transcript-assisted DAYS|unlimited]|capture [PATH] [--goal TEXT] [--completed-work TEXT] [--pending-work TEXT] [--validation-command COMMAND] [--validation-at RFC3339] [--validation-exit STATUS] [--validation-source SOURCE] [--validation-freshness fresh|stale|unknown] [--risks TEXT] [--next-action TEXT] [--project-command DESCRIPTION] [--redact-path PATH] [--redact-text TEXT]|show ID|review ID [--redact-path PATH] [--redact-text TEXT] [--non-interactive]} [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
