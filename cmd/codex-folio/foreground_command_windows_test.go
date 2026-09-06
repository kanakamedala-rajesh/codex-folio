//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForegroundCommandInvokesWindowsBatchShimWithoutInterpretingArguments(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "fake codex.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\nif not \"%~1\"==\"hello world\" exit /b 2\r\nif not \"%~2\"==\"a&b\" exit /b 3\r\necho hello world\r\necho a^&b\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := foregroundCommand(shim, "hello world", "a&b")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.ReplaceAll(output.String(), "\r\n", "\n") != "hello world\na&b\n" {
		t.Fatalf("batch output = %q", output.String())
	}
}
