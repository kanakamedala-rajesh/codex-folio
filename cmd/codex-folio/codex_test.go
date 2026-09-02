package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
)

type fakeCodexResolver struct {
	candidate launch.Candidate
	err       error
}

func (resolver fakeCodexResolver) Resolve(string) (launch.Candidate, error) {
	return resolver.candidate, resolver.err
}

func TestCodexDiscoverJSONKeepsDiagnosticsOffStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	resolver := fakeCodexResolver{candidate: launch.Candidate{
		Path:    "/opt/codex/bin/codex",
		Version: "0.1.2",
	}}

	if exitCode := runWithServicePathResolverAndCodexResolver(
		[]string{"codex", "discover", "--codex-bin", "/opt/codex/bin/codex", "--json"},
		&stdout,
		&stderr,
		buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return platform.Paths{}, nil },
		resolver,
	); exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr = %q", exitCode, exitSuccess, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var report launch.Discovery
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("JSON output error = %v; output = %q", err, stdout.String())
	}
	if report.Executable != resolver.candidate.Path || report.Version != resolver.candidate.Version {
		t.Fatalf("report = %#v, want resolver result", report)
	}
	if report.Capabilities.TransparentLaunch != launch.CapabilitySupported || report.Capabilities.Metadata != launch.CapabilityDegraded || report.Capabilities.Experimental != launch.CapabilityDisabled {
		t.Fatalf("capabilities = %#v, want supported/degraded/disabled", report.Capabilities)
	}
}

func TestCodexDiscoverHumanOutputReportsCompatibilityLevels(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolverAndCodexResolver(
		[]string{"codex", "discover"},
		&stdout,
		&stderr,
		buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return platform.Paths{}, nil },
		fakeCodexResolver{candidate: launch.Candidate{Path: "/opt/codex/bin/codex", Version: "0.1.2"}},
	); exitCode != exitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr = %q", exitCode, exitSuccess, stderr.String())
	}
	for _, want := range []string{
		"Codex executable: /opt/codex/bin/codex",
		"Codex version: 0.1.2",
		"transparent launch: supported",
		"metadata: degraded",
		"experimental: disabled",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestCodexDiscoverErrorIsStableAndDoesNotExposeCandidateDetails(t *testing.T) {
	const sentinel = "/private/codex/path-with-sensitive-detail"
	var stdout, stderr bytes.Buffer
	if exitCode := runWithServicePathResolverAndCodexResolver(
		[]string{"codex", "discover", "--codex-bin", sentinel},
		&stdout,
		&stderr,
		buildinfo.Metadata{},
		func(*string) (platform.Paths, error) { return platform.Paths{}, nil },
		fakeCodexResolver{err: apperrors.New(apperrors.LaunchCodexPathInvalid, errors.New(sentinel))},
	); exitCode != exitFailure {
		t.Fatalf("exit code = %d, want %d; stderr = %q", exitCode, exitFailure, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), apperrors.LaunchCodexPathInvalid) {
		t.Fatalf("stderr = %q, want stable path error", stderr.String())
	}
	if strings.Contains(stderr.String(), sentinel) {
		t.Fatalf("stderr = %q, must not expose candidate detail", stderr.String())
	}
}
