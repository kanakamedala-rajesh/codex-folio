package codex

import (
	"context"
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

func TestAuthenticatorUsesLoginStatusAndMapsFailureToNotAuthenticated(t *testing.T) {
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
	if err != profile.ErrNotAuthenticated {
		t.Fatalf("Check() error = %v, want not-authenticated", err)
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
