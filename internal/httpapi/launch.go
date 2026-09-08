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
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/launch"
)

const maxLaunchBodySize = 1024 * 1024

type CommandLaunchService interface {
	Prepare(context.Context, launch.PrepareRequest, string) (launch.Plan, string, error)
	MarkStarted(context.Context, string, int) error
	MarkExited(context.Context, string, int, string, string) error
	MarkAbandoned(context.Context, string) error
}

type CommandLaunchRequest struct {
	Action           string   `json:"action"`
	Alias            string   `json:"alias,omitempty"`
	Executable       string   `json:"executable,omitempty"`
	Version          string   `json:"version,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	Arguments        []string `json:"arguments,omitempty"`
	LeaseID          string   `json:"lease_id,omitempty"`
	ProcessID        int      `json:"process_id,omitempty"`
	ExitStatus       int      `json:"exit_status,omitempty"`
}

type CommandLaunchResponse struct {
	Plan    *launch.Plan `json:"plan,omitempty"`
	Warning string       `json:"warning,omitempty"`
}

func (client *CommandClient) Launch(ctx context.Context, input CommandLaunchRequest) (CommandLaunchResponse, error) {
	var result CommandLaunchResponse
	encoded, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandLaunchPath, bytes.NewReader(encoded))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandLaunchPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandLaunchPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (server *Server) commandLaunch(response http.ResponseWriter, request *http.Request) {
	if server.launches == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxLaunchBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.LaunchPlanInvalid)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxLaunchBodySize))
	decoder.DisallowUnknownFields()
	var input CommandLaunchRequest
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.LaunchPlanInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.LaunchPlanInvalid)
		return
	}

	var result CommandLaunchResponse
	switch strings.TrimSpace(input.Action) {
	case "prepare":
		plan, warning, actionErr := server.launches.Prepare(request.Context(), launch.PrepareRequest{
			Alias: input.Alias, Executable: input.Executable, WorkingDirectory: input.WorkingDirectory, Arguments: input.Arguments,
		}, input.Version)
		err = actionErr
		if err == nil {
			result.Plan = &plan
			result.Warning = warning
		}
	case "started":
		err = server.launches.MarkStarted(request.Context(), input.LeaseID, input.ProcessID)
	case "exited":
		err = server.launches.MarkExited(request.Context(), input.LeaseID, input.ExitStatus, input.Executable, input.Version)
	case "abandoned":
		err = server.launches.MarkAbandoned(request.Context(), input.LeaseID)
	default:
		err = apperrors.New(apperrors.LaunchPlanInvalid, launch.ErrPlanInvalid)
	}
	if err != nil {
		status := http.StatusConflict
		if code := apperrors.Code(err); code == apperrors.StoreReadFailed || code == apperrors.StoreWriteFailed {
			status = http.StatusInternalServerError
		}
		server.writeAPIError(response, status, diagnostics.CodeFor(err, apperrors.LaunchPlanInvalid))
		return
	}
	writeJSON(response, http.StatusOK, result)
}
