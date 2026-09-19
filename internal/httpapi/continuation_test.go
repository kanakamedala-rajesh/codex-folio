package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
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

	service.source = continuation.SourceLaunch{ProfileID: "source-profile", State: continuation.SourceRunning}
	blocked, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"approve","checkpoint_id":"checkpoint-1","revision":"revision-2","target_alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.StatusCode != http.StatusConflict || service.approveID != "" {
		t.Fatalf("approval with running source = %d, approve call %q", blocked.StatusCode, service.approveID)
	}
	_ = blocked.Body.Close()
	service.source = continuation.SourceLaunch{ProfileID: "source-profile", State: continuation.SourceExited}

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

func TestBrowserHandoffReadsHistoryOnlyAfterPerHandoffConsentAndPersistsOnlyApprovedPreview(t *testing.T) {
	base := continuation.Checkpoint{
		ID: "checkpoint-1", Status: continuation.StatusDraft, Revision: "repository-revision",
		Project: continuation.Project{ID: "project-1", Alias: "Atlas", Basename: "atlas"}, Source: continuation.SourceRepositoryFirst,
		Fields: continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "repository goal"}},
	}
	preview := base
	preview.Source, preview.Revision = continuation.SourceTranscriptAssisted, "preview-revision"
	preview.Fields.Goal = continuation.Evidence[string]{Value: "history goal", Provenance: continuation.ProvenanceModelDerived}
	service := &checkpointServiceStub{checkpoint: base, assistedPreview: preview}
	history := &browserHistoryStub{fields: continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "history goal from /source-home/projects/atlas", Provenance: continuation.ProvenanceModelDerived}}}
	registry, err := profile.NewRegistry(&registryRepository{profiles: []profile.IdentityProfile{
		{ID: "source-profile", Alias: "Personal", Status: profile.StatusReady, IdentityHomeID: "home-1"},
		{ID: "target-profile", Alias: "Work", Status: profile.StatusReady, IdentityHomeID: "home-2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{Checkpoints: service, CheckpointHistory: history, Profiles: registry, CommandToken: "command-token"})
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

	withoutConsent, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"assist","checkpoint_id":"checkpoint-1","revision":"repository-revision","thread_id":"11111111-1111-4111-8111-111111111111","target_alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if withoutConsent.StatusCode != http.StatusBadRequest || history.calls != 0 {
		t.Fatalf("assist without consent = %d, history calls %d", withoutConsent.StatusCode, history.calls)
	}
	_ = withoutConsent.Body.Close()
	service.prepareHistoryErr = continuation.ErrHandoffNotReady
	blocked, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"assist","checkpoint_id":"checkpoint-1","revision":"repository-revision","thread_id":"11111111-1111-4111-8111-111111111111","history_consent":true,"target_alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.StatusCode != http.StatusConflict || history.calls != 0 || service.prepareHistoryCalls != 1 {
		t.Fatalf("blocked history preparation = %d, history calls %d, preparation calls %d", blocked.StatusCode, history.calls, service.prepareHistoryCalls)
	}
	_ = blocked.Body.Close()
	service.prepareHistoryErr = nil

	assisted, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"assist","checkpoint_id":"checkpoint-1","revision":"repository-revision","thread_id":"11111111-1111-4111-8111-111111111111","history_consent":true,"target_alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, assisted)
	if assisted.StatusCode != http.StatusOK || !strings.Contains(body, `"source":"transcript-assisted"`) || !strings.Contains(body, `"revision":"preview-revision"`) || !strings.Contains(body, `history goal from [HOME]/projects/atlas`) || strings.Contains(body, "/source-home") || service.previewEdit.Fields.Goal.Value != "history goal from [HOME]/projects/atlas" || history.calls != 1 || service.saveCalls != 0 {
		t.Fatalf("assisted preview = %d/%s, history calls %d, saves %d", assisted.StatusCode, body, history.calls, service.saveCalls)
	}

	approved, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"approve-assisted","checkpoint_id":"checkpoint-1","revision":"repository-revision","preview_revision":"preview-revision","target_alias":"Work","fields":{"goal":"sanitized goal","completed_work":"","pending_work":"","known_validation":"","risks":"","next_action":""},"redact_text":["secret"]}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	_ = readBody(t, approved)
	if approved.StatusCode != http.StatusOK || service.assistedID != "checkpoint-1" || service.previewRevision != "preview-revision" || service.assistedEdit.Fields.Goal.Value != "sanitized goal" {
		t.Fatalf("approved assisted = %d, service %#v", approved.StatusCode, service)
	}
}

