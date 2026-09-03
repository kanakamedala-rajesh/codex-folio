package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
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
	doer := client.doer
	if doer == nil {
		doer = http.DefaultClient
	}
	response, err := doer.Do(request)
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
