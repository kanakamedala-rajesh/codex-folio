package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

type diagnosticConfirmationReader struct {
	prompt *bytes.Buffer
	done   bool
}

func (reader *diagnosticConfirmationReader) Read(target []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	text := reader.prompt.String()
	start := strings.LastIndex(text, "Type ")
	end := strings.LastIndex(text, " to export this local diagnostic bundle")
	if start < 0 || end <= start+5 {
		return 0, io.EOF
	}
	return copy(target, text[start+5:end]+"\n"), nil
}

func TestDiagnosticsCLIConsumesPriorPreviewFromRunningOwner(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenWithVault(paths.DatabaseFile, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	diagnosticService, err := newDiagnosticService(state, false)
	if err != nil {
		t.Fatal(err)
	}
	const token = "diagnostic-running-owner-token"
	server, err := httpapi.NewServer(httpapi.Options{DiagnosticService: diagnosticService, CommandToken: token})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = state.Close()
		_ = owner.Close()
	})

	run := func(args []string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := runDiagnosticsWithDependencies(
			args, strings.NewReader(""), &stdout, &stderr,
			func(*string) (platform.Paths, error) { return paths, nil },
			func(platform.Paths, platform.VaultMode, string) (*store.Store, error) {
				return nil, errors.New("running-owner diagnostics must not open the store")
			},
			newServiceDiagnosticSink(),
		)
		return code, stdout.String(), stderr.String()
	}

	code, output, diagnostic := run([]string{"export", "--dry-run", "--json"})
	if code != exitSuccess || diagnostic != "" {
		t.Fatalf("preview = %d/%s/%s", code, output, diagnostic)
	}
	var preview httpapi.DiagnosticsResponse
	if err := json.Unmarshal([]byte(output), &preview); err != nil || preview.Preview == nil {
		t.Fatalf("preview JSON = %#v, error = %v", preview, err)
	}

	staleDestination := filepath.Join(t.TempDir(), "stale.json")
	code, _, diagnostic = run([]string{"export", "--output", staleDestination, "--confirm", "stale", "--non-interactive"})
	if code == exitSuccess || !strings.Contains(diagnostic, apperrors.DiagnosticsConfirmationInvalid) {
		t.Fatalf("stale confirmation = %d/%s", code, diagnostic)
	}
	if _, err := os.Stat(staleDestination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale confirmation destination exists: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "confirmed.json")
	code, _, diagnostic = run([]string{"export", "--output", destination, "--confirm", preview.Preview.ConfirmationDigest, "--non-interactive"})
	if code != exitSuccess || diagnostic != "" {
		t.Fatalf("confirmed prior preview = %d/%s", code, diagnostic)
	}
	encoded, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var exported httpapi.DiagnosticBundle
	if err := json.Unmarshal(encoded, &exported); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported, preview.Preview.Bundle) {
		t.Fatalf("exported bundle = %#v, want exact preview %#v", exported, preview.Preview.Bundle)
	}
}

func TestDiagnosticsCLIConfiguresPreviewsAndWritesOnlyConfirmedLocalBundle(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	opener := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithVault(paths.DatabaseFile, secureVault)
	}
	state, err := opener(paths, "", "")
	if err != nil {
		t.Fatal(err)
	}
	event, err := diagnostics.NewEvent(time.Now().UTC(), diagnostics.SeverityError, diagnostics.ComponentStore, apperrors.StoreReadFailed, diagnostics.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordDiagnostic(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	run := func(args []string, input io.Reader) (int, string, string) {
		var output, diagnostic bytes.Buffer
		if input == nil {
			input = strings.NewReader("")
		}
		code := runDiagnosticsWithDependencies(args, input, &output, &diagnostic, func(*string) (platform.Paths, error) { return paths, nil }, opener, newServiceDiagnosticSink())
		return code, output.String(), diagnostic.String()
	}
	code, output, diagnostic := run([]string{"settings", "--json"}, nil)
	if code != 0 || diagnostic != "" || !strings.Contains(output, `"enabled":true`) || !strings.Contains(output, `"retention_days":14`) {
		t.Fatalf("defaults = %d/%s/%s", code, output, diagnostic)
	}
	code, output, diagnostic = run([]string{"settings", "--enabled", "false", "--level", "warning", "--retention-days", "7", "--json"}, nil)
	if code != 0 || diagnostic != "" || !strings.Contains(output, `"enabled":false`) || !strings.Contains(output, `"minimum_level":"warning"`) {
		t.Fatalf("configured = %d/%s/%s", code, output, diagnostic)
	}
	code, output, diagnostic = run([]string{"export", "--dry-run"}, nil)
	if code != 0 || diagnostic != "" || !strings.Contains(output, "Diagnostic bundle fields:") || !strings.Contains(output, "Excluded: identities") {
		t.Fatalf("preview = %d/%s/%s", code, output, diagnostic)
	}
	code, _, diagnostic = run([]string{"export", "--output", filepath.Join(t.TempDir(), "unconfirmed.json"), "--non-interactive"}, nil)
	if code == 0 || !strings.Contains(diagnostic, apperrors.DiagnosticsConfirmationInvalid) {
		t.Fatalf("unconfirmed = %d/%s", code, diagnostic)
	}

	destination := filepath.Join(t.TempDir(), "diagnostics.json")
	var stdout, stderr bytes.Buffer
	reader := &diagnosticConfirmationReader{prompt: &stderr}
	code = runDiagnosticsWithDependencies([]string{"export", "--output", destination}, reader, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, opener, newServiceDiagnosticSink())
	if code != 0 {
		t.Fatalf("confirmed = %d/%s/%s", code, stdout.String(), stderr.String())
	}
	encoded, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Work", filepath.Dir(paths.DatabaseFile), "arguments", "analytics"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("bundle contains forbidden value %q: %s", forbidden, encoded)
		}
	}
	before := string(encoded)
	stderr.Reset()
	reader = &diagnosticConfirmationReader{prompt: &stderr}
	code = runDiagnosticsWithDependencies([]string{"export", "--output", destination}, reader, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }, opener, newServiceDiagnosticSink())
	if code == 0 || !strings.Contains(stderr.String(), apperrors.DiagnosticsExportFailed) {
		t.Fatalf("existing destination = %d/%s", code, stderr.String())
	}
	after, err := os.ReadFile(destination)
	if err != nil || string(after) != before {
		t.Fatalf("existing destination changed: %v", err)
	}
}
