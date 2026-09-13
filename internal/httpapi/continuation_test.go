package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/profile"
)

func TestBrowserHandoffCapturesRepositoryFirstDraftAndRejectsStaleApproval(t *testing.T) {
	service := &checkpointServiceStub{rejectRevisionMismatch: true, checkpoint: continuation.Checkpoint{
		ID: "checkpoint-1", Status: continuation.StatusDraft, Revision: "revision-2",
		Project: continuation.Project{ID: "project-1", Alias: "Atlas", Basename: "atlas"},
		Source:  continuation.SourceRepositoryFirst,
		Fields:  continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "Ship handoff"}},
	}}
	registry, err := profile.NewRegistry(&registryRepository{profiles: []profile.IdentityProfile{
		{ID: "source-profile", Alias: "Personal", Status: profile.StatusReady, IdentityHomeID: "home-1"},
		{ID: "target-profile", Alias: "Work", Status: profile.StatusReady, IdentityHomeID: "home-2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{Checkpoints: service, Profiles: registry, CommandToken: "command-token"})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	withoutCSRF, err := doRequest(client, http.MethodPost, origin+"/api/v1/handoff", server.Address(), origin, []byte(`{"action":"capture","project_id":"project-1","target_alias":"Work","fields":{"goal":"Ship handoff","completed_work":"","pending_work":"","known_validation":"","risks":"","next_action":""}}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("capture without CSRF status = %d, want %d", withoutCSRF.StatusCode, http.StatusForbidden)
	}
	_ = withoutCSRF.Body.Close()

	captured, err := doRequest(client, http.MethodPost, origin+"/api/v1/handoff", server.Address(), origin, []byte(`{"action":"capture","project_id":"project-1","target_alias":"Work","fields":{"goal":"Ship handoff","completed_work":"","pending_work":"","known_validation":"","risks":"","next_action":""}}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	capturedBody := readBody(t, captured)
	if captured.StatusCode != http.StatusOK || !strings.Contains(capturedBody, `"source_state":"exited"`) || !strings.Contains(capturedBody, `"target_eligible":true`) || !strings.Contains(capturedBody, `"revision":"revision-2"`) || strings.Contains(capturedBody, `"staged":null`) || strings.Contains(capturedBody, `"modified":null`) || strings.Contains(capturedBody, `"untracked":null`) {
		t.Fatalf("capture status/body = %d/%s", captured.StatusCode, capturedBody)
	}
	if service.captureProjectID != "project-1" || service.capture.Goal != "Ship handoff" {
		t.Fatalf("browser capture = %q/%#v", service.captureProjectID, service.capture)
	}

	service.checkpoint.Revision = "revision-3"
	stale, err := doRequest(client, http.MethodPost, origin+"/api/v1/handoff", server.Address(), origin, []byte(`{"action":"approve","checkpoint_id":"checkpoint-1","revision":"revision-2","target_alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	staleBody := readBody(t, stale)
	if stale.StatusCode != http.StatusConflict || !strings.Contains(staleBody, `"code":"CF_CONTINUATION_CHECKPOINT_INVALID"`) {
		t.Fatalf("stale approval status/body = %d/%s", stale.StatusCode, staleBody)
	}
}

func TestCommandCheckpointCaptureAndShowUseAuthorizedService(t *testing.T) {
	service := &checkpointServiceStub{checkpoint: continuation.Checkpoint{ID: "checkpoint-1", Status: continuation.StatusDraft}}
	server, _, _ := startTestServer(t, Options{Checkpoints: service, CommandToken: "checkpoint-token"})
	client := NewCommandClient(server.Origin(), "checkpoint-token", nil)
	request := CommandCheckpointRequest{Action: "capture", Path: "/repo", Goal: "ship checkpoint"}
	retention, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "retention", Source: continuation.SourceRepositoryFirst, Setting: "1"})
	if err != nil || retention.Retention == nil || retention.Retention.RepositoryFirst != "1" {
		t.Fatalf("retention = %#v, %v", retention.Retention, err)
	}
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
	exported, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "export", ID: "checkpoint-1"})
	if err != nil || exported.Export == nil || exported.Export.Checkpoint.ID != "checkpoint-1" || service.exportID != "checkpoint-1" {
		t.Fatalf("export = %#v/%q, %v", exported.Export, service.exportID, err)
	}
	history, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "history-source", ID: "checkpoint-1", Revision: "revision-1"})
	if err != nil || history.HistorySource == nil || history.HistorySource.IdentityHome != "/source-home" {
		t.Fatalf("history source = %#v, %v", history.HistorySource, err)
	}
	preview, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "preview-assisted", ID: "checkpoint-1", Revision: "revision-1", Fields: &fields, RedactText: []string{"secret"}})
	if err != nil || preview.Checkpoint.ID != "checkpoint-1" || service.previewID != "checkpoint-1" {
		t.Fatalf("assisted preview = %#v/%q, %v", preview.Checkpoint, service.previewID, err)
	}
	if _, err := client.Checkpoint(context.Background(), CommandCheckpointRequest{Action: "approve-assisted", ID: "checkpoint-1", Revision: "revision-1", PreviewRevision: "preview-1", Fields: &fields, RedactText: []string{"secret"}}); err != nil || service.assistedID != "checkpoint-1" || service.previewRevision != "preview-1" {
		t.Fatalf("assisted approval = %q/%q, %v", service.assistedID, service.previewRevision, err)
	}
	if _, err := NewCommandClient(server.Origin(), "wrong", nil).Checkpoint(context.Background(), request); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized error = %v", err)
	}
}

