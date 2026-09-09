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
	fields := continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "edited"}}
	if _, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "edit", ID: "checkpoint-1", Fields: &fields, RedactText: []string{"secret"}}); err != nil || service.editID != "checkpoint-1" || service.edit.Fields.Goal.Value != "edited" {
		t.Fatalf("edit = %q/%#v, %v", service.editID, service.edit, err)
	}
	if _, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "approve", ID: "checkpoint-1", Revision: "revision-1"}); err != nil || service.approveID != "checkpoint-1" || service.revision != "revision-1" {
		t.Fatalf("approve = %q/%q, %v", service.approveID, service.revision, err)
	}
	if _, err := NewCommandClient(server.Origin(), "wrong", nil).Checkpoint(context.Background(), request); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized error = %v", err)
	}
}

type checkpointServiceStub struct {
	checkpoint continuation.Checkpoint
	capture    continuation.CaptureRequest
	showID     string
	editID     string
	edit       continuation.EditRequest
	approveID  string
	revision   string
}

func (stub *checkpointServiceStub) Capture(_ context.Context, request continuation.CaptureRequest) (continuation.Checkpoint, error) {
	stub.capture = request
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Show(_ context.Context, id string) (continuation.Checkpoint, error) {
	stub.showID = id
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Edit(_ context.Context, id string, request continuation.EditRequest) (continuation.Checkpoint, error) {
	stub.editID, stub.edit = id, request
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Approve(_ context.Context, id, revision string) (continuation.Checkpoint, error) {
	stub.approveID, stub.revision = id, revision
	return stub.checkpoint, nil
}
