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

const providerFloorBasis = "codex_app_server_supported_read_no_published_polling_cadence"

type CollectionSettingsService interface {
	CollectionSettings(context.Context) (usage.CollectionSettings, bool, error)
	SetCollectionSettings(context.Context, usage.CollectionSettings) (usage.CollectionSettings, bool, error)
}

func (server *Server) collectionSettingsHandler(response http.ResponseWriter, request *http.Request) {
	if server.collectionSettings == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	var settings usage.CollectionSettings
	var enabled bool
	var err error
	switch request.Method {
	case http.MethodGet:
		if len(request.URL.Query()) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		settings, enabled, err = server.collectionSettings.CollectionSettings(request.Context())
	case http.MethodPut:
		contentType, _, parseErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if parseErr != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
		decoder.DisallowUnknownFields()
		var input CollectionSettingsRequest
		if decodeErr := decoder.Decode(&input); decodeErr != nil {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		var trailing any
		if decodeErr := decoder.Decode(&trailing); !errors.Is(decodeErr, io.EOF) {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		minimumSeconds := int64(usage.ProviderSafeMinimum / time.Second)
		maximumSeconds := int64(usage.MaximumInterval / time.Second)
		if input.ActiveIntervalSeconds < minimumSeconds || input.ActiveIntervalSeconds > maximumSeconds ||
			input.IdleIntervalSeconds < minimumSeconds || input.IdleIntervalSeconds > maximumSeconds {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.UsageRequestInvalid)
			return
		}
		settings, enabled, err = server.collectionSettings.SetCollectionSettings(request.Context(), usage.CollectionSettings{
			ActiveInterval:  time.Duration(input.ActiveIntervalSeconds) * time.Second,
			IdleInterval:    time.Duration(input.IdleIntervalSeconds) * time.Second,
			ProviderMinimum: usage.ProviderSafeMinimum,
		})
	default:
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPut)
		return
	}
	if err != nil {
		server.writeAPIError(response, http.StatusBadRequest, diagnostics.CodeFor(err, apperrors.UsageRequestInvalid))
		return
	}
	writeJSON(response, http.StatusOK, collectionSettingsResponse(settings, enabled))
}

func collectionSettingsResponse(settings usage.CollectionSettings, enabled bool) CollectionSettingsResponse {
	return CollectionSettingsResponse{
		ActiveIntervalSeconds:  int64(settings.ActiveInterval / time.Second),
		IdleIntervalSeconds:    int64(settings.IdleInterval / time.Second),
		ProviderMinimumSeconds: int64(settings.ProviderMinimum / time.Second),
		SchedulerEnabled:       enabled,
		ProviderFloorBasis:     providerFloorBasis,
	}
}

func (client *CommandClient) CollectionSettings(ctx context.Context) (CollectionSettingsResponse, error) {
	return client.collectionSettings(ctx, http.MethodGet, nil)
}

func (client *CommandClient) SetCollectionSettings(ctx context.Context, input CollectionSettingsRequest) (CollectionSettingsResponse, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return CollectionSettingsResponse{}, err
	}
	return client.collectionSettings(ctx, http.MethodPut, body)
}

func (client *CommandClient) collectionSettings(ctx context.Context, method string, body []byte) (CollectionSettingsResponse, error) {
	var result CollectionSettingsResponse
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandCollectionSettingsPath, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	if body != nil {
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
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandCollectionSettingsPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandCollectionSettingsPath, response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
