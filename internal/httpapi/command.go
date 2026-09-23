package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
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

// NewPinnedCommandClient authenticates the exact HTTPS identity before any
// command token is sent. A private service descriptor supplies the fingerprint.
func NewPinnedCommandClient(origin, token, certificateSHA256 string) (*CommandClient, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("pinned command origin must be HTTPS")
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("pinned command origin must be loopback")
	}
	want, err := hex.DecodeString(certificateSHA256)
	if err != nil || len(want) != sha256.Size || hex.EncodeToString(want) != certificateSHA256 {
		return nil, errors.New("invalid command certificate fingerprint")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		// The exact certificate pin below authenticates the peer. The machine's
		// root pool need not trust this locally generated certificate.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("command TLS peer did not present a certificate")
			}
			certificate := state.PeerCertificates[0]
			got := sha256.Sum256(certificate.Raw)
			if subtle.ConstantTimeCompare(got[:], want) != 1 {
				return errors.New("command TLS certificate fingerprint mismatch")
			}
			if err := certificate.VerifyHostname(host); err != nil {
				return err
			}
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return errors.New("command TLS certificate is outside its validity period")
			}
			return nil
		},
	}}
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("command endpoint redirected")
		},
	}
	return NewCommandClient(origin, token, httpClient), nil
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

func (client *CommandClient) AuthenticateProfile(ctx context.Context, input CommandProfileAuthenticationRequest, output io.Writer) (CommandProfileAuthenticationResult, error) {
	var result CommandProfileAuthenticationResult
	encoded, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandProfileAuthenticationPath, bytes.NewReader(encoded))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/x-ndjson")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandProfileAuthenticationPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandProfileAuthenticationPath, response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var event profileAuthenticationEvent
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return result, io.ErrUnexpectedEOF
		} else if err != nil {
			return result, err
		}
		if event.Output != "" && output != nil {
			if _, err := io.WriteString(output, event.Output); err != nil {
				return result, err
			}
		}
		if event.Code != "" {
			return result, apperrors.New(event.Code, errors.New("profile authentication failed"))
		}
		if event.Result != nil {
			return *event.Result, nil
		}
	}
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
	if strings.HasPrefix(client.origin, "https://") {
		return unpinnedCommandDoer{}
	}
	return http.DefaultClient
}

type unpinnedCommandDoer struct{}

func (unpinnedCommandDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("HTTPS command client requires a certificate pin")
}

const CommandDashboardPath = "/api/v1/command/dashboard"

type vaultUnlockRequest struct {
	Passphrase string `json:"passphrase"`
}

type dashboardLink struct {
	URL string `json:"dashboard_url"`
}

// Dashboard issues a fresh one-time browser entry without restarting the owner.
func (client *CommandClient) Dashboard(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandDashboardPath, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("dashboard entry unavailable"))
	}
	var result dashboardLink
	err = json.NewDecoder(response.Body).Decode(&result)
	return result.URL, err
}

// ServiceHealth returns only the safe operational projection from the
// command-authorized service channel.
func (client *CommandClient) ServiceHealth(ctx context.Context) (ServiceHealth, error) {
	return client.vault(ctx, http.MethodGet, "")
}

// UnlockVault supplies a passphrase to the already-running owner for this
// process session. The passphrase is never accepted by the browser contract.
func (client *CommandClient) UnlockVault(ctx context.Context, passphrase string) (ServiceHealth, error) {
	return client.vault(ctx, http.MethodPost, passphrase)
}

func (client *CommandClient) vault(ctx context.Context, method, passphrase string) (ServiceHealth, error) {
	var result ServiceHealth
	var body io.Reader
	var encoded []byte
	if method == http.MethodPost {
		var err error
		encoded, err = json.Marshal(vaultUnlockRequest{Passphrase: passphrase})
		if err != nil {
			return result, err
		}
		defer clear(encoded)
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.origin+CommandVaultPath, body)
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
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("%s %s returned HTTP %d", method, CommandVaultPath, response.StatusCode))
		}
		return result, fmt.Errorf("%s %s returned HTTP %d", method, CommandVaultPath, response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}

func (server *Server) commandDashboard(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	token, err := server.randomBytes(randomTokenSize)
	if err != nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(token)
	server.mu.Lock()
	digest := sha256.Sum256([]byte(encoded))
	server.bootstrapTokens[digest] = server.clock.Now().UTC().Add(server.bootstrapTTL)
	link := server.origin + BootstrapPathName + "?" + BootstrapQueryName + "=" + encoded
	server.mu.Unlock()
	writeJSON(response, http.StatusOK, dashboardLink{URL: link})
}

func (server *Server) commandVault(response http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		writeJSON(response, http.StatusOK, server.health())
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	if server.serviceLifecycle == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxVaultUnlockBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input vaultUnlockRequest
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Passphrase) == "" {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.CLIUsage)
		return
	}
	if err := server.serviceLifecycle.Unlock(request.Context(), input.Passphrase); err != nil {
		input.Passphrase = ""
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.VaultLocked))
		return
	}
	input.Passphrase = ""
	writeJSON(response, http.StatusOK, server.health())
}
