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
)

func (server *Server) diagnosticsHandler(response http.ResponseWriter, request *http.Request) {
	if server.diagnosticService == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		if len(request.URL.Query()) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.DiagnosticsRequestInvalid)
			return
		}
		settings, err := server.diagnosticService.Settings(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		writeJSON(response, http.StatusOK, diagnosticsResponse(settings, nil, nil))
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.DiagnosticsRequestInvalid)
		return
	}
	var input DiagnosticsRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.DiagnosticsRequestInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.DiagnosticsRequestInvalid)
		return
	}

	settings, err := server.diagnosticService.Settings(request.Context())
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	var preview *diagnostics.Preview
	var bundle *diagnostics.Bundle
	switch input.Action {
	case "settings":
		if input.Settings != nil || input.Confirmation != nil {
			err = apperrors.New(apperrors.DiagnosticsRequestInvalid, errors.New("diagnostic settings read does not accept values"))
		}
	case "configure":
		if input.Settings == nil || input.Confirmation != nil {
			err = apperrors.New(apperrors.DiagnosticsRequestInvalid, errors.New("diagnostic settings are required"))
			break
		}
		settings, err = server.diagnosticService.UpdateSettings(request.Context(), diagnostics.Settings{
			Enabled: input.Settings.Enabled, MinimumLevel: diagnostics.Level(input.Settings.MinimumLevel), RetentionDays: int(input.Settings.RetentionDays),
		})
	case "preview":
		if input.Settings != nil || input.Confirmation != nil {
			err = apperrors.New(apperrors.DiagnosticsRequestInvalid, errors.New("diagnostic preview does not accept settings or confirmation"))
			break
		}
		value, previewErr := server.diagnosticService.Preview(request.Context())
		preview, err = &value, previewErr
	case "export":
		if input.Settings != nil || input.Confirmation == nil || *input.Confirmation == "" {
			err = apperrors.New(apperrors.DiagnosticsRequestInvalid, errors.New("diagnostic export confirmation is required"))
			break
		}
		value, exportErr := server.diagnosticService.Export(request.Context(), *input.Confirmation)
		bundle, err = &value, exportErr
	default:
		err = apperrors.New(apperrors.DiagnosticsRequestInvalid, errors.New("diagnostic action is invalid"))
	}
	if err != nil {
		code := diagnostics.CodeFor(err, apperrors.DiagnosticsRequestInvalid)
		status := http.StatusConflict
		if code == apperrors.DiagnosticsRequestInvalid || code == apperrors.DiagnosticsConfigurationInvalid {
			status = http.StatusBadRequest
		}
		server.writeAPIError(response, status, code)
		return
	}
	writeJSON(response, http.StatusOK, diagnosticsResponse(settings, preview, bundle))
}

func diagnosticsResponse(settings diagnostics.Settings, preview *diagnostics.Preview, bundle *diagnostics.Bundle) DiagnosticsResponse {
	result := DiagnosticsResponse{
		Settings:            DiagnosticSettings{Enabled: settings.Enabled, MinimumLevel: string(settings.MinimumLevel), RetentionDays: int64(settings.RetentionDays)},
		MaximumEncodedBytes: diagnostics.MaxBundleEncodedBytes,
	}
	if preview != nil {
		value := DiagnosticPreview{
			Fields: append([]string(nil), preview.Fields...), DiagnosticCount: int64(preview.DiagnosticCount),
			EncodedBytes: int64(preview.EncodedBytes), ConfirmationDigest: preview.ConfirmationDigest,
			Bundle: diagnosticBundle(preview.Bundle),
		}
		result.Preview = &value
	}
	if bundle != nil {
		value := diagnosticBundle(*bundle)
		result.Bundle = &value
	}
	return result
}

func diagnosticBundle(bundle diagnostics.Bundle) DiagnosticBundle {
	records := make([]DiagnosticRecord, 0, len(bundle.Diagnostics))
	for _, record := range bundle.Diagnostics {
		records = append(records, DiagnosticRecord{
			Id: record.ID, Component: record.Component, ErrorCode: record.ErrorCode, Severity: string(record.Severity),
			OccurrenceCount: int64(record.OccurrenceCount), FirstSeenAt: record.FirstSeenAt.Format(time.RFC3339Nano), LastSeenAt: record.LastSeenAt.Format(time.RFC3339Nano),
		})
	}
	return DiagnosticBundle{
		SchemaVersion: int64(bundle.SchemaVersion),
		GeneratedAt:   bundle.GeneratedAt.Format(time.RFC3339Nano),
		Settings:      DiagnosticSettings{Enabled: bundle.Settings.Enabled, MinimumLevel: string(bundle.Settings.MinimumLevel), RetentionDays: int64(bundle.Settings.RetentionDays)},
		Environment: DiagnosticEnvironment{
			ApplicationVersion: bundle.Environment.ApplicationVersion, DatabaseSchemaVersion: int64(bundle.Environment.DatabaseSchemaVersion),
			OsFamily: string(bundle.Environment.OSFamily), Architecture: string(bundle.Environment.Architecture),
			Features: DiagnosticFeatureStates{
				Service: bundle.Environment.Features.Service, DetailedAlerts: bundle.Environment.Features.DetailedAlerts,
				AutomaticUpdates: bundle.Environment.Features.AutomaticUpdates, Telemetry: bundle.Environment.Features.Telemetry,
			},
			Health: DiagnosticHealth{
				Service: string(bundle.Environment.Health.Service), Database: string(bundle.Environment.Health.Database),
				Vault: string(bundle.Environment.Health.Vault), ErrorCode: optionalNonEmpty(bundle.Environment.Health.ErrorCode),
			},
		},
		Diagnostics: records,
	}
}

func optionalNonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (client *CommandClient) Diagnostics(ctx context.Context, input DiagnosticsRequest) (DiagnosticsResponse, error) {
	var result DiagnosticsResponse
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandDiagnosticsPath, bytes.NewReader(body))
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
		var failure UsageErrorResponse
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandDiagnosticsPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandDiagnosticsPath, response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
