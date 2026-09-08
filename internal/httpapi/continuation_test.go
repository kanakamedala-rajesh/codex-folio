package httpapi

import (
	"context"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
)

func TestCommandCheckpointCaptureAndShowUseAuthorizedService(t *testing.T) {
	service := &checkpointServiceStub{checkpoint: continuation.Checkpoint{ID: "checkpoint-1", Status: continuation.StatusDraft}}
	server, _, _ := startTestServer(t, Options{Checkpoints: service, CommandToken: "checkpoint-token"})
	client := NewCommandClient(server.Origin(), "checkpoint-token", nil)
	request := CommandCheckpointRequest{Action: "capture", Path: "/repo", Goal: "ship checkpoint"}
	result, err := client.Checkpoint(context.Background(), request)
	if err != nil || result.Checkpoint.ID != "checkpoint-1" || service.capture.Path != "/repo" {
		t.Fatalf("capture = %#v/%#v, %v", result, service.capture, err)
	}
	if _, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "show", ID: "checkpoint-1"}); err != nil || service.showID != "checkpoint-1" {
		t.Fatalf("show ID/error = %q/%v", service.showID, err)
	}
	if _, err := NewCommandClient(server.Origin(), "wrong", nil).Checkpoint(context.Background(), request); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized error = %v", err)
	}
}

type checkpointServiceStub struct {
	checkpoint continuation.Checkpoint
	capture    continuation.CaptureRequest
	showID     string
}

func (stub *checkpointServiceStub) Capture(_ context.Context, request continuation.CaptureRequest) (continuation.Checkpoint, error) {
	stub.capture = request
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Show(_ context.Context, id string) (continuation.Checkpoint, error) {
	stub.showID = id
	return stub.checkpoint, nil
}
