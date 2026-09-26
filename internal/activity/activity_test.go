package activity

import (
	"context"
	"errors"
	"testing"
	"time"
)

type activityReader struct{ sessions []SourceSession }

func (reader activityReader) Probe(context.Context, string) (SourceInspection, error) {
	return SourceInspection{Status: SourceStatusSupported, SessionCount: len(reader.sessions)}, nil
}

func (reader activityReader) Read(context.Context, ReadRequest) ([]SourceSession, error) {
	return append([]SourceSession(nil), reader.sessions...), nil
}

type activityRepository struct {
	target     ProfileTarget
	saved      []ObservedSessionRecord
	timeline   []TimelineRecord
	refreshIDs []string
	cutoff     time.Time
}

func (repository *activityRepository) ListActivitySources(context.Context) ([]SourceTarget, error) {
	return []SourceTarget{{ID: "home-1", Label: repository.target.Alias, IdentityHome: repository.target.IdentityHome}}, nil
}

func (repository *activityRepository) ResolveActivityProfile(context.Context, string) (ProfileTarget, error) {
	return repository.target, nil
}

func (repository *activityRepository) SaveObservedSessions(_ context.Context, records []ObservedSessionRecord) error {
	repository.saved = append([]ObservedSessionRecord(nil), records...)
	for _, record := range records {
		if record.LastObservedAt.Before(repository.cutoff) {
			continue
		}
		found := false
		for _, existing := range repository.timeline {
			if existing.SourceSessionID == record.SourceSessionID {
				found = true
			}
		}
		if !found {
			repository.timeline = append(repository.timeline, TimelineRecord{RecordType: RecordTypeObservedSession, Source: record.Source, SourceSessionID: record.SourceSessionID})
		}
	}
	return nil
}

func (repository *activityRepository) ListActivity(context.Context, Filters) ([]TimelineRecord, error) {
	return append([]TimelineRecord(nil), repository.timeline...), nil
}

func (repository *activityRepository) RefreshSessionIDs(context.Context, string) ([]string, error) {
	return repository.refreshIDs, nil
}

func (repository *activityRepository) AssignSessions(context.Context, []string, string) error {
	return nil
}

type activityProjects struct{ project ProjectIdentity }

func (projects activityProjects) Resolve(context.Context, string, string) (ProjectIdentity, error) {
	return projects.project, nil
}

func TestRefreshStoresOnlyNormalizedObservedSessionMetadata(t *testing.T) {
	started := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repository := &activityRepository{
		refreshIDs: []string{"018f4f70-6f77-7c3f-9b77-93aa087dfc4d"},
		target:     ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"},
		timeline:   []TimelineRecord{{RecordType: RecordTypeObservedSession, ID: "observed-1", SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"}},
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
	if saved.ProfileID != "" || saved.ProfileAlias != "" || saved.ProjectID != "project-1" || saved.ProjectAlias != "Folio" || saved.ProjectBasename != "codex-folio" {
		t.Fatalf("saved association = %#v", saved)
	}
	if saved.SourceSessionID == "" || saved.SourceVersion != "0.150.1" || saved.Model != "gpt-5" || saved.TokensUsed == nil || *saved.TokensUsed != 42 {
		t.Fatalf("saved metadata = %#v", saved)
	}
}

func tokenCount(value int64) *int64 { return &value }

func TestImportRequiresConsentAndLeavesUnknownHistoryUnassigned(t *testing.T) {
	start := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	repository := &activityRepository{refreshIDs: []string{"018f4f70-6f77-7c3f-9b77-93aa087dfc4d"}, target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"}}
	service, err := NewService(ServiceOptions{Repository: repository, Reader: activityReader{sessions: []SourceSession{{SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d", Source: SourceLocalMetadata, StartedAt: start, LastObservedAt: start}}}, Projects: activityProjects{}})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := service.ReviewSources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].SessionCount != 1 || len(repository.saved) != 0 {
		t.Fatalf("review = %#v, saved = %#v, error = %v", sources, repository.saved, err)
	}
	if _, err := service.ImportSource(context.Background(), "home-1", "state_5", false); !errors.Is(err, ErrActivityInvalid) || len(repository.saved) != 0 {
		t.Fatalf("refused import saved %#v: %v", repository.saved, err)
	}
	result, err := service.ImportSource(context.Background(), "home-1", "state_5", true)
	if err != nil || result.ImportedCount != 1 || len(repository.saved) != 1 || repository.saved[0].ProfileID != "" {
		t.Fatalf("import = %#v, saved = %#v, error = %v", result, repository.saved, err)
	}
}

func TestRefreshLeavesUnsupportedWorkingDirectoryUnassociated(t *testing.T) {
	repository := &activityRepository{refreshIDs: []string{"018f4f70-6f77-7c3f-9b77-93aa087dfc4d"}, target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"}}
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
	repository := &activityRepository{refreshIDs: []string{"018f4f70-6f77-7c3f-9b77-93aa087dfc4d"}, target: ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: "/private/home"}}
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

func TestRefreshDoesNotImportUnconsentedHistory(t *testing.T) {
	start := time.Now().UTC()
	repository := &activityRepository{target: ProfileTarget{ID: "profile-1"}, refreshIDs: []string{"known"}}
	service, err := NewService(ServiceOptions{Repository: repository, Reader: activityReader{sessions: []SourceSession{
		{SourceSessionID: "unknown", Source: SourceLocalMetadata, StartedAt: start, LastObservedAt: start},
		{SourceSessionID: "known", Source: SourceLocalMetadata, StartedAt: start, LastObservedAt: start},
	}}, Projects: activityProjects{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), "Work", "state_5"); err != nil {
		t.Fatal(err)
	}
	if len(repository.saved) != 1 || repository.saved[0].SourceSessionID != "known" {
		t.Fatalf("saved = %#v", repository.saved)
	}
	repository.saved = nil
	repository.refreshIDs = nil
	if _, err := service.Refresh(context.Background(), "Work", "state_5"); err != nil {
		t.Fatal(err)
	}
	if len(repository.saved) != 0 {
		t.Fatal("refresh imported history without consent")
	}
}

func TestImportCountsOnlyRetainedSessions(t *testing.T) {
	start := time.Now().UTC()
	repository := &activityRepository{cutoff: start.Add(-time.Hour)}
	service, _ := NewService(ServiceOptions{Repository: repository, Reader: activityReader{sessions: []SourceSession{
		{SourceSessionID: "expired", Source: SourceLocalMetadata, StartedAt: start.Add(-2 * time.Hour), LastObservedAt: start.Add(-2 * time.Hour)},
		{SourceSessionID: "retained", Source: SourceLocalMetadata, StartedAt: start, LastObservedAt: start},
	}}, Projects: activityProjects{}})
	first, err := service.ImportSource(context.Background(), "home-1", "state_5", true)
	if err != nil || first.ImportedCount != 1 || first.AlreadyPresentCount != 0 {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := service.ImportSource(context.Background(), "home-1", "state_5", true)
	if err != nil || second.ImportedCount != 0 || second.AlreadyPresentCount != 1 {
		t.Fatalf("repeat = %#v, %v", second, err)
	}
}
