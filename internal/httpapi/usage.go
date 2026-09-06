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
	"venkatasudha.com/codex-folio/internal/usage"
)

type CommandUsageService interface {
	Refresh(context.Context, string) (usage.Snapshot, error)
}

func (client *CommandClient) RefreshUsage(ctx context.Context, alias string) (UsageSnapshotResponse, error) {
	var result UsageSnapshotResponse
	encoded, err := json.Marshal(UsageRefreshRequest{Alias: alias})
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandUsageRefreshPath, bytes.NewReader(encoded))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandUsageRefreshPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandUsageRefreshPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (server *Server) usageRefresh(response http.ResponseWriter, request *http.Request) {
	if server.usage == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	var input UsageRefreshRequest
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
		return
	}
	snapshot, err := server.usage.Refresh(request.Context(), input.Alias)
	if err != nil {
		status := http.StatusConflict
		if code := apperrors.Code(err); code == apperrors.StoreReadFailed || code == apperrors.StoreWriteFailed || code == apperrors.UsageCollectionFailed || code == apperrors.UsageSourceInvalid {
			status = http.StatusInternalServerError
		}
		server.writeAPIError(response, status, diagnostics.CodeFor(err, apperrors.UsageRequestInvalid))
		return
	}
	writeJSON(response, http.StatusOK, usageSnapshotResponse(snapshot))
}

func usageSnapshotResponse(snapshot usage.Snapshot) UsageSnapshotResponse {
	result := UsageSnapshotResponse{
		SnapshotId: snapshot.ID, ProfileId: snapshot.ProfileID, Alias: snapshot.Alias, Source: snapshot.Source,
		SourceVersion: snapshot.SourceVersion, CapturedAt: formatUsageTime(snapshot.CapturedAt),
		Observations: []UsageObservation{}, Availability: []UsageMetricAvailability{},
	}
	for _, observation := range snapshot.Observations {
		result.Observations = append(result.Observations, UsageObservation{
			ObservationId: observation.ID, MetricKey: observation.Metric.Key, Value: observation.Value, Unit: observation.Metric.Unit,
			ValueKind: observation.Metric.ValueKind, SourceClass: observation.Metric.SourceClass, Scope: observation.Metric.Scope,
			Aggregation: observation.Metric.Aggregation, ObservedAt: formatUsageTime(observation.ObservedAt), WindowStart: formatUsageTimePointer(observation.WindowStart),
			WindowEnd: formatUsageTimePointer(observation.WindowEnd), Provenance: observation.Provenance, Freshness: observation.Freshness, Availability: observation.Availability,
		})
	}
	for _, availability := range snapshot.Availability {
		result.Availability = append(result.Availability, UsageMetricAvailability{
			MetricAvailabilityId: availability.ID, MetricKey: availability.MetricKey, State: availability.State,
			CheckedAt: formatUsageTime(availability.CheckedAt), Provenance: availability.Provenance,
		})
	}
	return result
}

func formatUsageTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func formatUsageTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatUsageTime(*value)
}
