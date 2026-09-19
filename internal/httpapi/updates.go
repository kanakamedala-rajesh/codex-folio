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
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/updates"
)

func (server *Server) updatesHandler(response http.ResponseWriter, request *http.Request) {
	if server.updates == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		if len(request.URL.Query()) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UpdateRequestInvalid)
			return
		}
		snapshot, err := server.updates.Status(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		writeJSON(response, http.StatusOK, updateResponse(snapshot))
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UpdateRequestInvalid)
		return
	}
	var input UpdateRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UpdateRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UpdateRequestInvalid)
		return
	}

	var snapshot updates.Snapshot
	switch input.Action {
	case "configure":
		if input.AutomaticChecks == nil {
			err = apperrors.New(apperrors.UpdateRequestInvalid, errors.New("automatic_checks is required"))
			break
		}
		snapshot, err = server.updates.SetAutomatic(request.Context(), *input.AutomaticChecks)
	case "check":
		if input.AutomaticChecks != nil {
			err = apperrors.New(apperrors.UpdateRequestInvalid, errors.New("an explicit check does not change automatic consent"))
			break
		}
		snapshot, err = server.updates.Check(request.Context())
	default:
		err = apperrors.New(apperrors.UpdateRequestInvalid, errors.New("update action is invalid"))
	}
	if err != nil {
		code := diagnostics.CodeFor(err, apperrors.UpdateRequestInvalid)
		status := http.StatusInternalServerError
		if code == apperrors.UpdateRequestInvalid {
			status = http.StatusBadRequest
		}
		server.writeAPIError(response, status, code)
		return
	}
	writeJSON(response, http.StatusOK, updateResponse(snapshot))
}

func updateResponse(snapshot updates.Snapshot) UpdateResponse {
	return UpdateResponse{
		AutomaticChecks: snapshot.Settings.AutomaticChecks,
		Status:          string(snapshot.State.Status), CurrentVersion: snapshot.State.CurrentVersion,
		AvailableVersion: snapshot.State.AvailableVersion, ReleaseNotes: snapshot.State.ReleaseNotes,
		DownloadUrl: snapshot.State.DownloadURL, InstallerGuidance: snapshot.State.InstallerGuidance,
		CheckedAt: formatOptionalTime(snapshot.State.CheckedAt), NextCheckAt: formatOptionalTime(snapshot.State.NextCheckAt),
		ErrorCode: snapshot.State.ErrorCode,
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (client *CommandClient) UpdatesStatus(ctx context.Context) (UpdateResponse, error) {
	return client.updates(ctx, http.MethodGet, nil)
}

func (client *CommandClient) ManageUpdates(ctx context.Context, input UpdateRequest) (UpdateResponse, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return UpdateResponse{}, err
	}
	return client.updates(ctx, http.MethodPost, body)
}

func (client *CommandClient) updates(ctx context.Context, method string, body []byte) (UpdateResponse, error) {
	var result UpdateResponse
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandUpdatesPath, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var failure UsageErrorResponse
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandUpdatesPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandUpdatesPath, response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
