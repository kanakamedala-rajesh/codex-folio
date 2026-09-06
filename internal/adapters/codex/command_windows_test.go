//go:build windows

package codex

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandInvokesWindowsBatchShimWithoutInterpretingArguments(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "fake codex.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\nif not \"%~1\"==\"hello world\" exit /b 2\r\nif not \"%~2\"==\"a&b\" exit /b 3\r\necho hello world\r\necho a^&b\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), shim, []string{"hello world", "a&b"}, os.Environ(), nil, &output, &output); err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}
	if strings.ReplaceAll(output.String(), "\r\n", "\n") != "hello world\na&b\n" {
		t.Fatalf("batch output = %q", output.String())
	}
}
