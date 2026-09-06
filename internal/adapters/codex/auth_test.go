package codex

import (
	"bufio"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/profile"
)

func TestAuthenticatorDelegatesLoginWithoutCapturingCodexOutput(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	var executable string
	var args []string
	var environment []string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, gotExecutable string, gotArgs, gotEnvironment []string, _ io.Reader, _, _ io.Writer) error {
		executable = gotExecutable
		args = append([]string(nil), gotArgs...)
		environment = append([]string(nil), gotEnvironment...)
		return nil
	})

	err := authenticator.Authenticate(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
		Method:       profile.AuthMethodDeviceCode,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if executable != executablePath || strings.Join(args, " ") != "login --device-auth" {
		t.Fatalf("command = %q %v, want Codex device login", executable, args)
	}
	if !containsEnvironment(environment, "CODEX_HOME="+filepath.Clean(identityHome)) {
		t.Fatalf("environment does not set target CODEX_HOME: %v", environment)
	}
}

func TestAuthenticatorMapsAuthenticationStatusInterfaceFailureToUnavailable(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	var args []string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, gotArgs, _ []string, _ io.Reader, _, _ io.Writer) error {
		args = append([]string(nil), gotArgs...)
		return io.ErrUnexpectedEOF
	})

	err := authenticator.Check(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
	})
	if err != profile.ErrAuthenticationUnavailable {
		t.Fatalf("Check() error = %v, want authentication unavailable", err)
	}
	if strings.Join(args, " ") != "login status" {
		t.Fatalf("status command = %v, want login status", args)
	}
}

func TestAuthenticatorUsesAccountReadForUsableAuth(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	var args []string
	var input string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, gotArgs, _ []string, stdin io.Reader, stdout, _ io.Writer) error {
		args = append([]string(nil), gotArgs...)
		data, err := io.ReadAll(stdin)
		if err != nil {
			t.Fatal(err)
		}
		input = string(data)
		_, err = io.WriteString(stdout, `{"id":1,"result":{}}`+"\n"+`{"id":2,"result":{"account":{"type":"chatgpt"}}}`+"\n")
		return err
	})

	err := authenticator.Check(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if strings.Join(args, " ") != "app-server --stdio" {
		t.Fatalf("account command = %v, want app-server --stdio", args)
	}
	if !strings.Contains(input, `"method":"account/read"`) || !strings.Contains(input, `"refreshToken":false`) {
		t.Fatalf("account/read request = %q", input)
	}
}

