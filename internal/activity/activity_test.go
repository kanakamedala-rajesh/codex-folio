package activity

import (
	"context"
	"errors"
	"testing"
	"time"
)

type activityReader struct{ sessions []SourceSession }

func (reader activityReader) Read(context.Context, ReadRequest) ([]SourceSession, error) {
	return append([]SourceSession(nil), reader.sessions...), nil
}

type activityRepository struct {
	target   ProfileTarget
	saved    []ObservedSessionRecord
	timeline []TimelineRecord
}

func (repository *activityRepository) ResolveActivityProfile(context.Context, string) (ProfileTarget, error) {
	return repository.target, nil
}

func (repository *activityRepository) SaveObservedSessions(_ context.Context, records []ObservedSessionRecord) error {
	repository.saved = append([]ObservedSessionRecord(nil), records...)
	return nil
}

func (repository *activityRepository) ListActivity(context.Context, Filters) ([]TimelineRecord, error) {
	return append([]TimelineRecord(nil), repository.timeline...), nil
}

type activityProjects struct{ project ProjectIdentity }

func (projects activityProjects) Resolve(context.Context, string, string) (ProjectIdentity, error) {
	return projects.project, nil
}

func TestRefreshStoresOnlyNormalizedObservedSessionMetadata(t *testing.T) {
	started := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repository := &activityRepository{
		target:   ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"},
		timeline: []TimelineRecord{{RecordType: RecordTypeObservedSession, ID: "observed-1"}},
	}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Reader: activityReader{sessions: []SourceSession{{
			SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d",
			Source:          SourceLocalMetadata, StartedAt: started, LastObservedAt: started.Add(time.Minute),
			WorkingDirectory: "/private/repository", Model: "gpt-5", TokensUsed: tokenCount(42),
		}}},
		Projects: activityProjects{project: ProjectIdentity{ID: "project-1", Alias: "Folio", Basename: "codex-folio"}},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	timeline, err := service.Refresh(context.Background(), "Work", "0.150.1")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(timeline) != 1 || timeline[0].ID != "observed-1" {
		t.Fatalf("timeline = %#v", timeline)
	}
	if len(repository.saved) != 1 {
		t.Fatalf("saved records = %#v", repository.saved)
	}
	saved := repository.saved[0]
	if saved.ProfileID != "profile-1" || saved.ProfileAlias != "Work" || saved.ProjectID != "project-1" || saved.ProjectAlias != "Folio" || saved.ProjectBasename != "codex-folio" {
		t.Fatalf("saved association = %#v", saved)
	}
	if saved.SourceSessionID == "" || saved.SourceVersion != "0.150.1" || saved.Model != "gpt-5" || saved.TokensUsed == nil || *saved.TokensUsed != 42 {
		t.Fatalf("saved metadata = %#v", saved)
	}
}

func tokenCount(value int64) *int64 { return &value }

func TestRefreshLeavesUnsupportedWorkingDirectoryUnassociated(t *testing.T) {
	repository := &activityRepository{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"}}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Reader:     activityReader{sessions: []SourceSession{{SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d", Source: SourceLocalMetadata, StartedAt: time.Now(), LastObservedAt: time.Now(), WorkingDirectory: "/not/a/repository"}}},
		Projects:   failingProjects{err: ErrPathInvalid},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "Work", "0.150.1"); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if len(repository.saved) != 1 || repository.saved[0].ProjectID != "" {
		t.Fatalf("saved records = %#v", repository.saved)
	}
}

type failingProjects struct{ err error }

func (projects failingProjects) Resolve(context.Context, string, string) (ProjectIdentity, error) {
	return ProjectIdentity{}, projects.err
}

func TestRefreshDoesNotHideProjectStoreFailures(t *testing.T) {
	repository := &activityRepository{target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"}}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Reader:     activityReader{sessions: []SourceSession{{SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d", Source: SourceLocalMetadata, StartedAt: time.Now(), LastObservedAt: time.Now(), WorkingDirectory: "/repository"}}},
		Projects:   failingProjects{err: errors.New("store failed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "Work", "0.150.1"); err == nil {
		t.Fatal("Refresh() error = nil, want project store failure")
	}
}
