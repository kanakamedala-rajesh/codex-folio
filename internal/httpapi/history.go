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
	"slices"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (client *CommandClient) History(ctx context.Context, input HistoryRequest) (HistoryResponse, error) {
	var result HistoryResponse
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandHistoryPath, bytes.NewReader(body))
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
	if response.StatusCode != http.StatusOK {
		var failure UsageErrorResponse
		if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
			return result, err
		}
		return result, apperrors.New(failure.Code, fmt.Errorf("analytics request returned HTTP %d", response.StatusCode))
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}

func (server *Server) history(response http.ResponseWriter, request *http.Request) {
	if server.historyService == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.AnalyticsRequestInvalid)
		return
	}
	var input HistoryRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.AnalyticsRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.AnalyticsRequestInvalid)
		return
	}
	var result struct {
		Retention  *usage.RetentionResult    `json:"retention,omitempty"`
		Purge      *usage.PurgeResult        `json:"purge,omitempty"`
		Aggregates *[]usage.HistoryAggregate `json:"aggregates,omitempty"`
	}
	switch input.Action {
	case "retention":
		if input.Scope != nil || input.Confirmation != nil || (input.Setting != nil && *input.Setting == "") {
			err = usage.ErrInvalid
			break
		}
		setting := ""
		if input.Setting != nil {
			setting = *input.Setting
		}
		value, requestErr := server.historyService.Retention(request.Context(), setting, input.Run != nil && *input.Run)
		result.Retention, err = &value, requestErr
	case "purge", "aggregates":
		if input.Scope == nil || input.Setting != nil || input.Run != nil || (input.Confirmation != nil && (input.Action != "purge" || *input.Confirmation == "")) {
			err = usage.ErrInvalid
			break
		}
		scope := usage.HistoryScope{ProfileID: input.Scope.ProfileId, ProjectID: input.Scope.ProjectId, From: input.Scope.From, To: input.Scope.To, Classes: input.Scope.Classes}
		if input.Action == "purge" {
			confirmation := ""
			if input.Confirmation != nil {
				confirmation = *input.Confirmation
			}
			value, requestErr := server.historyService.Purge(request.Context(), scope, confirmation)
			result.Purge, err = &value, requestErr
		} else {
			if !slices.Equal(scope.Classes, []string{"aggregates"}) {
				err = usage.ErrInvalid
				break
			}
			value, requestErr := server.historyService.Aggregates(request.Context(), scope)
			result.Aggregates, err = &value, requestErr
		}
	default:
		err = usage.ErrInvalid
	}
	if err != nil {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.AnalyticsRequestInvalid))
		return
	}
	writeJSON(response, http.StatusOK, result)
}
