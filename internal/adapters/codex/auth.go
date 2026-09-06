package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/profile"
)

// CommandRunner is the process boundary used by Codex authentication. Login
// output is streamed to the caller; account/read output is captured only as a
// structured authentication response.
type CommandRunner func(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) error
type appServerRunner func(context.Context, string, []string, string, int) ([]byte, error)

// Authenticator delegates all authentication and status checks to Codex.
type Authenticator struct {
	run       CommandRunner
	appServer appServerRunner
}

var errAccountReadUnavailable = errors.New("Codex account/read is unavailable")

func NewAuthenticator() *Authenticator {
	return &Authenticator{run: runCommand, appServer: runAppServer}
}

func NewAuthenticatorWithCommandRunner(run CommandRunner) *Authenticator {
	if run == nil {
		run = runCommand
	}
	return &Authenticator{run: run, appServer: appServerWithCommandRunner(run)}
}

func (authenticator *Authenticator) Authenticate(ctx context.Context, request profile.AuthenticationRequest) error {
	if authenticator == nil || authenticator.run == nil || !validRequest(request) {
		return profile.ErrAuthenticationFailed
	}
	ctx = contextOrBackground(ctx)
	if request.Method != profile.AuthMethodBrowser && request.Method != profile.AuthMethodDeviceCode {
		return profile.ErrAuthenticationFailed
	}
	args := []string{"login"}
	if request.Method == profile.AuthMethodDeviceCode {
		args = append(args, "--device-auth")
	}
	err := authenticator.run(ctx, request.Discovery.Executable, args, codexEnvironment(request.IdentityHome), request.Stdin, outputOrDiscard(request.Stdout), outputOrDiscard(request.Stderr))
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if request.Method == profile.AuthMethodBrowser {
		// A non-zero browser login is the only safe signal available without
		// capturing provider output. Automatic setup can then try device auth.
		return profile.ErrBrowserUnavailable
	}
	return profile.ErrAuthenticationFailed
}

func (authenticator *Authenticator) Check(ctx context.Context, request profile.AuthenticationRequest) error {
	if authenticator == nil || authenticator.run == nil || !validRequest(request) {
		return profile.ErrAuthenticationUnavailable
	}
	ctx = contextOrBackground(ctx)
	authenticated, err := authenticator.readAccount(ctx, request)
	if err == nil {
		if authenticated {
			return nil
		}
		return profile.ErrNotAuthenticated
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	err = authenticator.run(ctx, request.Discovery.Executable, []string{"login", "status"}, codexEnvironment(request.IdentityHome), nil, io.Discard, io.Discard)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return profile.ErrAuthenticationUnavailable
}

func (authenticator *Authenticator) ObserveDocumentedMetadata(ctx context.Context, request profile.AuthenticationRequest) (profile.DocumentedMetadata, error) {
	if authenticator == nil || authenticator.run == nil || !validRequest(request) {
		return profile.DocumentedMetadata{}, profile.ErrDocumentedMetadataUnavailable
	}
	ctx = contextOrBackground(ctx)
	workspace, err := authenticator.readWorkspaceAccount(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return profile.DocumentedMetadata{}, ctx.Err()
		}
		return profile.DocumentedMetadata{}, profile.ErrDocumentedMetadataUnavailable
	}
	if strings.TrimSpace(workspace) == "" {
		return profile.DocumentedMetadata{}, profile.ErrDocumentedMetadataUnavailable
	}
	return profile.DocumentedMetadata{Workspace: workspace}, nil
}

type accountReadResponse struct {
	ID     json.RawMessage `json:"id"`
	Result *struct {
		Account json.RawMessage `json:"account"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

type accountRateLimitsResponse struct {
	ID     json.RawMessage `json:"id"`
	Result *struct {
		AccountID *string `json:"accountId"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

func (authenticator *Authenticator) readAccount(ctx context.Context, request profile.AuthenticationRequest) (bool, error) {
	output, err := authenticator.appServer(ctx, request.Discovery.Executable, codexEnvironment(request.IdentityHome), `{"method":"account/read","id":2,"params":{"refreshToken":false}}`, 2)

	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var response accountReadResponse
		if json.Unmarshal(line, &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("2")) {
			continue
		}
		if len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
			return false, errAccountReadUnavailable
		}
		if response.Result == nil || len(response.Result.Account) == 0 || bytes.Equal(bytes.TrimSpace(response.Result.Account), []byte("null")) {
			return false, nil
		}
		return true, nil
	}
	if scanner.Err() != nil {
		return false, errAccountReadUnavailable
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, errAccountReadUnavailable
	}
	return false, errAccountReadUnavailable
}

