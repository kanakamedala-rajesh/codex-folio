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
	"net/url"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configbundle"
)

type ConfigurationBundleRequest struct {
	Action                string               `json:"action"`
	Bundle                *configbundle.Bundle `json:"bundle,omitempty"`
	ConfirmationDigest    string               `json:"confirmation_digest,omitempty"`
	Reviewed              bool                 `json:"reviewed,omitempty"`
	Resolutions           map[string]string    `json:"resolutions,omitempty"`
	IncludeProjectAliases bool                 `json:"include_project_aliases,omitempty"`
	Appearance            string               `json:"appearance,omitempty"`
}
type ConfigurationBundleResponse struct {
	Preview *configbundle.Preview     `json:"preview,omitempty"`
	Result  *configbundle.ApplyResult `json:"result,omitempty"`
}

func (server *Server) configurationBundleHandler(response http.ResponseWriter, request *http.Request) {
	if server.configurationBundles == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		includeProjectAliases, valid := configurationProjectAliasOption(request.URL.Query())
		if !valid {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
			return
		}
		preview, err := server.configurationBundles.ExportPreview(request.Context(), includeProjectAliases)
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, errorCodeForBundle(err))
			return
		}
		writeJSON(response, http.StatusOK, ConfigurationBundleResponse{Preview: &preview})
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > configbundle.MaxBundleBytes+65536 {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, configbundle.MaxBundleBytes+65536))
	decoder.DisallowUnknownFields()
	var input ConfigurationBundleRequest
	if err = decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
		return
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
		return
	}
	switch input.Action {
	case "configure_appearance":
		if input.Bundle != nil || input.ConfirmationDigest != "" || input.Reviewed || len(input.Resolutions) != 0 || input.IncludeProjectAliases {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
			return
		}
		preview, configureErr := server.configurationBundles.SetAppearance(request.Context(), input.Appearance)
		if configureErr != nil {
			server.writeAPIError(response, http.StatusBadRequest, errorCodeForBundle(configureErr))
			return
		}
		writeJSON(response, http.StatusOK, ConfigurationBundleResponse{Preview: &preview})
	case "export_preview":
		if input.Bundle != nil || input.ConfirmationDigest != "" || input.Reviewed || len(input.Resolutions) != 0 {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
			return
		}
		preview, previewErr := server.configurationBundles.ExportPreview(request.Context(), input.IncludeProjectAliases)
		if previewErr != nil {
			server.writeAPIError(response, http.StatusInternalServerError, errorCodeForBundle(previewErr))
			return
		}
		writeJSON(response, http.StatusOK, ConfigurationBundleResponse{Preview: &preview})
	case "import_preview":
		source, ok := server.configurationBundleSource(response, input.Bundle)
		if !ok {
			return
		}
		preview, _, previewErr := server.configurationBundles.ImportPreview(request.Context(), source)
		if previewErr != nil {
			server.writeAPIError(response, http.StatusBadRequest, errorCodeForBundle(previewErr))
			return
		}
		writeJSON(response, http.StatusOK, ConfigurationBundleResponse{Preview: &preview})
	case "import_apply":
		source, ok := server.configurationBundleSource(response, input.Bundle)
		if !ok {
			return
		}
		result, applyErr := server.configurationBundles.ImportApply(request.Context(), source, configbundle.ApplyRequest{ConfirmationDigest: input.ConfirmationDigest, Reviewed: input.Reviewed, Resolutions: input.Resolutions})
		if applyErr != nil {
			status := http.StatusBadRequest
			if errors.Is(applyErr, configbundle.ErrConflict) || errors.Is(applyErr, configbundle.ErrStalePreview) {
				status = http.StatusConflict
			}
			server.writeAPIError(response, status, errorCodeForBundle(applyErr))
			return
		}
		writeJSON(response, http.StatusOK, ConfigurationBundleResponse{Result: &result})
	default:
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
	}
}

func configurationProjectAliasOption(query url.Values) (bool, bool) {
	if len(query) == 0 {
		return false, true
	}
	values, ok := query["include_project_aliases"]
	if !ok || len(query) != 1 || len(values) != 1 || (values[0] != "true" && values[0] != "false") {
		return false, false
	}
	return values[0] == "true", true
}

func (server *Server) configurationBundleSource(response http.ResponseWriter, bundle *configbundle.Bundle) ([]byte, bool) {
	if bundle == nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
		return nil, false
	}
	source, err := configbundle.CanonicalJSON(*bundle)
	if err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationBundleInvalid)
		return nil, false
	}
	return source, true
}
func errorCodeForBundle(err error) string {
	switch {
	case errors.Is(err, configbundle.ErrConflict):
		return apperrors.ConfigurationBundleConflict
	case errors.Is(err, configbundle.ErrStalePreview):
		return apperrors.ConfigurationBundleStale
	default:
		return apperrors.ConfigurationBundleInvalid
	}
}

func (client *CommandClient) ConfigurationExportPreview(ctx context.Context, includeProjectAliases bool) (ConfigurationBundleResponse, error) {
	return client.configurationBundle(ctx, http.MethodGet, ConfigurationBundleRequest{IncludeProjectAliases: includeProjectAliases})
}
func (client *CommandClient) ConfigurationImport(ctx context.Context, input ConfigurationBundleRequest) (ConfigurationBundleResponse, error) {
	return client.configurationBundle(ctx, http.MethodPost, input)
}
func (client *CommandClient) configurationBundle(ctx context.Context, method string, input ConfigurationBundleRequest) (ConfigurationBundleResponse, error) {
	var result ConfigurationBundleResponse
	var body io.Reader
	if method == http.MethodPost {
		encoded, err := json.Marshal(input)
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(encoded)
	}
	target := client.origin + CommandConfigurationBundlePath
	if method == http.MethodGet && input.IncludeProjectAliases {
		target += "?include_project_aliases=true"
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
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
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure UsageErrorResponse
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandConfigurationBundlePath, response.StatusCode))
		}
		return result, fmt.Errorf("configuration request returned HTTP %d", response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}
