package codex

import (
	"context"
	"io"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/profile"
)

func TestAuthenticatorDelegatesLoginWithoutCapturingCodexOutput(t *testing.T) {
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
		Discovery:    profile.Discovery{Executable: "/opt/codex/bin/codex"},
		IdentityHome: "/private/managed-homes/profile-1",
		Method:       profile.AuthMethodDeviceCode,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if executable != "/opt/codex/bin/codex" || strings.Join(args, " ") != "login --device-auth" {
		t.Fatalf("command = %q %v, want Codex device login", executable, args)
	}
	if !containsEnvironment(environment, "CODEX_HOME=/private/managed-homes/profile-1") {
		t.Fatalf("environment does not set target CODEX_HOME: %v", environment)
	}
}

func TestAuthenticatorUsesLoginStatusAndMapsFailureToNotAuthenticated(t *testing.T) {
	var args []string
	authenticator := NewAuthenticatorWithCommandRunner(func(_ context.Context, _ string, gotArgs, _ []string, _ io.Reader, _, _ io.Writer) error {
		args = append([]string(nil), gotArgs...)
		return io.ErrUnexpectedEOF
	})

	err := authenticator.Check(context.Background(), profile.AuthenticationRequest{
		Discovery:    profile.Discovery{Executable: "/opt/codex/bin/codex"},
		IdentityHome: "/private/managed-homes/profile-1",
	})
	if err != profile.ErrNotAuthenticated {
		t.Fatalf("Check() error = %v, want not-authenticated", err)
	}
	if strings.Join(args, " ") != "login status" {
		t.Fatalf("status command = %v, want login status", args)
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
