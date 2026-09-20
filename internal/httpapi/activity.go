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
	"strconv"
	"strings"
	"unicode"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type CommandActivityService interface {
	Refresh(context.Context, string) ([]activity.TimelineRecord, error)
	List(context.Context, activity.Filters) ([]activity.TimelineRecord, error)
}

type CommandActivityRequest struct {
	Action       string `json:"action"`
	Alias        string `json:"alias,omitempty"`
	ProfileAlias string `json:"profile_alias,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
}

type CommandActivityResponse struct {
	Records []activity.TimelineRecord `json:"records"`
}

func (client *CommandClient) Activity(ctx context.Context, input CommandActivityRequest) (CommandActivityResponse, error) {
	var result CommandActivityResponse
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandActivityPath, bytes.NewReader(body))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandActivityPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandActivityPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (server *Server) commandActivity(response http.ResponseWriter, request *http.Request) {
	if server.activities == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ActivityRequestInvalid)
		return
	}
	var input CommandActivityRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ActivityRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ActivityRequestInvalid)
		return
	}
	if !validActivityFilter(input.ProfileAlias, input.ProjectID) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ActivityRequestInvalid)
		return
	}
	var records []activity.TimelineRecord
	switch input.Action {
	case "refresh":
		if strings.TrimSpace(input.Alias) == "" || input.ProfileAlias != "" || input.ProjectID != "" {
			err = activity.ErrActivityInvalid
		} else {
			records, err = server.activities.Refresh(request.Context(), input.Alias)
		}
	case "list":
		if input.Alias != "" {
			err = activity.ErrActivityInvalid
		} else {
			records, err = server.activities.List(request.Context(), activity.Filters{ProfileAlias: input.ProfileAlias, ProjectID: input.ProjectID})
		}
	default:
		err = activity.ErrActivityInvalid
	}
	if err != nil {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ActivityRequestInvalid))
		return
	}
	writeJSON(response, http.StatusOK, CommandActivityResponse{Records: records})
}

func (server *Server) getActivity(response http.ResponseWriter, request *http.Request) {
	if server.activities == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	query := request.URL.Query()
	profileAlias, projectID := query.Get("profile"), query.Get("project")
	if !validActivityQuery(query) || !validActivityFilter(profileAlias, projectID) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ActivityRequestInvalid)
		return
	}
	records, err := server.activities.List(request.Context(), activity.Filters{ProfileAlias: profileAlias, ProjectID: projectID})
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	writeJSON(response, http.StatusOK, ActivityResponseFor(records))
}

func validActivityQuery(query map[string][]string) bool {
	for key, values := range query {
		if (key != "profile" && key != "project") || len(values) != 1 {
			return false
		}
	}
	return true
}

func ActivityResponseFor(records []activity.TimelineRecord) ActivityResponse {
	result := ActivityResponse{Records: make([]ActivityRecord, 0, len(records))}
	for _, record := range records {
		item := ActivityRecord{
			RecordType: record.RecordType, Id: record.ID, SourceSessionId: record.SourceSessionID,
			ProfileId: record.ProfileID, ProfileAlias: record.ProfileAlias, ProjectId: record.ProjectID,
			ProjectAlias: record.ProjectAlias, ProjectBasename: record.ProjectBasename, Source: record.Source,
			SourceVersion: record.SourceVersion, Provenance: record.Provenance, StartedAt: formatUsageTime(record.StartedAt),
			LastObservedAt: formatUsageTime(record.LastObservedAt), Lifecycle: record.Lifecycle, Model: record.Model,
			CorrelationState: record.Correlation.State, CorrelationManagedLaunchId: record.Correlation.ManagedLaunchID,
			CorrelationEvidenceType: record.Correlation.EvidenceType, CorrelationConfidence: record.Correlation.Confidence,
		}
		if record.ContinuationCheckpointID != "" {
			item.ContinuationCheckpointId = &record.ContinuationCheckpointID
		}
		if record.ContinuationRevision != "" {
			item.ContinuationRevision = &record.ContinuationRevision
		}
		if record.ExitStatus != nil {
			item.ExitStatus = strconv.Itoa(*record.ExitStatus)
		}
		if record.TokensUsed != nil {
			item.TokensUsed = strconv.FormatInt(*record.TokensUsed, 10)
		}
		result.Records = append(result.Records, item)
	}
	return result
}

func validActivityFilter(profileAlias, projectID string) bool {
	return len(profileAlias) <= 120 && len(projectID) <= 200 &&
		strings.IndexFunc(profileAlias, unicode.IsControl) < 0 && strings.IndexFunc(projectID, unicode.IsControl) < 0
}
