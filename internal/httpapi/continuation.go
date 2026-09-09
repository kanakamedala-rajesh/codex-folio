package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type CommandCheckpointService interface {
	Capture(context.Context, continuation.CaptureRequest) (continuation.Checkpoint, error)
	Show(context.Context, string) (continuation.Checkpoint, error)
	Edit(context.Context, string, continuation.EditRequest) (continuation.Checkpoint, error)
	Approve(context.Context, string, string) (continuation.Checkpoint, error)
}

type CommandCheckpointRequest struct {
	Action          string                           `json:"action"`
	ID              string                           `json:"id,omitempty"`
	Path            string                           `json:"path,omitempty"`
	Alias           string                           `json:"alias,omitempty"`
	Goal            string                           `json:"goal,omitempty"`
	CompletedWork   string                           `json:"completed_work,omitempty"`
	PendingWork     string                           `json:"pending_work,omitempty"`
	Validation      *continuation.ValidationEvidence `json:"validation,omitempty"`
	Risks           string                           `json:"risks,omitempty"`
	NextAction      string                           `json:"next_action,omitempty"`
	ProjectCommands []string                         `json:"project_commands,omitempty"`
	RedactPaths     []string                         `json:"redact_paths,omitempty"`
	RedactText      []string                         `json:"redact_text,omitempty"`
	Fields          *continuation.CheckpointFields   `json:"fields,omitempty"`
	Revision        string                           `json:"revision,omitempty"`
}

type CommandCheckpointResponse struct {
	Checkpoint continuation.Checkpoint `json:"checkpoint"`
}

func (client *CommandClient) Checkpoint(ctx context.Context, input CommandCheckpointRequest) (CommandCheckpointResponse, error) {
	var result CommandCheckpointResponse
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandCheckpointPath, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandCheckpointPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandCheckpointPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (server *Server) commandCheckpoint(response http.ResponseWriter, request *http.Request) {
	if server.checkpoints == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxCheckpointBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
	var input CommandCheckpointRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxCheckpointBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
	var checkpoint continuation.Checkpoint
	switch input.Action {
	case "capture":
		if input.ID != "" || input.Fields != nil || input.Revision != "" {
			err = continuation.ErrCheckpointInvalid
		} else {
			checkpoint, err = server.checkpoints.Capture(request.Context(), continuation.CaptureRequest{
				Path: input.Path, Alias: input.Alias, Goal: input.Goal, CompletedWork: input.CompletedWork,
				PendingWork: input.PendingWork, Validation: input.Validation, Risks: input.Risks,
				NextAction: input.NextAction, ConfiguredCommands: input.ProjectCommands, RedactPaths: input.RedactPaths, RedactText: input.RedactText,
			})
		}
	case "show":
		if input.Path != "" || input.Alias != "" || input.Goal != "" || input.CompletedWork != "" || input.PendingWork != "" || input.Validation != nil || input.Risks != "" || input.NextAction != "" || len(input.ProjectCommands) != 0 || len(input.RedactPaths) != 0 || len(input.RedactText) != 0 || input.Fields != nil || input.Revision != "" {
			err = continuation.ErrCheckpointInvalid
		} else {
			checkpoint, err = server.checkpoints.Show(request.Context(), input.ID)
		}
	case "edit":
		if input.ID == "" || input.Fields == nil || input.Path != "" || input.Alias != "" || input.Goal != "" || input.CompletedWork != "" || input.PendingWork != "" || input.Validation != nil || input.Risks != "" || input.NextAction != "" || len(input.ProjectCommands) != 0 || input.Revision != "" {
			err = continuation.ErrCheckpointInvalid
		} else {
			checkpoint, err = server.checkpoints.Edit(request.Context(), input.ID, continuation.EditRequest{Fields: *input.Fields, RedactPaths: input.RedactPaths, RedactText: input.RedactText})
		}
	case "approve":
		if input.ID == "" || input.Revision == "" || input.Path != "" || input.Alias != "" || input.Goal != "" || input.CompletedWork != "" || input.PendingWork != "" || input.Validation != nil || input.Risks != "" || input.NextAction != "" || len(input.ProjectCommands) != 0 || len(input.RedactPaths) != 0 || len(input.RedactText) != 0 || input.Fields != nil {
			err = continuation.ErrCheckpointInvalid
		} else {
			checkpoint, err = server.checkpoints.Approve(request.Context(), input.ID, input.Revision)
		}
	default:
		err = continuation.ErrCheckpointInvalid
	}
	if err != nil {
		status, code := checkpointError(err)
		server.writeAPIError(response, status, diagnostics.CodeFor(err, code))
		return
	}
	writeJSON(response, http.StatusOK, CommandCheckpointResponse{Checkpoint: checkpoint})
}

func checkpointError(err error) (int, string) {
	switch {
	case errors.Is(err, continuation.ErrCheckpointNotFound):
		return http.StatusNotFound, apperrors.ContinuationCheckpointNotFound
	case errors.Is(err, continuation.ErrCheckpointOversize):
		return http.StatusConflict, apperrors.ContinuationCheckpointOversize
	case errors.Is(err, continuation.ErrCheckpointRevisionChanged):
		return http.StatusConflict, apperrors.ContinuationCheckpointInvalid
	case errors.Is(err, continuation.ErrHandoffNotReady):
		return http.StatusConflict, apperrors.ContinuationCheckpointInvalid
	case errors.Is(err, continuation.ErrRepositoryInspection):
		return http.StatusConflict, apperrors.ContinuationRepositoryInspectionFailed
	case errors.Is(err, continuation.ErrCheckpointInvalid):
		return http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid
	default:
		return http.StatusInternalServerError, apperrors.StoreWriteFailed
	}
}
