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
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/telemetry"
)

var telemetryExcludedFields = []string{"identity", "workspace", "project", "paths", "usage", "session", "codex_content", "credentials", "command_arguments", "raw_payloads"}

func (server *Server) telemetryHandler(response http.ResponseWriter, request *http.Request) {
	if server.telemetry == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		if len(request.URL.Query()) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.TelemetryRequestInvalid)
			return
		}
		snapshot, err := server.telemetry.Status(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		writeJSON(response, http.StatusOK, telemetryResponse(snapshot))
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.TelemetryRequestInvalid)
		return
	}
	var input TelemetryRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.TelemetryRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.TelemetryRequestInvalid)
		return
	}
	var snapshot telemetry.Snapshot
	switch input.Action {
	case "enable":
		if input.SchemaVersion == nil {
			err = telemetry.ErrConsentRequired
		} else {
			snapshot, err = server.telemetry.Enable(request.Context(), int(*input.SchemaVersion))
		}
	case "revoke":
		if input.SchemaVersion != nil {
			err = telemetry.ErrInvalid
		} else {
			snapshot, err = server.telemetry.Revoke(request.Context())
		}
	case "reset_id":
		if input.SchemaVersion != nil {
			err = telemetry.ErrInvalid
		} else {
			snapshot, err = server.telemetry.ResetInstallationID(request.Context())
		}
	default:
		err = telemetry.ErrInvalid
	}
	if err != nil {
		status, code := http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreWriteFailed)
		if errors.Is(err, telemetry.ErrPrerequisitesMissing) {
			status, code = http.StatusConflict, apperrors.TelemetryUnavailable
		} else if errors.Is(err, telemetry.ErrConsentRequired) || errors.Is(err, telemetry.ErrInvalid) {
			status, code = http.StatusBadRequest, apperrors.TelemetryRequestInvalid
		}
		server.writeAPIError(response, status, code)
		return
	}
	writeJSON(response, http.StatusOK, telemetryResponse(snapshot))
}

func telemetryResponse(snapshot telemetry.Snapshot) TelemetryResponse {
	available := snapshot.Prerequisites.Ready()
	detail := "Telemetry collection is off. Enablement is unavailable until every published privacy prerequisite is operational."
	if available && snapshot.Status == telemetry.StatusDisabled {
		detail = "Telemetry is available but remains off until explicit consent is granted for this schema version."
	} else if snapshot.Status == telemetry.StatusEnabled {
		detail = "Telemetry is enabled for the explicitly consented public schema."
	}
	return TelemetryResponse{
		Available: available, Enabled: snapshot.Status == telemetry.StatusEnabled, Status: string(snapshot.Status),
		SchemaVersion: int64(snapshot.Schema.Version), ConsentSchemaVersion: int64(snapshot.Schema.ConsentVersion),
		EventRetentionDays: int64(snapshot.Schema.EventRetentionDays), AggregateRetentionMonths: int64(snapshot.Schema.AggregateRetentionMonths),
		InstallationIdPresent: snapshot.InstallationID != "",
		Prerequisites: TelemetryPrerequisites{
			Endpoint: snapshot.Prerequisites.Endpoint, PublicSchema: snapshot.Prerequisites.PublicSchema,
			PrivacyNotice: snapshot.Prerequisites.PrivacyNotice, EventRetention: snapshot.Prerequisites.EventRetention,
			AggregateRetention: snapshot.Prerequisites.AggregateRetention, Deletion: snapshot.Prerequisites.Deletion, Reset: snapshot.Prerequisites.Reset,
		},
		AllowedFields: append([]string(nil), snapshot.Schema.Fields...), ExcludedFields: append([]string(nil), telemetryExcludedFields...), Detail: detail,
		OsFamilies: telemetryStrings(snapshot.Schema.OSFamilies), Architectures: telemetryStrings(snapshot.Schema.Architectures),
		Features: telemetryStrings(snapshot.Schema.Features), Outcomes: telemetryStrings(snapshot.Schema.Outcomes),
		DurationBuckets: telemetryStrings(snapshot.Schema.DurationBuckets),
	}
}

func telemetryStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func (client *CommandClient) TelemetryStatus(ctx context.Context) (TelemetryResponse, error) {
	return client.telemetry(ctx, http.MethodGet, nil)
}

func (client *CommandClient) ManageTelemetry(ctx context.Context, input TelemetryRequest) (TelemetryResponse, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return TelemetryResponse{}, err
	}
	return client.telemetry(ctx, http.MethodPost, body)
}

func (client *CommandClient) telemetry(ctx context.Context, method string, body []byte) (TelemetryResponse, error) {
	var result TelemetryResponse
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandTelemetryPath, bytes.NewReader(body))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandTelemetryPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandTelemetryPath, response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
