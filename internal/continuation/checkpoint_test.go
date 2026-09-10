package continuation

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
)

func TestCapturePersistsSanitizedRepositoryFirstDraft(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	projects := &projectStub{project: activity.ProjectIdentity{ID: "project-1", Alias: "Folio", Basename: "codex-folio"}, path: "/home/alice/codex-folio"}
	repository := &checkpointRepositoryStub{}
	inspector := &inspectorStub{inventory: RepositoryInventory{
		Branch: "feature", Head: "abc123", Upstream: &UpstreamDivergence{Ahead: 2, Behind: 1},
		Staged: []string{"safe.go", "private/secret.txt"}, Modified: []string{"README.md"}, Untracked: []string{"notes.txt"},
		Diff: DiffStatistics{FilesChanged: 2, Insertions: 8, Deletions: 3},
	}}
	service, err := NewService(ServiceOptions{Repository: repository, Projects: projects, Inspector: inspector, Now: func() time.Time { return now }, Random: strings.NewReader(strings.Repeat("a", 16)), HomeDirectory: "/home/alice"})
	if err != nil {
		t.Fatal(err)
	}

	checkpoint, err := service.Capture(context.Background(), CaptureRequest{
		Path: "/worktree", Goal: "finish /home/alice/codex-folio without token-value", CompletedWork: "done", ConfiguredCommands: []string{"go test ./..."}, RedactPaths: []string{"private/secret.txt"}, RedactText: []string{"token-value"},
		Validation: &ValidationEvidence{Command: "go test ./internal/continuation", Timestamp: &now, ExitStatus: intPointer(0), Source: "user", Freshness: FreshnessFresh},
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if inspector.calls != 1 || inspector.path != projects.path {
		t.Fatalf("inspector calls/path = %d/%q", inspector.calls, inspector.path)
	}
	if repository.saved.Status != StatusDraft || repository.saved.ProjectIdentityID != "project-1" || repository.saved.ExpiresAt == nil || !repository.saved.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("saved record = %#v", repository.saved)
	}
	if checkpoint.Project.Alias != "Folio" || checkpoint.Repository.Staged.Value[1] != RedactedValue || checkpoint.Fields.Goal.Value != "finish [HOME]/codex-folio without [REDACTED]" {
		t.Fatalf("checkpoint projection = %#v", checkpoint)
	}
	if checkpoint.Fields.PendingWork.Completeness != CompletenessUnknown || checkpoint.Fields.Goal.Provenance != ProvenanceUserConfirmed || checkpoint.Repository.Branch.Provenance != ProvenanceLocalObserved {
		t.Fatalf("field evidence = %#v", checkpoint)
	}
	if checkpoint.Fields.Validation.Completeness != CompletenessComplete || checkpoint.Fields.Validation.Value[0].Timestamp == nil || checkpoint.Fields.Validation.Value[0].ExitStatus == nil {
		t.Fatalf("validation evidence = %#v", checkpoint.Fields.Validation)
	}
	if checkpoint.Source != SourceRepositoryFirst || checkpoint.Status != StatusDraft || checkpoint.SizeBytes == 0 {
		t.Fatalf("checkpoint metadata = %#v", checkpoint)
	}
	if len(checkpoint.Repository.ConfiguredCommands.Value) != 1 || checkpoint.Repository.ConfiguredCommands.Value[0] != "go test ./..." || checkpoint.Repository.ConfiguredCommands.Provenance != ProvenanceUserConfirmed {
		t.Fatalf("configured commands = %#v", checkpoint.Repository.ConfiguredCommands)
	}
	encoded := repository.saved.Metadata
	for _, forbidden := range []string{"/home/alice", "private/secret.txt", "token-value"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("persisted metadata contains %q: %s", forbidden, encoded)
		}
	}
}

