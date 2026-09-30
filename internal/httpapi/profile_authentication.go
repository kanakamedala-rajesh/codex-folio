package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/profile"
)

type CommandProfileAuthenticationRequest struct {
	Action                   string             `json:"action"`
	Alias                    string             `json:"alias"`
	DisplayName              string             `json:"display_name,omitempty"`
	CodexOverride            string             `json:"codex_override,omitempty"`
	ReferencedHomePath       string             `json:"referenced_home_path,omitempty"`
	AuthMethod               profile.AuthMethod `json:"auth_method,omitempty"`
	NonInteractive           bool               `json:"non_interactive"`
	ConfigurationPackID      string             `json:"configuration_pack_id,omitempty"`
	ConfigurationPackVersion string             `json:"configuration_pack_version,omitempty"`
}

type CommandProfileAuthenticationResult struct {
	Setup            *profile.SetupResult            `json:"setup,omitempty"`
	Reauthentication *profile.ReauthenticationResult `json:"reauthentication,omitempty"`
}

type CommandProfileAuthenticationService interface {
	Authenticate(context.Context, CommandProfileAuthenticationRequest, io.Writer) (CommandProfileAuthenticationResult, error)
}

type profileAuthenticationEvent struct {
	Output string                              `json:"output,omitempty"`
	Result *CommandProfileAuthenticationResult `json:"result,omitempty"`
	Code   string                              `json:"code,omitempty"`
}

// profileOperation contains only browser-safe progress for terminal setup.
type profileOperation struct {
	State string
	Code  string
}

func (server *Server) getProfileOperation(alias string) profileOperation {
	server.profileOperationMu.Lock()
	defer server.profileOperationMu.Unlock()
	return server.profileOperations[strings.ToLower(alias)]
}

func (server *Server) setProfileOperation(alias string, operation profileOperation) {
	server.profileOperationMu.Lock()
	defer server.profileOperationMu.Unlock()
	if server.profileOperations == nil {
		server.profileOperations = make(map[string]profileOperation)
	}
	server.profileOperations[strings.ToLower(alias)] = operation
}

func (server *Server) beginProfileOperation(alias string) bool {
	server.profileOperationMu.Lock()
	defer server.profileOperationMu.Unlock()
	if server.profileOperations == nil {
		server.profileOperations = make(map[string]profileOperation)
	}
	key := strings.ToLower(alias)
	if server.profileOperations[key].State == "running" {
		return false
	}
	server.profileOperations[key] = profileOperation{State: "running"}
	return true
}

type profileAuthenticationWriter struct {
	mu      sync.Mutex
	encoder *json.Encoder
	flusher http.Flusher
}

func (writer *profileAuthenticationWriter) Write(content []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.encoder.Encode(profileAuthenticationEvent{Output: string(content)}); err != nil {
		return 0, err
	}
	writer.flusher.Flush()
	return len(content), nil
}

func (server *Server) commandProfileAuthentication(response http.ResponseWriter, request *http.Request) {
	if server.profileAuthentication == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	var input CommandProfileAuthenticationRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	if input.Action != "add" && input.Action != "reauthenticate" {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	if err := profile.ValidateAlias(input.Alias); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, diagnostics.CodeFor(err, apperrors.ProfileAliasInvalid))
		return
	}
	if !server.beginProfileOperation(input.Alias) {
		server.writeAPIError(response, http.StatusConflict, apperrors.ProfileSetupInvalid)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		server.setProfileOperation(input.Alias, profileOperation{State: "failed", Code: apperrors.HTTPAPIServiceUnavailable})
		server.writeAPIError(response, http.StatusInternalServerError, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	_ = http.NewResponseController(response).SetWriteDeadline(time.Time{})
	response.Header().Set("Content-Type", "application/x-ndjson")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()
	stream := &profileAuthenticationWriter{encoder: json.NewEncoder(response), flusher: flusher}
	result, authErr := server.profileAuthentication.Authenticate(request.Context(), input, stream)
	operation := profileOperation{State: "ready"}
	if authErr != nil {
		operation = profileOperation{State: "failed", Code: diagnostics.CodeFor(authErr, apperrors.ProfileAuthenticationFailed)}
	}
	server.setProfileOperation(input.Alias, operation)
	event := profileAuthenticationEvent{Result: &result}
	if authErr != nil {
		event.Result = nil
		event.Code = diagnostics.CodeFor(authErr, apperrors.ProfileAuthenticationFailed)
	}
	stream.mu.Lock()
	_ = stream.encoder.Encode(event)
	stream.flusher.Flush()
	stream.mu.Unlock()
}