func TestAppServerHandshakeWaitsForInitializeBeforeRequest(t *testing.T) {
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverInput)
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"initialize"`) {
			done <- errors.New("missing initialize request")
			return
		}
		_, _ = io.WriteString(serverOutput, `{"id":1,"result":{}}`+"\n")
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"initialized"`) {
			done <- errors.New("missing initialized notification")
			return
		}
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"account/read"`) {
			done <- errors.New("missing account request")
			return
		}
		_, _ = io.WriteString(serverOutput, `{"id":2,"result":{"account":{"type":"chatgpt"}}}`+"\n")
		_ = serverOutput.Close()
		done <- nil
	}()
	output, err := exchangeAppServer(clientInput, clientOutput, func() error { return <-done }, `{"method":"account/read","id":2}`, 2)
	if err != nil || !strings.Contains(string(output), `"account"`) {
		t.Fatalf("exchangeAppServer() = %q/%v", output, err)
	}
}

func TestAuthenticatorMapsNullAccountToNotAuthenticated(t *testing.T) {
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, _ []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
		_, err := io.WriteString(stdout, `{"id":2,"result":{"account":null}}`+"\n")
		return err
	})

	err := authenticator.Check(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: filepath.Join(t.TempDir(), "codex")},
		IdentityHome: filepath.Join(t.TempDir(), "managed-homes", "profile-1"),
	})
	if err != profile.ErrNotAuthenticated {
		t.Fatalf("Check() error = %v, want not-authenticated", err)
	}
}

func TestAuthenticatorFakeCodexSupportsExpiryRecoveryAndRepeatedReuse(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	authenticated := false
	var calls []string
	var homes []string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, gotExecutable string, args, environment []string, _ io.Reader, stdout, _ io.Writer) error {
		if gotExecutable != executablePath {
			t.Fatalf("executable = %q, want %q", gotExecutable, executablePath)
		}
		calls = append(calls, strings.Join(args, " "))
		homes = append(homes, environmentValue(environment, "CODEX_HOME"))
		switch strings.Join(args, " ") {
		case "app-server --stdio":
			account := `{"id":2,"result":{"account":{"type":"chatgpt"}}}`
			if !authenticated {
				account = `{"id":2,"result":{"account":null}}`
			}
			_, err := io.WriteString(stdout, account+"\n")
			return err
		case "login --device-auth":
			authenticated = true
			return nil
		default:
			return io.ErrUnexpectedEOF
		}
	})
	request := profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
		Method:       profile.AuthMethodDeviceCode,
	}
	if err := authenticator.Check(context.Background(), request); err != profile.ErrNotAuthenticated {
		t.Fatalf("expired Check() error = %v, want not-authenticated", err)
	}
	if err := authenticator.Authenticate(context.Background(), request); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if err := authenticator.Check(context.Background(), request); err != nil {
		t.Fatalf("recovered Check() error = %v", err)
	}
	if err := authenticator.Check(context.Background(), request); err != nil {
		t.Fatalf("repeated Check() error = %v", err)
	}
	if strings.Join(calls, ",") != "app-server --stdio,login --device-auth,app-server --stdio,app-server --stdio" {
		t.Fatalf("fake Codex calls = %v, want expiry, device login, and repeated reuse", calls)
	}
	for _, home := range homes {
		if home != filepath.Clean(identityHome) {
			t.Fatalf("CODEX_HOME = %q, want existing home %q", home, filepath.Clean(identityHome))
		}
	}
}

func TestAuthenticatorFakeCodexSupportsBrowserRecovery(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	authenticated := false
	var calls []string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, args, environment []string, _ io.Reader, stdout, _ io.Writer) error {
		calls = append(calls, strings.Join(args, " "))
		if environmentValue(environment, "CODEX_HOME") != filepath.Clean(identityHome) {
			t.Fatalf("CODEX_HOME does not target the existing Identity Home")
		}
		switch strings.Join(args, " ") {
		case "app-server --stdio":
			account := `{"id":2,"result":{"account":null}}`
			if authenticated {
				account = `{"id":2,"result":{"account":{"type":"chatgpt"}}}`
			}
			_, err := io.WriteString(stdout, account+"\n")
			return err
		case "login":
			authenticated = true
			return nil
		default:
			return io.ErrUnexpectedEOF
		}
	})
	request := profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
		Method:       profile.AuthMethodBrowser,
	}
	if err := authenticator.Check(context.Background(), request); err != profile.ErrNotAuthenticated {
		t.Fatalf("expired Check() error = %v, want not-authenticated", err)
	}
	if err := authenticator.Authenticate(context.Background(), request); err != nil {
		t.Fatalf("browser Authenticate() error = %v", err)
	}
	if err := authenticator.Check(context.Background(), request); err != nil {
		t.Fatalf("recovered Check() error = %v", err)
	}
	if strings.Join(calls, ",") != "app-server --stdio,login,app-server --stdio" {
		t.Fatalf("fake Codex calls = %v, want expiry, browser login, and recovery", calls)
	}
}

func TestAuthenticatorFakeCodexMapsCancellationAndFailure(t *testing.T) {
	request := profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: filepath.Join(t.TempDir(), "codex")},
		IdentityHome: filepath.Join(t.TempDir(), "managed-homes", "profile-1"),
		Method:       profile.AuthMethodDeviceCode,
	}

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		authenticator := NewAuthenticatorWithCommandRunner(func(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) error {
			cancel()
			return errors.New("fake Codex cancelled")
		})
		if err := authenticator.Authenticate(ctx, request); !errors.Is(err, context.Canceled) {
			t.Fatalf("Authenticate() error = %v, want cancellation", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		authenticator := NewAuthenticatorWithCommandRunner(func(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) error {
			return errors.New("fake Codex failed")
		})
		if err := authenticator.Authenticate(context.Background(), request); err != profile.ErrAuthenticationFailed {
			t.Fatalf("Authenticate() error = %v, want authentication failure", err)
		}
	})
}

func TestAuthenticatorReportsDocumentedMetadataUnavailableWithoutInference(t *testing.T) {
	metadata, err := (&Authenticator{}).ObserveDocumentedMetadata(context.Background(), profile.AuthenticationRequest{})
	if err != profile.ErrDocumentedMetadataUnavailable {
		t.Fatalf("ObserveDocumentedMetadata() error = %v, want metadata unavailable", err)
	}
	if metadata != (profile.DocumentedMetadata{}) {
		t.Fatalf("metadata = %#v, want empty metadata", metadata)
	}
}

func TestAuthenticatorDoesNotInferDocumentedMetadataWithoutAccountID(t *testing.T) {
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, _ []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
		_, err := io.WriteString(stdout, `{"id":3,"result":{"rateLimits":{}}}`+"\n")
		return err
	})

	metadata, err := authenticator.ObserveDocumentedMetadata(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: filepath.Join(t.TempDir(), "codex")},
		IdentityHome: filepath.Join(t.TempDir(), "managed-homes", "profile-1"),
	})
	if err != profile.ErrDocumentedMetadataUnavailable {
		t.Fatalf("ObserveDocumentedMetadata() error = %v, want metadata unavailable", err)
	}
	if metadata != (profile.DocumentedMetadata{}) {
		t.Fatalf("metadata = %#v, want empty metadata", metadata)
	}
}

func TestAuthenticatorReadsDocumentedWorkspaceFromRateLimits(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "codex")
	identityHome := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	var args []string
	var input string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, gotArgs, _ []string, stdin io.Reader, stdout, _ io.Writer) error {
		args = append([]string(nil), gotArgs...)
		data, err := io.ReadAll(stdin)
		if err != nil {
			t.Fatal(err)
		}
		input = string(data)
		_, err = io.WriteString(stdout, `{"id":3,"result":{"accountId":"workspace-1"}}`+"\n")
		return err
	})

	metadata, err := authenticator.ObserveDocumentedMetadata(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: executablePath},
		IdentityHome: identityHome,
	})
	if err != nil {
		t.Fatalf("ObserveDocumentedMetadata() error = %v", err)
	}
	if metadata != (profile.DocumentedMetadata{Workspace: "workspace-1"}) {
		t.Fatalf("metadata = %#v, want workspace metadata", metadata)
	}
	if strings.Join(args, " ") != "app-server --stdio" {
		t.Fatalf("account command = %v, want app-server --stdio", args)
	}
	if !strings.Contains(input, `"method":"account/rateLimits/read"`) {
		t.Fatalf("rate limits request = %q", input)
	}
}

func containsEnvironment(environment []string, want string) bool {
	for _, entry := range environment {
		if entry == want {
			return true
		}
	}
	return false
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