func TestBrowserCheckpointManagementListsRetainsPurgesAndExportsWithoutPaths(t *testing.T) {
	checkpoint := continuation.Checkpoint{
		ID: "checkpoint-1", Status: continuation.StatusApproved, Revision: "revision-1", Source: continuation.SourceTranscriptAssisted,
		Project: continuation.Project{ID: "project-1", Alias: "Atlas", Basename: "atlas"},
		Fields:  continuation.CheckpointFields{Goal: continuation.Evidence[string]{Value: "sanitized goal"}},
	}
	service := &checkpointServiceStub{checkpoint: checkpoint, checkpoints: []continuation.Checkpoint{checkpoint}, retentionPolicy: continuation.RetentionPolicy{RepositoryFirst: "30", TranscriptAssisted: "7"}}
	server, _, _ := startTestServer(t, Options{Checkpoints: service, CommandToken: "command-token"})
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
	post := func(body string) CheckpointManagementResponse {
		t.Helper()
		response, requestErr := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(body), bootstrap.CSRFToken)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("management status/body = %d/%s", response.StatusCode, readBody(t, response))
		}
		var envelope HandoffResult
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if envelope.Kind != "management" || envelope.Management == nil || envelope.Handoff != nil {
			t.Fatalf("management envelope = %#v", envelope)
		}
		return *envelope.Management
	}

	listed := post(`{"action":"list"}`)
	if listed.Checkpoints == nil || len(*listed.Checkpoints) != 1 || (*listed.Checkpoints)[0].State != "recoverable" || strings.Contains(mustJSON(t, listed), "IdentityHome") {
		t.Fatalf("checkpoint list = %#v", listed.Checkpoints)
	}
	retention := post(`{"action":"retention"}`)
	if retention.RetentionPolicy == nil || retention.RetentionPolicy.RepositoryFirst != "30" || retention.RetentionPolicy.TranscriptAssisted != "7" {
		t.Fatalf("retention = %#v", retention.RetentionPolicy)
	}
	purgePreview := post(`{"action":"purge-preview","checkpoint_id":"checkpoint-1","revision":"revision-1"}`)
	if purgePreview.OperationPreview == nil || purgePreview.OperationPreview.Confirmation == "" {
		t.Fatalf("purge preview = %#v", purgePreview.OperationPreview)
	}
	post(`{"action":"purge","checkpoint_id":"checkpoint-1","revision":"revision-1","confirmation":"` + purgePreview.OperationPreview.Confirmation + `"}`)
	if service.purgedRevision != "checkpoint-1/revision-1" {
		t.Fatalf("purged revision = %q", service.purgedRevision)
	}
	exportPreview := post(`{"action":"export-preview","checkpoint_id":"checkpoint-1","revision":"revision-1"}`)
	if exportPreview.OperationPreview == nil || exportPreview.OperationPreview.Filename != "checkpoint-checkpoint-1.cfolio" || !slices.Contains(exportPreview.OperationPreview.IncludedFields, "project_alias") || !slices.Contains(exportPreview.OperationPreview.ExcludedFields, "identity_homes") || slices.Contains(exportPreview.OperationPreview.IncludedFields, "project alias") || slices.Contains(exportPreview.OperationPreview.ExcludedFields, "Identity Homes") {
		t.Fatalf("export preview = %#v", exportPreview.OperationPreview)
	}
	invalidExport, err := doRequest(client, http.MethodPost, origin+HandoffPath, server.Address(), origin, []byte(`{"action":"export-encrypted","checkpoint_id":"checkpoint-1","revision":"revision-1","confirmation":"`+exportPreview.OperationPreview.Confirmation+`"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	invalidBody := readBody(t, invalidExport)
	if invalidExport.StatusCode != http.StatusBadRequest || !strings.Contains(invalidBody, `"code":"CF_CONTINUATION_CHECKPOINT_INVALID"`) {
		t.Fatalf("invalid export status/body = %d/%s", invalidExport.StatusCode, invalidBody)
	}
	download := post(`{"action":"export-encrypted","checkpoint_id":"checkpoint-1","revision":"revision-1","confirmation":"` + exportPreview.OperationPreview.Confirmation + `","passphrase":"correct horse battery staple"}`)
	if download.Download == nil || download.Download.MediaType != "application/vnd.codex-folio.checkpoint+json" {
		t.Fatalf("download = %#v", download.Download)
	}
	artifact, err := base64.StdEncoding.DecodeString(download.Download.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := continuation.OpenCheckpointExport(artifact, "correct horse battery staple")
	if err != nil || opened.Checkpoint.Fields.Goal.Value != "sanitized goal" {
		t.Fatalf("OpenCheckpointExport() = %#v, %v", opened, err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
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
	assistedPreview        continuation.Checkpoint
	previewEdit            continuation.EditRequest
	assistedEdit           continuation.EditRequest
	saveCalls              int
	checkpoints            []continuation.Checkpoint
	retentionPolicy        continuation.RetentionPolicy
	purgedRevision         string
	captureProjectID       string
	source                 continuation.SourceLaunch
	prepareHistoryErr      error
	prepareHistoryCalls    int
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
	if stub.retentionPolicy.RepositoryFirst != "" || stub.retentionPolicy.TranscriptAssisted != "" {
		return stub.retentionPolicy, nil
	}
	return continuation.RetentionPolicy{RepositoryFirst: setting, TranscriptAssisted: "7"}, nil
}

func (stub *checkpointServiceStub) List(context.Context) ([]continuation.Checkpoint, error) {
	return append([]continuation.Checkpoint(nil), stub.checkpoints...), nil
}

func (stub *checkpointServiceStub) Purge(_ context.Context, id, revision string) error {
	stub.purgedRevision = id + "/" + revision
	return nil
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
	stub.prepareHistoryCalls++
	if stub.prepareHistoryErr != nil {
		return continuation.HistorySource{}, stub.prepareHistoryErr
	}
	return continuation.HistorySource{SourceProfileID: "source-profile", IdentityHome: "/source-home"}, nil
}

func (stub *checkpointServiceStub) PreviewAssisted(_ context.Context, id, _ string, edit continuation.EditRequest) (continuation.Checkpoint, error) {
	stub.previewID, stub.previewEdit = id, edit
	if stub.assistedPreview.ID != "" {
		preview := stub.assistedPreview
		preview.Fields = edit.Fields
		return preview, nil
	}
	return stub.checkpoint, nil
}

func (stub *checkpointServiceStub) ApproveAssisted(_ context.Context, id, _, previewRevision string, edit continuation.EditRequest) (continuation.Checkpoint, error) {
	stub.assistedID, stub.previewRevision, stub.assistedEdit = id, previewRevision, edit
	approved := stub.assistedPreview
	approved.Status = continuation.StatusApproved
	return approved, nil
}

type browserHistoryStub struct {
	fields continuation.CheckpointFields
	calls  int
}

func (stub *browserHistoryStub) Candidates(context.Context, continuation.HistorySource, string) (continuation.CheckpointFields, error) {
	stub.calls++
	return stub.fields, nil
}
