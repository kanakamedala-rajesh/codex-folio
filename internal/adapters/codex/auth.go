package codex

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/profile"
)

// CommandRunner is the process boundary used by Codex authentication. It
// deliberately exposes streams and environment only to the child process;
// the adapter never captures or interprets Codex output.
type CommandRunner func(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) error

// Authenticator delegates all authentication and status checks to Codex.
type Authenticator struct {
	run CommandRunner
}

func NewAuthenticator() *Authenticator {
	return NewAuthenticatorWithCommandRunner(runCommand)
}

func NewAuthenticatorWithCommandRunner(run CommandRunner) *Authenticator {
	if run == nil {
		run = runCommand
	}
	return &Authenticator{run: run}
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
		return profile.ErrNotAuthenticated
	}
	ctx = contextOrBackground(ctx)
	err := authenticator.run(ctx, request.Discovery.Executable, []string{"login", "status"}, codexEnvironment(request.IdentityHome), nil, io.Discard, io.Discard)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return profile.ErrNotAuthenticated
}

func validRequest(request profile.AuthenticationRequest) bool {
	return strings.TrimSpace(request.Discovery.Executable) != "" && filepath.IsAbs(request.IdentityHome)
}

func codexEnvironment(identityHome string) []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, "CODEX_HOME") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CODEX_HOME="+filepath.Clean(identityHome))
}

func runCommand(ctx context.Context, executable string, args []string, environment []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = environment
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
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