type checkpointServiceStub struct {
	checkpoint             continuation.Checkpoint
	capture                continuation.CaptureRequest
	showID                 string
	editID                 string
	edit                   continuation.EditRequest
	approveID              string
	exportID               string
	revision               string
	previewID              string
	assistedID             string
	previewRevision        string
	captureProjectID       string
	source                 continuation.SourceLaunch
	rejectRevisionMismatch bool
}

func (stub *checkpointServiceStub) CaptureProject(_ context.Context, projectID string, request continuation.CaptureRequest) (continuation.Checkpoint, error) {
	stub.captureProjectID, stub.capture = projectID, request
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Source(context.Context, string) (continuation.SourceLaunch, error) {
	if stub.source.State == "" {
		return continuation.SourceLaunch{ProfileID: "source-profile", State: continuation.SourceExited}, nil
	}
	return stub.source, nil
}

func (stub *checkpointServiceStub) Retention(_ context.Context, source, setting string) (continuation.RetentionPolicy, error) {
	return continuation.RetentionPolicy{RepositoryFirst: setting, TranscriptAssisted: "7"}, nil
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
	if stub.rejectRevisionMismatch && revision != stub.checkpoint.Revision {
		return continuation.Checkpoint{}, continuation.ErrCheckpointRevisionChanged
	}
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) Export(_ context.Context, id string) (continuation.CheckpointExport, error) {
	stub.exportID = id
	return continuation.CheckpointExport{FormatVersion: continuation.CheckpointExportVersion, Checkpoint: stub.checkpoint}, nil
}

func (stub *checkpointServiceStub) PrepareHistory(context.Context, string, string) (continuation.HistorySource, error) {
	return continuation.HistorySource{SourceProfileID: "source-profile", IdentityHome: "/source-home"}, nil
}

func (stub *checkpointServiceStub) PreviewAssisted(_ context.Context, id, _ string, _ continuation.EditRequest) (continuation.Checkpoint, error) {
	stub.previewID = id
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) ApproveAssisted(_ context.Context, id, _, previewRevision string, _ continuation.EditRequest) (continuation.Checkpoint, error) {
	stub.assistedID, stub.previewRevision = id, previewRevision
	return stub.checkpoint, nil
}
