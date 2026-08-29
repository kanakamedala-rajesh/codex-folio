package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"venkatasudha.com/codex-folio/internal/buildinfo"
)

func TestVersionJSONIsMachineReadable(t *testing.T) {
	t.Parallel()

	want := buildinfo.Metadata{
		Product:        buildinfo.ProductName,
		Command:        buildinfo.CommandName,
		Version:        buildinfo.Version,
		SourceRevision: "abc1234",
		BuildClass:     "development",
		Dirty:          "clean",
	}
	var stdout, stderr bytes.Buffer

	if exitCode := run([]string{"version", "--json"}, &stdout, &stderr, want); exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got buildinfo.Metadata
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("version output is not JSON: %v\noutput: %s", err, stdout.String())
	}
	if got != want {
		t.Fatalf("version JSON = %#v, want %#v", got, want)
	}
}

func TestVersionHumanOutputContainsBuildIdentity(t *testing.T) {
	t.Parallel()

	metadata := buildinfo.Metadata{
		Product:        buildinfo.ProductName,
		Command:        buildinfo.CommandName,
		Version:        buildinfo.Version,
		SourceRevision: "abc1234",
		BuildClass:     "development",
		Dirty:          "dirty",
	}
	var stdout, stderr bytes.Buffer

	if exitCode := run([]string{"--version"}, &stdout, &stderr, metadata); exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr = %q", exitCode, stderr.String())
	}

	for _, want := range []string{
		buildinfo.ProductName + " " + buildinfo.Version,
		"source revision: abc1234",
		"build classification: development",
		"working tree: dirty",
	} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Errorf("human version output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestUnknownCommandUsesUsageExitCode(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"profiles"}, &stdout, &stderr, buildinfo.Metadata{}); exitCode != exitUsage {
		t.Fatalf("run() exit code = %d, want %d", exitCode, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("unknown command")) {
		t.Fatalf("stderr = %q, want unknown-command diagnostic", stderr.String())
	}
}