func TestCheckpointRetentionControlsOriginExpiryAndExpiredLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	repository := &checkpointRepositoryStub{retention: RetentionPolicy{RepositoryFirst: "1", TranscriptAssisted: "unlimited"}}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Projects:   &projectStub{project: activity.ProjectIdentity{ID: "project-1"}, path: "/repo"},
		Inspector:  &inspectorStub{},
		Now:        func() time.Time { return now },
		Random:     strings.NewReader(strings.Repeat("a", 16)),
	})
	if err != nil {
		t.Fatal(err)
	}

	policy, err := service.Retention(context.Background(), "", "")
	if err != nil || policy.RepositoryFirst != "1" || policy.TranscriptAssisted != "unlimited" {
		t.Fatalf("Retention() = %#v, %v", policy, err)
	}
	checkpoint, err := service.Capture(context.Background(), CaptureRequest{Path: "/repo"})
	if err != nil || checkpoint.Retention != "1" || checkpoint.ExpiresAt == nil || !checkpoint.ExpiresAt.Equal(now.AddDate(0, 0, 1)) {
		t.Fatalf("Capture() = %#v, %v", checkpoint, err)
	}

	repository.loaded = repository.saved
	assisted, err := service.PreviewAssisted(context.Background(), checkpoint.ID, checkpoint.Revision, EditRequest{})
	if err != nil || assisted.Retention != "unlimited" || assisted.ExpiresAt != nil {
		t.Fatalf("PreviewAssisted() = %#v, %v", assisted, err)
	}

	expires := now
	completed := checkpoint
	completed.Status, completed.ExpiresAt = StatusCompleted, &expires
	completed, repository.loaded.Metadata, err = finalizeCheckpoint(completed)
	if err != nil {
		t.Fatal(err)
	}
	repository.loaded.Status, repository.loaded.ExpiresAt = StatusCompleted, &expires
	shown, err := service.Show(context.Background(), checkpoint.ID)
	if err != nil || shown.Status != StatusExpired || repository.saved.Status != StatusExpired || repository.saved.ExpiresAt == nil || !repository.saved.ExpiresAt.Equal(expires) {
		t.Fatalf("Show(expired completed) = %#v, saved %#v, %v", shown, repository.saved, err)
	}
	if _, err := service.PrepareHandoff(context.Background(), checkpoint.ID, shown.Revision, "target"); !errors.Is(err, ErrHandoffNotReady) {
		t.Fatalf("PrepareHandoff(expired) error = %v", err)
	}
}

