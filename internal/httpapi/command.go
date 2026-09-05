package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
)

// CommandClient is the authenticated loopback transport used by CLI commands.
type CommandClient struct {
	origin string
	token  string
	doer   HTTPDoer
}

func NewCommandClient(origin, token string, doer HTTPDoer) *CommandClient {
	return &CommandClient{origin: strings.TrimRight(origin, "/"), token: token, doer: doer}
}

func (client *CommandClient) GetSelection(ctx context.Context) (CommandSelectionResponse, error) {
	return client.selection(ctx, http.MethodGet, "")
}

func (client *CommandClient) SetSelection(ctx context.Context, alias string) (CommandSelectionResponse, error) {
	return client.selection(ctx, http.MethodPut, alias)
}

func (client *CommandClient) ListProfiles(ctx context.Context) (CommandProfilesResponse, error) {
	return client.profiles(ctx, http.MethodGet, "", profile.ProfileEdits{})
}

func (client *CommandClient) EditProfile(ctx context.Context, alias string, edits profile.ProfileEdits) (CommandProfilesResponse, error) {
	return client.profiles(ctx, http.MethodPut, alias, edits)
}

func (client *CommandClient) PreviewProfileLifecycle(ctx context.Context, action, alias string) (profile.RemovalRecord, error) {
	return client.profileLifecycle(ctx, commandProfileLifecycleRequest{Action: action, Alias: alias})
}

func (client *CommandClient) ApplyProfileLifecycle(ctx context.Context, action, alias, replacement, confirmation string) (profile.RemovalRecord, error) {
	return client.profileLifecycle(ctx, commandProfileLifecycleRequest{Action: action, Alias: alias, Replacement: replacement, Confirmation: confirmation, Apply: true})
}

func (client *CommandClient) ConfigurationPack(ctx context.Context, input CommandConfigurationPackRequest) (CommandConfigurationPackResponse, error) {
	var result CommandConfigurationPackResponse
	encoded, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandConfigurationPackPath, bytes.NewReader(encoded))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandConfigurationPackPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandConfigurationPackPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *CommandClient) profileLifecycle(ctx context.Context, input commandProfileLifecycleRequest) (profile.RemovalRecord, error) {
	var result profile.RemovalRecord
	encoded, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandProfileLifecyclePath, bytes.NewReader(encoded))
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
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandProfileLifecyclePath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandProfileLifecyclePath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *CommandClient) profiles(ctx context.Context, method, alias string, edits profile.ProfileEdits) (CommandProfilesResponse, error) {
	var result CommandProfilesResponse
	body := bytes.NewReader(nil)
	if method == http.MethodPut {
		encoded, err := json.Marshal(commandProfileEditRequest{Alias: alias, Edits: edits})
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandProfilesPath, body)
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/json")
	}
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
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandProfilesPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandProfilesPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *CommandClient) selection(ctx context.Context, method, alias string) (CommandSelectionResponse, error) {
	var result CommandSelectionResponse
	var body *bytes.Reader
	if method == http.MethodPut {
		encoded, err := json.Marshal(SelectionRequest{Alias: alias})
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(encoded)
	} else {
		body = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandSelectionPath, body)
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/json")
	}
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
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandSelectionPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandSelectionPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *CommandClient) httpDoer() HTTPDoer {
	if client.doer != nil {
		return client.doer
	}
	return http.DefaultClient
}
