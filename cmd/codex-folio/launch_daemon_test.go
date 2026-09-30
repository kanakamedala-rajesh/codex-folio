package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/launch"
)

func TestForegroundCodexArgumentsKeepsExistingLongHomeAndArgs(t *testing.T) {
	home := filepath.Join(t.TempDir(), strings.Repeat("a", 70))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "existing-auth-marker")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := launch.Plan{Executable: "codex", Arguments: []string{"-m", "model", "resume"}, Environment: map[string]string{"CODEX_HOME": home}}
	probes := 0
	probe := func(string) bool { probes++; return true }
	for attempt := 0; attempt < 2; attempt++ {
		arguments, err := foregroundCodexArguments(plan, "linux", probe)
		if err != nil || !slices.Equal(arguments, []string{"--no-daemon", "-m", "model", "resume"}) {
			t.Fatalf("attempt %d: arguments = %v, %v", attempt, arguments, err)
		}
	}
	if probes != 2 || !slices.Equal(plan.Arguments, []string{"-m", "model", "resume"}) {
		t.Fatalf("probes/original arguments = %d/%v", probes, plan.Arguments)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "unchanged" {
		t.Fatalf("existing home content changed: %v", err)
	}
	arguments, err := foregroundCodexArguments(plan, "windows", func(string) bool { t.Fatal("Windows must not probe Unix daemon support"); return false })
	if err != nil || !slices.Equal(arguments, plan.Arguments) {
		t.Fatalf("Windows arguments = %v, %v", arguments, err)
	}
	plan.Arguments = []string{"--no-daemon", "resume"}
	arguments, err = foregroundCodexArguments(plan, "linux", func(string) bool { t.Fatal("explicit flag must not probe"); return false })
	if err != nil || !slices.Equal(arguments, plan.Arguments) {
		t.Fatalf("explicit arguments = %v, %v", arguments, err)
	}
	plan.Arguments = []string{"--", "--no-daemon"}
	arguments, err = foregroundCodexArguments(plan, "linux", func(string) bool { return true })
	if err != nil || !slices.Equal(arguments, []string{"--no-daemon", "--", "--no-daemon"}) {
		t.Fatalf("literal prompt arguments = %v, %v", arguments, err)
	}
	_, err = foregroundCodexArguments(launch.Plan{Executable: "codex", Environment: plan.Environment}, "linux", func(string) bool { return false })
	if apperrors.Code(err) != apperrors.LaunchDaemonUnsupported {
		t.Fatalf("unsupported Codex error = %v", err)
	}
}

func TestForegroundCodexArgumentsResolvesLongHomeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink behavior")
	}
	home := filepath.Join(t.TempDir(), strings.Repeat("a", 70))
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	short, err := os.CreateTemp(os.TempDir(), "cf-*")
	if err != nil {
		t.Fatal(err)
	}
	link := short.Name()
	_ = short.Close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if len([]byte(link)) > maxDaemonSafeHomeBytes {
		t.Fatalf("symlink fixture is not short: %d bytes", len([]byte(link)))
	}
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	arguments, err := foregroundCodexArguments(launch.Plan{Executable: "codex", Environment: map[string]string{"CODEX_HOME": link}}, "linux", func(string) bool { return true })
	if err != nil || !slices.Equal(arguments, []string{"--no-daemon"}) {
		t.Fatalf("symlink arguments = %v, %v", arguments, err)
	}
}

func TestCodexSupportsNoDaemonFromInstalledHelp(t *testing.T) {
	name, supported, unsupported := "codex", "#!/bin/sh\nprintf '%s\\n' '      --no-daemon  Run without shared server'\n", "#!/bin/sh\nprintf '%s\\n' 'No daemon option'\n"
	if runtime.GOOS == "windows" {
		name, supported, unsupported = "codex.cmd", "@echo off\r\necho       --no-daemon  Run without shared server\r\n", "@echo off\r\necho No daemon option\r\n"
	}
	path := filepath.Join(t.TempDir(), name)
	for _, fixture := range []struct {
		content string
		want    bool
	}{{supported, true}, {unsupported, false}} {
		if err := os.WriteFile(path, []byte(fixture.content), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := codexSupportsNoDaemon(path); got != fixture.want {
			t.Fatalf("capability = %t, want %t", got, fixture.want)
		}
	}
}