func TestExportProjectsOnlyApprovedSanitizedCheckpoint(t *testing.T) {
	now := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	checkpoint := Checkpoint{
		ID: "checkpoint-1", Status: StatusApproved,
		Project:    Project{ID: "project-1", Alias: "folio", Basename: "codex-folio"},
		Repository: RepositoryState{Branch: observed("feature/export", CompletenessComplete), Modified: observed([]string{"safe.go"}, CompletenessComplete)},
		Fields:     CheckpointFields{Goal: userField("ship the approved checkpoint")},
		Source:     SourceRepositoryFirst, CreatedAt: now.Add(-time.Hour),
	}
	var err error
	checkpoint, metadata, err := finalizeCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	repository := &checkpointRepositoryStub{loaded: CheckpointRecord{ID: checkpoint.ID, Status: checkpoint.Status, Metadata: metadata}}
	service, err := NewService(ServiceOptions{Repository: repository, Projects: &projectStub{path: "/private/repository"}, Inspector: &inspectorStub{}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	exported, err := service.Export(context.Background(), checkpoint.ID)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if exported.FormatVersion != CheckpointExportVersion || !exported.ExportedAt.Equal(now) || exported.Checkpoint.Status != StatusApproved || exported.Checkpoint.Project.Alias != "folio" || exported.Checkpoint.Project.Basename != "codex-folio" {
		t.Fatalf("Export() = %#v", exported)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "/private/repository") {
		t.Fatalf("export contains canonical path: %s", encoded)
	}

	for _, status := range []string{StatusDraft, StatusLaunching, StatusExpired} {
		checkpoint.Status, repository.loaded.Status = status, status
		checkpoint, repository.loaded.Metadata, err = finalizeCheckpoint(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Export(context.Background(), checkpoint.ID); !errors.Is(err, ErrHandoffNotReady) {
			t.Fatalf("Export(%s) error = %v, want ErrHandoffNotReady", status, err)
		}
	}
	repository.loaded.Status = StatusCompleted
	if _, err := service.Export(context.Background(), checkpoint.ID); err != nil {
		t.Fatalf("Export(completed) error = %v", err)
	}
}

func TestCaptureMarksMissingValidationAttributesPartialAndExplicit(t *testing.T) {
	repository := &checkpointRepositoryStub{}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Projects:   &projectStub{project: activity.ProjectIdentity{ID: "project-1"}, path: "/repo"},
		Inspector:  &inspectorStub{},
		Random:     strings.NewReader(strings.Repeat("a", 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := service.Capture(context.Background(), CaptureRequest{Path: "/repo", Validation: &ValidationEvidence{Command: "go test ./..."}})
	if err != nil {
		t.Fatal(err)
	}
	validation := checkpoint.Fields.Validation
	if validation.Completeness != CompletenessPartial || validation.Value[0].Source != ProvenanceUnknown || validation.Value[0].Freshness != FreshnessUnknown {
		t.Fatalf("validation evidence = %#v", validation)
	}
	if !strings.Contains(repository.saved.Metadata, `"timestamp":null,"exit_status":null,"source":"unknown","freshness":"unknown"`) {
		t.Fatalf("validation unknowns are not explicit: %s", repository.saved.Metadata)
	}
}

func TestCaptureRejectsOversizeWithoutTruncatingOrPersisting(t *testing.T) {
	repository := &checkpointRepositoryStub{}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Projects:   &projectStub{project: activity.ProjectIdentity{ID: "project-1", Alias: "Folio", Basename: "folio"}, path: "/repo"},
		Inspector:  &inspectorStub{},
		Random:     strings.NewReader(strings.Repeat("a", 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Capture(context.Background(), CaptureRequest{Path: "/repo", Goal: strings.Repeat("x", MaxCheckpointBytes)})
	if !errors.Is(err, ErrCheckpointOversize) {
		t.Fatalf("Capture() error = %v, want ErrCheckpointOversize", err)
	}
	if repository.saveCalls != 0 {
		t.Fatalf("SaveCheckpoint calls = %d, want 0", repository.saveCalls)
	}
}

func TestEditSanitizesDraftAndApprovalRequiresTheReviewedRevision(t *testing.T) {
	repository := &checkpointRepositoryStub{}
	service, err := NewService(ServiceOptions{
		Repository:    repository,
		Projects:      &projectStub{},
		Inspector:     &inspectorStub{},
		HomeDirectory: "/home/alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := Checkpoint{
		ID: "checkpoint-1", Status: StatusApproved, Source: SourceRepositoryFirst,
		Repository: RepositoryState{Modified: observed([]string{"private/secret.txt"}, CompletenessComplete)},
		Fields:     CheckpointFields{Goal: userField("old goal")},
	}
	checkpoint, repository.loaded.Metadata, err = finalizeCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	repository.loaded.ID, repository.loaded.Status = checkpoint.ID, checkpoint.Status

	edited, err := service.Edit(context.Background(), checkpoint.ID, EditRequest{
		Fields:      CheckpointFields{Goal: userField("finish /home/alice without token")},
		RedactPaths: []string{"private/secret.txt"}, RedactText: []string{"token"},
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if edited.Status != StatusDraft || edited.Fields.Goal.Value != "finish [HOME] without [REDACTED]" || edited.Repository.Modified.Value[0] != RedactedValue || edited.Revision == "" {
		t.Fatalf("edited checkpoint = %#v", edited)
	}
	if repository.saved.Status != StatusDraft {
		t.Fatalf("saved edit status = %q, want draft", repository.saved.Status)
	}

	repository.loaded = repository.saved
	approved, err := service.Approve(context.Background(), checkpoint.ID, edited.Revision)
	if err != nil || approved.Status != StatusApproved || repository.saved.Status != StatusApproved {
		t.Fatalf("Approve() = %#v, %v; saved status %q", approved, err, repository.saved.Status)
	}
	repository.loaded = repository.saved
	if _, err := service.Approve(context.Background(), checkpoint.ID, "stale-revision"); !errors.Is(err, ErrCheckpointRevisionChanged) {
		t.Fatalf("Approve(stale) error = %v, want ErrCheckpointRevisionChanged", err)
	}

	repository.loaded = repository.saved
	reedited, err := service.Edit(context.Background(), checkpoint.ID, EditRequest{Fields: edited.Fields})
	if err != nil || reedited.Status != StatusDraft {
		t.Fatalf("Edit(approved) = %#v, %v", reedited, err)
	}
	repository.loaded = repository.saved
	oversize := reedited.Fields
	oversize.Goal.Value = strings.Repeat("x", MaxCheckpointBytes)
	if _, err := service.Edit(context.Background(), checkpoint.ID, EditRequest{Fields: oversize}); !errors.Is(err, ErrCheckpointOversize) {
		t.Fatalf("Edit(oversize) error = %v, want ErrCheckpointOversize", err)
	}

	repository.loaded = repository.saved
	repository.loaded.Status = StatusCompleted
	if _, err := service.Edit(context.Background(), checkpoint.ID, EditRequest{Fields: edited.Fields}); !errors.Is(err, ErrHandoffNotReady) {
		t.Fatalf("Edit(completed) error = %v, want ErrHandoffNotReady", err)
	}
	if _, err := service.Approve(context.Background(), checkpoint.ID, repository.loaded.Metadata); !errors.Is(err, ErrHandoffNotReady) {
		t.Fatalf("Approve(completed) error = %v, want ErrHandoffNotReady", err)
	}
}

func TestTranscriptAssistancePersistsOnlyTheApprovedSanitizedRevisionForSevenDays(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	checkpoint := Checkpoint{
		ID: "checkpoint-1", Status: StatusDraft, Project: Project{ID: "project-1"}, Source: SourceRepositoryFirst,
		Fields: CheckpointFields{Goal: userField("repository goal")}, CreatedAt: now, ExpiresAt: timePointer(now.Add(30 * 24 * time.Hour)),
	}
	var err error
	checkpoint, metadata, err := prepareCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	repository := &checkpointRepositoryStub{
		loaded: CheckpointRecord{ID: checkpoint.ID, ProjectIdentityID: checkpoint.Project.ID, Status: StatusDraft, Metadata: metadata, ExpiresAt: checkpoint.ExpiresAt},
		source: SourceLaunch{ProfileID: "source-profile", State: SourceExited}, historyHome: filepath.Join(t.TempDir(), "source"),
	}
	service, err := NewService(ServiceOptions{Repository: repository, Projects: &projectStub{}, Inspector: &inspectorStub{}, Now: func() time.Time { return now }, HomeDirectory: "/home/alice"})
	if err != nil {
		t.Fatal(err)
	}
	repository.source.State = SourceRunning
	if _, err := service.PrepareHistory(context.Background(), checkpoint.ID, checkpoint.Revision); !errors.Is(err, ErrHistoryUnavailable) {
		t.Fatalf("PrepareHistory(active) error = %v, want ErrHistoryUnavailable", err)
	}
	repository.source.State = SourceExited
	history, err := service.PrepareHistory(context.Background(), checkpoint.ID, checkpoint.Revision)
	if err != nil || history.IdentityHome != repository.historyHome || history.SourceProfileID != "source-profile" {
		t.Fatalf("PrepareHistory() = %#v, %v", history, err)
	}
	edit := EditRequest{Fields: CheckpointFields{
		Goal: userField("approved /home/alice goal without token"), NextAction: userField("run tests"),
	}, RedactText: []string{"token"}}
	preview, err := service.PreviewAssisted(context.Background(), checkpoint.ID, checkpoint.Revision, edit)
	if err != nil {
		t.Fatalf("PreviewAssisted() error = %v", err)
	}
	if repository.saveCalls != 0 || preview.Status != StatusDraft || preview.Source != SourceTranscriptAssisted || preview.Fields.Goal.Value != "approved [HOME] goal without [REDACTED]" || !preview.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("preview/save calls = %#v/%d", preview, repository.saveCalls)
	}
	if _, err := service.ApproveAssisted(context.Background(), checkpoint.ID, checkpoint.Revision, preview.Revision+"changed", edit); !errors.Is(err, ErrCheckpointRevisionChanged) || repository.saveCalls != 0 {
		t.Fatalf("ApproveAssisted(changed preview) error/save calls = %v/%d", err, repository.saveCalls)
	}
	approved, err := service.ApproveAssisted(context.Background(), checkpoint.ID, checkpoint.Revision, preview.Revision, edit)
	if err != nil {
		t.Fatalf("ApproveAssisted() error = %v", err)
	}
	if repository.saveCalls != 1 || approved.Status != StatusApproved || repository.saved.Status != StatusApproved || repository.saved.Metadata != mustEncodeCheckpoint(t, approved) || !approved.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("approved/saved = %#v/%#v; calls %d", approved, repository.saved, repository.saveCalls)
	}
	for _, forbidden := range []string{"/home/alice", "token"} {
		if strings.Contains(repository.saved.Metadata, forbidden) {
			t.Fatalf("persisted assisted checkpoint contains %q", forbidden)
		}
	}
}

func TestShowLoadsEncryptedRecordProjection(t *testing.T) {
	want := Checkpoint{ID: "checkpoint-1", Status: StatusDraft, Project: Project{ID: "project-1", Alias: "Folio", Basename: "folio"}, Source: SourceRepositoryFirst}
	metadata, err := encodeCheckpoint(want)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Repository: &checkpointRepositoryStub{loaded: CheckpointRecord{ID: want.ID, Metadata: metadata}}, Projects: &projectStub{}, Inspector: &inspectorStub{}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Show(context.Background(), want.ID)
	if err != nil || got.ID != want.ID || got.Project.Alias != "Folio" {
		t.Fatalf("Show() = %#v, %v", got, err)
	}
}

func TestPrepareHandoffUsesOnlyApprovedCurrentCheckpointAfterExitedSource(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	checkpoint := Checkpoint{
		ID: "checkpoint-1", Status: StatusApproved, Revision: "revision-1",
		Project: Project{ID: "project-1", Alias: "Folio", Basename: "folio"},
		Fields:  CheckpointFields{Goal: userField("finish ticket 50")},
		Source:  SourceRepositoryFirst, CreatedAt: now.Add(-time.Hour), ExpiresAt: timePointer(now.Add(24 * time.Hour)),
	}
	metadata, err := encodeCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	repository := &checkpointRepositoryStub{
		loaded: CheckpointRecord{ID: checkpoint.ID, ProjectIdentityID: checkpoint.Project.ID, Status: StatusApproved, Metadata: metadata, ExpiresAt: checkpoint.ExpiresAt},
		source: SourceLaunch{ProfileID: "source-profile", State: SourceExited},
	}
	service, err := NewService(ServiceOptions{
		Repository: repository, Projects: &projectStub{path: "/repo"}, Inspector: &inspectorStub{}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := service.PrepareHandoff(context.Background(), checkpoint.ID, checkpoint.Revision, "target-profile")
	if err != nil {
		t.Fatalf("PrepareHandoff() error = %v", err)
	}
	if prepared.WorkingDirectory != "/repo" || prepared.ProjectID != "project-1" || prepared.SourceProfileID != "source-profile" || prepared.Context != metadata {
		t.Fatalf("prepared handoff = %#v", prepared)
	}
}

func TestPrepareHandoffRejectsUnapprovedExpiredChangedOrUnstoppedState(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	base := Checkpoint{
		ID: "checkpoint-1", Status: StatusApproved, Revision: "revision-1", Project: Project{ID: "project-1"},
		Source: SourceRepositoryFirst, CreatedAt: now.Add(-time.Hour), ExpiresAt: timePointer(now.Add(time.Hour)),
	}
	for _, test := range []struct {
		name, status, revision, target string
		expires                        *time.Time
		source                         SourceLaunch
	}{
		{name: "draft", status: StatusDraft, revision: base.Revision, target: "target", expires: base.ExpiresAt, source: SourceLaunch{ProfileID: "source", State: SourceExited}},
		{name: "expired", status: StatusApproved, revision: base.Revision, target: "target", expires: timePointer(now), source: SourceLaunch{ProfileID: "source", State: SourceExited}},
		{name: "changed", status: StatusApproved, revision: "changed", target: "target", expires: base.ExpiresAt, source: SourceLaunch{ProfileID: "source", State: SourceExited}},
		{name: "running", status: StatusApproved, revision: base.Revision, target: "target", expires: base.ExpiresAt, source: SourceLaunch{ProfileID: "source", State: SourceRunning}},
		{name: "uncertain", status: StatusApproved, revision: base.Revision, target: "target", expires: base.ExpiresAt, source: SourceLaunch{ProfileID: "source", State: SourceUncertain}},
		{name: "same profile", status: StatusApproved, revision: base.Revision, target: "source", expires: base.ExpiresAt, source: SourceLaunch{ProfileID: "source", State: SourceExited}},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkpoint := base
			checkpoint.Status, checkpoint.ExpiresAt = test.status, test.expires
			metadata, err := encodeCheckpoint(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			repository := &checkpointRepositoryStub{loaded: CheckpointRecord{
				ID: checkpoint.ID, ProjectIdentityID: checkpoint.Project.ID, Status: test.status, Metadata: metadata, ExpiresAt: test.expires,
			}, source: test.source}
			service, err := NewService(ServiceOptions{Repository: repository, Projects: &projectStub{path: "/repo"}, Inspector: &inspectorStub{}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.PrepareHandoff(context.Background(), checkpoint.ID, test.revision, test.target); !errors.Is(err, ErrHandoffNotReady) {
				t.Fatalf("PrepareHandoff() error = %v, want ErrHandoffNotReady", err)
			}
		})
	}
}

type projectStub struct {
	project activity.ProjectIdentity
	path    string
}

func (stub *projectStub) Resolve(context.Context, string, string) (activity.ProjectIdentity, error) {
	return stub.project, nil
}

func (stub *projectStub) CanonicalLocation(context.Context, string) (string, error) {
	return stub.path, nil
}

type inspectorStub struct {
	inventory RepositoryInventory
	calls     int
	path      string
}

func (stub *inspectorStub) Inspect(_ context.Context, path string) (RepositoryInventory, error) {
	stub.calls++
	stub.path = path
	return stub.inventory, nil
}

type checkpointRepositoryStub struct {
	saved       CheckpointRecord
	loaded      CheckpointRecord
	source      SourceLaunch
	historyHome string
	saveCalls   int
	retention   RetentionPolicy
}

func (stub *checkpointRepositoryStub) SaveCheckpoint(_ context.Context, record CheckpointRecord) error {
	stub.saveCalls++
	stub.saved = record
	return nil
}

func (stub *checkpointRepositoryStub) LoadCheckpoint(context.Context, string) (CheckpointRecord, error) {
	return stub.loaded, nil
}

func (stub *checkpointRepositoryStub) LatestSourceLaunch(context.Context, string) (SourceLaunch, error) {
	return stub.source, nil
}

func (stub *checkpointRepositoryStub) SourceIdentityHome(context.Context, string) (string, error) {
	return stub.historyHome, nil
}

func (stub *checkpointRepositoryStub) CheckpointRetention(context.Context) (RetentionPolicy, error) {
	return stub.retention, nil
}

func (stub *checkpointRepositoryStub) SetCheckpointRetention(_ context.Context, source, setting string) (RetentionPolicy, error) {
	if source == SourceRepositoryFirst {
		stub.retention.RepositoryFirst = setting
	} else {
		stub.retention.TranscriptAssisted = setting
	}
	return stub.retention, nil
}

func mustEncodeCheckpoint(t *testing.T, checkpoint Checkpoint) string {
	t.Helper()
	encoded, err := encodeCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func intPointer(value int) *int              { return &value }
func timePointer(value time.Time) *time.Time { return &value }
