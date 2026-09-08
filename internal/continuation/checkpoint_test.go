package continuation

import (
	"context"
	"errors"
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
	saved     CheckpointRecord
	loaded    CheckpointRecord
	saveCalls int
}

func (stub *checkpointRepositoryStub) SaveCheckpoint(_ context.Context, record CheckpointRecord) error {
	stub.saveCalls++
	stub.saved = record
	return nil
}

func (stub *checkpointRepositoryStub) LoadCheckpoint(context.Context, string) (CheckpointRecord, error) {
	return stub.loaded, nil
}

func intPointer(value int) *int { return &value }