func (authenticator *Authenticator) readWorkspaceAccount(ctx context.Context, request profile.AuthenticationRequest) (string, error) {
	output, err := authenticator.appServer(ctx, request.Discovery.Executable, codexEnvironment(request.IdentityHome), `{"method":"account/rateLimits/read","id":3}`, 3)

	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var response accountRateLimitsResponse
		if json.Unmarshal(line, &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("3")) {
			continue
		}
		if len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
			return "", errAccountReadUnavailable
		}
		if response.Result == nil || response.Result.AccountID == nil {
			return "", errAccountReadUnavailable
		}
		return strings.TrimSpace(*response.Result.AccountID), nil
	}
	if scanner.Err() != nil {
		return "", errAccountReadUnavailable
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errAccountReadUnavailable
	}
	return "", errAccountReadUnavailable
}

func validRequest(request profile.AuthenticationRequest) bool {
	return strings.TrimSpace(request.Discovery.Executable) != "" && filepath.IsAbs(request.IdentityHome)
}

func codexEnvironment(identityHome string) []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && sameEnvironmentName(name, "CODEX_HOME") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CODEX_HOME="+filepath.Clean(identityHome))
}

func runCommand(ctx context.Context, executable string, args []string, environment []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := codexCommand(ctx, executable, args...)
	command.Env = environment
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func appServerWithCommandRunner(run CommandRunner) appServerRunner {
	return func(ctx context.Context, executable string, environment []string, request string, _ int) ([]byte, error) {
		var output bytes.Buffer
		input := "{\"method\":\"initialize\",\"id\":1,\"params\":{\"clientInfo\":{\"name\":\"codex-folio\",\"version\":\"0.0.1-alpha\"}}}\n{\"method\":\"initialized\"}\n" + request + "\n"
		err := run(ctx, executable, []string{"app-server", "--stdio"}, environment, strings.NewReader(input), &output, io.Discard)
		return output.Bytes(), err
	}
}

func runAppServer(ctx context.Context, executable string, environment []string, request string, responseID int) ([]byte, error) {
	command := codexCommand(ctx, executable, "app-server", "--stdio")
	command.Env = environment
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return exchangeAppServer(stdin, stdout, command.Wait, request, responseID)
}

func exchangeAppServer(stdin io.WriteCloser, stdout io.Reader, wait func() error, request string, responseID int) ([]byte, error) {
	finish := func() error {
		_ = stdin.Close()
		return wait()
	}
	if _, err := io.WriteString(stdin, "{\"method\":\"initialize\",\"id\":1,\"params\":{\"clientInfo\":{\"name\":\"codex-folio\",\"version\":\"0.0.1-alpha\"}}}\n"); err != nil {
		_ = finish()
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) == nil && bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("1")) {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		_ = finish()
		return nil, err
	}
	if _, err := io.WriteString(stdin, "{\"method\":\"initialized\"}\n"+request+"\n"); err != nil {
		_ = finish()
		return nil, err
	}
	wantID := []byte(fmt.Sprint(responseID))
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(line, &envelope) == nil && bytes.Equal(bytes.TrimSpace(envelope.ID), wantID) {
			waitErr := finish()
			return append(line, '\n'), waitErr
		}
	}
	waitErr := finish()
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	return nil, waitErr
}

func outputOrDiscard(output io.Writer) io.Writer {
	if output == nil {
		return io.Discard
	}
	return output
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

var _ profile.Authenticator = (*Authenticator)(nil)
var _ profile.DocumentedMetadataObserver = (*Authenticator)(nil)
