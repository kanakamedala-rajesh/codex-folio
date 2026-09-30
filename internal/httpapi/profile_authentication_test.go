package httpapi

import (
	"bytes"
	"context"
	"io"
	"testing"

	"venkatasudha.com/codex-folio/internal/profile"
)

type profileAuthenticationServiceStub struct{}

func (profileAuthenticationServiceStub) Authenticate(_ context.Context, request CommandProfileAuthenticationRequest, output io.Writer) (CommandProfileAuthenticationResult, error) {
	_, _ = io.WriteString(output, "device code: ABCD\n")
	result := profile.SetupResult{Profile: profile.IdentityProfile{ID: "profile-1", Alias: request.Alias, Status: profile.StatusReady, Selected: true}}
	return CommandProfileAuthenticationResult{Setup: &result}, nil
}

func TestCommandProfileAuthenticationStreamsOutputAndResult(t *testing.T) {
	server, _, _ := startTestServer(t, Options{ProfileAuthentication: profileAuthenticationServiceStub{}, CommandToken: "command-token"})
	var output bytes.Buffer
	result, err := NewCommandClient(server.Origin(), "command-token", nil).AuthenticateProfile(context.Background(), CommandProfileAuthenticationRequest{Action: "add", Alias: "Work"}, &output)
	if err != nil {
		t.Fatalf("AuthenticateProfile() error = %v", err)
	}
	if output.String() != "device code: ABCD\n" || result.Setup == nil || result.Setup.Profile.Alias != "Work" {
		t.Fatalf("output/result = %q/%#v", output.String(), result)
	}
	if operation := server.getProfileOperation("work"); operation.State != "ready" || operation.Code != "" {
		t.Fatalf("operation = %#v, want safe ready state", operation)
	}
}
