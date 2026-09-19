package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	alertfeature "venkatasudha.com/codex-folio/internal/alerts"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

func (server *Server) alertsHandler(response http.ResponseWriter, request *http.Request) {
	if server.alerts == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if len(request.URL.Query()) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		records, err := server.alerts.Evaluate(request.Context(), "")
		server.writeAlerts(response, records, err)
	case http.MethodPost:
		contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
		decoder.DisallowUnknownFields()
		var input AlertActionRequest
		if err := decoder.Decode(&input); err != nil {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		var records []alertfeature.Record
		switch input.Action {
		case "acknowledge":
			if input.AlertId == nil || *input.AlertId == "" || input.ProfileId != nil || input.MetricKey != nil || input.WarningPercent != nil || input.CriticalPercent != nil || input.DetailedContentEnabled != nil {
				server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
				return
			}
			records, err = server.alerts.Acknowledge(request.Context(), *input.AlertId)
		case "set_threshold":
			if input.AlertId != nil || input.ProfileId == nil || input.MetricKey == nil || input.WarningPercent == nil || input.CriticalPercent == nil || input.DetailedContentEnabled != nil {
				server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
				return
			}
			_, err = server.alerts.SetThreshold(request.Context(), alertfeature.Threshold{ProfileID: *input.ProfileId, MetricKey: *input.MetricKey, WarningPercent: *input.WarningPercent, CriticalPercent: *input.CriticalPercent})
			if err == nil {
				records, err = server.alerts.Evaluate(request.Context(), "")
			}
		case "set_notification_detail":
			if input.AlertId != nil || input.ProfileId != nil || input.MetricKey != nil || input.WarningPercent != nil || input.CriticalPercent != nil || input.DetailedContentEnabled == nil {
				server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
				return
			}
			_, err = server.alerts.SetNotificationDetail(request.Context(), *input.DetailedContentEnabled)
			if err == nil {
				records, err = server.alerts.Evaluate(request.Context(), "")
			}
		default:
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		server.writeAlerts(response, records, err)
	default:
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
	}
}

func (server *Server) writeAlerts(response http.ResponseWriter, records []alertfeature.Record, err error) {
	if err != nil {
		server.writeAPIError(response, http.StatusBadRequest, diagnostics.CodeFor(err, apperrors.UsageRequestInvalid))
		return
	}
	thresholds, err := server.alerts.Thresholds(nil)
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	health, err := server.alerts.DeliveryHealth(nil)
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	result := AlertsResponse{
		Active: []AlertRecord{}, History: []AlertRecord{}, Thresholds: []AlertThreshold{},
		DeliveryHealth: AlertDeliveryHealth{Dashboard: health.Dashboard, NativeNotifications: health.Native, Mechanism: health.Mechanism, Detail: health.Detail, DetailedContentEnabled: health.DetailEnabled},
	}
	for _, item := range records {
		projected := alertRecord(item)
		if item.State != alertfeature.StateResolved {
			result.Active = append(result.Active, projected)
		}
		result.History = append(result.History, projected)
	}
	for _, item := range thresholds {
		result.Thresholds = append(result.Thresholds, AlertThreshold{ProfileId: item.ProfileID, MetricKey: item.MetricKey, WarningPercent: item.WarningPercent, CriticalPercent: item.CriticalPercent})
	}
	writeJSON(response, http.StatusOK, result)
}

func alertRecord(item alertfeature.Record) AlertRecord {
	return AlertRecord{
		AlertId: item.ID, ProfileId: item.ProfileID, ProfileAlias: item.ProfileAlias, Category: item.Category, Kind: item.Kind, Severity: item.Severity, State: item.State,
		Title: item.Title, Guidance: item.Guidance, MetricKey: item.MetricKey, WindowStart: formatUsageTimePointer(item.WindowStart), WindowEnd: formatUsageTimePointer(item.WindowEnd),
		Source: item.Source, SourceVersion: item.SourceVersion, Provenance: item.Provenance, Scope: item.Scope, Freshness: item.Freshness, AvailabilityReason: item.AvailabilityReason,
		EvidenceCapturedAt: formatOptionalUsageTime(item.EvidenceCapturedAt), ObservedAt: formatOptionalUsageTime(item.ObservedAt),
		RemainingPercent: item.RemainingPercent, FirstSeenAt: formatUsageTime(item.FirstSeenAt), LastSeenAt: formatUsageTime(item.LastSeenAt),
		AcknowledgedAt: formatUsageTimePointer(item.AcknowledgedAt), ResolvedAt: formatUsageTimePointer(item.ResolvedAt), OccurrenceCount: int64(item.OccurrenceCount),
	}
}

func formatOptionalUsageTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatUsageTime(value)
}
