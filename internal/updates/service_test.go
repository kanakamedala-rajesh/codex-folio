package updates

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type memoryRepository struct {
	settings Settings
	state    CheckState
}

func (repo *memoryRepository) UpdateSettings(context.Context) (Settings, error) {
	return repo.settings, nil
}
func (repo *memoryRepository) SetUpdateSettings(_ context.Context, value Settings) (Settings, error) {
	repo.settings = value
	return value, nil
}
func (repo *memoryRepository) UpdateCheckState(context.Context) (CheckState, error) {
	if repo.state.Status == "" {
		return CheckState{Status: StatusNeverChecked}, nil
	}
	return repo.state, nil
}
func (repo *memoryRepository) SetUpdateCheckState(_ context.Context, value CheckState) (CheckState, error) {
	repo.state = value
	return value, nil
}

type recordingSource struct {
	configured bool
	evidence   Evidence
	err        error
	calls      int
}

func (source *recordingSource) Configured() bool { return source.configured }
func (source *recordingSource) Check(context.Context) (Evidence, error) {
	source.calls++
	return source.evidence, source.err
}

func newTestService(t *testing.T, repo *memoryRepository, source *recordingSource, now time.Time) *Service {
	t.Helper()
	service, err := NewService(ServiceOptions{Repository: repo, Source: source, Clock: fixedClock{now}, CurrentVersion: "1.2.3-alpha.2"})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func validEvidence() Evidence {
	return Evidence{Version: "1.2.3", ReleaseNotes: "Validated release notes.", DownloadURL: "https://updates.example/releases/1.2.3.zip", InstallerGuidance: "Verify the signature, then replace the executable while stopped."}
}

func TestServiceDefaultsToNoNetworkAndPersistsIndependentConsent(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	repo := &memoryRepository{}
	source := &recordingSource{configured: true, evidence: validEvidence()}
	service := newTestService(t, repo, source, now)

	snapshot, err := service.Status(context.Background())
	if err != nil || snapshot.Settings.AutomaticChecks || snapshot.State.Status != StatusDisabled || source.calls != 0 {
		t.Fatalf("Status() = %#v, %v; calls = %d", snapshot, err, source.calls)
	}
	snapshot, err = service.SetAutomatic(context.Background(), true)
	if err != nil || !snapshot.Settings.AutomaticChecks || source.calls != 0 {
		t.Fatalf("SetAutomatic() = %#v, %v; calls = %d", snapshot, err, source.calls)
	}
	if _, err := service.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if source.calls != 1 || !repo.settings.AutomaticChecks {
		t.Fatalf("calls/settings = %d/%#v", source.calls, repo.settings)
	}
	if _, err := service.SetAutomatic(context.Background(), false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := service.Check(context.Background()); err != nil {
		t.Fatalf("explicit disabled check: %v", err)
	}
	if source.calls != 2 || repo.settings.AutomaticChecks {
		t.Fatalf("explicit check changed consent: calls/settings = %d/%#v", source.calls, repo.settings)
	}
}

func TestAutomaticCheckHonorsOptInAndCadence(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	repo := &memoryRepository{}
	source := &recordingSource{configured: true, evidence: validEvidence()}
	service := newTestService(t, repo, source, now)
	if _, err := service.CheckAutomatic(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 0 {
		t.Fatalf("opted-out calls = %d", source.calls)
	}
	if _, err := service.SetAutomatic(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.CheckAutomatic(context.Background())
	if err != nil || source.calls != 1 || snapshot.State.Status != StatusUpdateAvailable || !snapshot.State.NextCheckAt.Equal(now.Add(NormalCheckInterval)) {
		t.Fatalf("first automatic = %#v, %v; calls=%d", snapshot, err, source.calls)
	}
	if _, err := service.CheckAutomatic(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 {
		t.Fatalf("not-due calls = %d", source.calls)
	}
}

func TestServiceInvalidatesEvidenceFromAnotherRunningVersionWithoutNetwork(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	repo := &memoryRepository{
		settings: Settings{AutomaticChecks: true},
		state: CheckState{
			Status: StatusUpdateAvailable, CurrentVersion: "1.2.2",
			AvailableVersion: "1.2.3", ReleaseNotes: "Stale notes.",
			DownloadURL: "https://updates.example/releases/1.2.3.zip", InstallerGuidance: "Stale guidance.",
			CheckedAt: now.Add(-time.Hour), NextCheckAt: now.Add(23 * time.Hour),
		},
	}
	source := &recordingSource{configured: true, evidence: validEvidence()}
	service := newTestService(t, repo, source, now)

	snapshot, err := service.Status(context.Background())
	if err != nil || !snapshot.Settings.AutomaticChecks || snapshot.State.Status != StatusNeverChecked || snapshot.State.CurrentVersion != "1.2.3-alpha.2" {
		t.Fatalf("Status() = %#v, %v", snapshot, err)
	}
	if snapshot.State.AvailableVersion != "" || snapshot.State.ReleaseNotes != "" || snapshot.State.DownloadURL != "" || snapshot.State.InstallerGuidance != "" || source.calls != 0 {
		t.Fatalf("Status() retained stale evidence or contacted source: %#v, calls=%d", snapshot.State, source.calls)
	}

	snapshot, err = service.SetAutomatic(context.Background(), false)
	if err != nil || snapshot.Settings.AutomaticChecks || snapshot.State.Status != StatusDisabled || snapshot.State.CurrentVersion != "1.2.3-alpha.2" || source.calls != 0 {
		t.Fatalf("SetAutomatic(false) = %#v, %v; calls=%d", snapshot, err, source.calls)
	}
}

func TestServiceCollapsesSourceFailuresToSafePersistedState(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		err    error
		status Status
		code   string
	}{
		{"unconfigured", ErrSourceUnconfigured, StatusUnconfigured, ErrorSourceUnconfigured},
		{"offline", errors.Join(ErrSourceOffline, errors.New("private transport cause")), StatusOffline, ErrorSourceOffline},
		{"malformed", ErrSourceMalformed, StatusMalformed, ErrorSourceMalformed},
		{"unavailable", errors.New("private server cause"), StatusUnavailable, ErrorSourceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &memoryRepository{}
			source := &recordingSource{configured: test.err != ErrSourceUnconfigured, err: test.err}
			snapshot, err := newTestService(t, repo, source, now).Check(context.Background())
			if err != nil || source.calls != 1 || snapshot.State.Status != test.status || snapshot.State.ErrorCode != test.code || !snapshot.State.NextCheckAt.Equal(now.Add(FailureRetryInterval)) {
				t.Fatalf("Check() = %#v, %v; calls=%d", snapshot, err, source.calls)
			}
			if snapshot.State.ReleaseNotes != "" || snapshot.State.DownloadURL != "" {
				t.Fatalf("failure retained payload: %#v", snapshot.State)
			}
		})
	}
}

func TestServiceCancellationIsNondestructive(t *testing.T) {
	repo := &memoryRepository{}
	source := &recordingSource{configured: true, err: context.Canceled}
	_, err := newTestService(t, repo, source, time.Now()).Check(context.Background())
	if !errors.Is(err, context.Canceled) || repo.state.Status != "" {
		t.Fatalf("Check() error/state = %v/%#v", err, repo.state)
	}
}

func TestVersionComparisonUsesSemverPrereleasePrecedence(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0+build.2", "1.0.0+build.1", 0},
		{"2.0.0", "1.99.99", 1},
	} {
		got, err := CompareVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Errorf("CompareVersions(%q,%q) = %d,%v want %d", test.left, test.right, got, err, test.want)
		}
	}
	for _, invalid := range []string{"v1.2.3", "1.02.3", "1.2", "1.2.3-01", "1.2.3 bad"} {
		if err := ValidateVersion(invalid); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidateVersion(%q) = %v", invalid, err)
		}
	}
}
