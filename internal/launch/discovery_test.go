package launch

import "testing"

type candidateResolver struct {
	candidate Candidate
	err       error
}

func (resolver candidateResolver) Resolve(string) (Candidate, error) {
	return resolver.candidate, resolver.err
}

func TestDiscoverReportsCapabilityLevelsAfterExecutableValidation(t *testing.T) {
	report, err := Discover(candidateResolver{candidate: Candidate{
		Path:    "/opt/codex/bin/codex",
		Version: "0.1.2",
	}}, "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if report.Executable != "/opt/codex/bin/codex" || report.Version != "0.1.2" {
		t.Fatalf("report = %#v, want resolved executable and version", report)
	}
	if report.Capabilities.TransparentLaunch != CapabilitySupported {
		t.Fatalf("transparent launch = %q, want supported", report.Capabilities.TransparentLaunch)
	}
	if report.Capabilities.Metadata != CapabilityDegraded {
		t.Fatalf("metadata = %q, want degraded", report.Capabilities.Metadata)
	}
	if report.Capabilities.Experimental != CapabilityDisabled {
		t.Fatalf("experimental = %q, want disabled", report.Capabilities.Experimental)
	}
}

func TestDiscoverRejectsMalformedResolverResults(t *testing.T) {
	for _, candidate := range []Candidate{
		{Version: "0.1.2"},
		{Path: "/opt/codex/bin/codex"},
	} {
		if _, err := Discover(candidateResolver{candidate: candidate}, ""); err == nil {
			t.Fatalf("Discover(%#v) error = nil, want validation failure", candidate)
		}
	}
}
