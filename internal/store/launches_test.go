package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

type launchClock struct{ now time.Time }

func (clock launchClock) Now() time.Time { return clock.now }

type mutableLaunchClock struct{ now time.Time }

func (clock *mutableLaunchClock) Now() time.Time { return clock.now }

type launchInspector struct {
	running       bool
	err           error
	bootSessionID string
}

func (inspector launchInspector) IsRunning(int) (bool, error) {
	return inspector.running, inspector.err
}

func (inspector launchInspector) BootSessionID() (string, error) {
	return inspector.bootSessionID, inspector.err
}

func TestPrepareAndCompleteManagedLaunchPersistsLifecycleWithoutChangingSelection(t *testing.T) {
	stateStore, _, profileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()

	request := launch.PrepareRequest{
		Alias:            "Work",
		Executable:       filepath.Join(home, "codex"),
		WorkingDirectory: filepath.Join(home, "repository"),
		Arguments:        []string{"--model", "value with spaces"},
	}
	plan, err := stateStore.PrepareLaunch(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if plan.Executable != request.Executable || plan.WorkingDirectory != request.WorkingDirectory {
		t.Fatalf("plan = %#v, want launch inputs", plan)
	}
	if len(plan.Arguments) != 2 || plan.Arguments[1] != "value with spaces" || plan.Environment["CODEX_HOME"] != home {
		t.Fatalf("plan = %#v, want exact arguments and target home environment", plan)
	}

	record, err := stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch(pending) error = %v", err)
	}
	if record.State != launch.StatePending || record.ProcessID != 0 || record.ExitStatus != nil {
		t.Fatalf("pending record = %#v, want pending without process result", record)
	}

	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 4321); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), plan.LeaseID, 256); err != nil {
		t.Fatalf("MarkManagedLaunchExited() error = %v", err)
	}
	record, err = stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch(exited) error = %v", err)
	}
	if record.State != launch.StateExited || record.ProcessID != 4321 || record.ExitStatus == nil || *record.ExitStatus != 256 || record.EndedAt == nil {
		t.Fatalf("exited record = %#v, want process and exit status", record)
	}
	var selectedProfile string
	if err := stateStore.db.QueryRowContext(context.Background(), "SELECT profile_id FROM selected_profile WHERE selection_id = 1").Scan(&selectedProfile); err != nil {
		t.Fatalf("selected profile query error = %v", err)
	}
	if selectedProfile != profileID {
		t.Fatalf("selected profile = %q, want immutable profile %q", selectedProfile, profileID)
	}
}

func TestLatestSourceLaunchIgnoresDefinitivelyAbandonedPreStartLaunch(t *testing.T) {
	stateStore, _, profileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()
	_, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectID := "project-1"
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	if err := stateStore.SaveProjectRecord(context.Background(), activity.ProjectRecord{ID: projectID, Alias: "Folio", Basename: "repository", CanonicalPath: home, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: home, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	abandoned, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: home, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchAbandoned(context.Background(), abandoned.LeaseID); err != nil {
		t.Fatal(err)
	}

	latest, err := stateStore.LatestSourceLaunch(context.Background(), projectID)
	if err != nil || latest.State != continuation.SourceExited || latest.ProfileID != profileID {
		t.Fatalf("LatestSourceLaunch() = %#v, %v; want exited source %q", latest, err, profileID)
	}
}

func TestLatestSourceLaunchUsesMostRecentlyExitedOverlappingLaunch(t *testing.T) {
	clock := &mutableLaunchClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)}
	stateStore, _, workProfileID, home := readyLaunchStoreWithClock(t, clock)
	defer func() { _ = stateStore.Close() }()
	personalProfileID, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectID := "project-1"
	if err := stateStore.SaveProjectRecord(context.Background(), activity.ProjectRecord{ID: projectID, Alias: "Folio", Basename: "repository", CanonicalPath: home, CreatedAt: clock.now, UpdatedAt: clock.now}); err != nil {
		t.Fatal(err)
	}

	work, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: home, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), work.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	personal, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: home, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), personal.LeaseID, 4002); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	if err := stateStore.MarkManagedLaunchExited(context.Background(), personal.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Minute)
	if err := stateStore.MarkManagedLaunchExited(context.Background(), work.LeaseID, 0); err != nil {
		t.Fatal(err)
	}

	latest, err := stateStore.LatestSourceLaunch(context.Background(), projectID)
	if err != nil || latest.State != continuation.SourceExited || latest.ProfileID != workProfileID || latest.ProfileID == personalProfileID {
		t.Fatalf("LatestSourceLaunch() = %#v, %v; want most recently exited source %q", latest, err, workProfileID)
	}
}

func TestHandoffLaunchReservesApprovedCheckpointAndCompletesItOnStart(t *testing.T) {
	stateStore, _, sourceProfileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()
	targetProfileID, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectPath := filepath.Join(home, "repository")
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "repository", CanonicalPath: projectPath, CreatedAt: now, UpdatedAt: now}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	concurrent, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: projectPath, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), concurrent.LeaseID, 4000); err != nil {
		t.Fatal(err)
	}
	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: projectPath, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(24 * time.Hour)
	checkpoint := continuation.CheckpointRecord{ID: "checkpoint-1", ProjectIdentityID: project.ID, Status: continuation.StatusApproved, Metadata: `{"status":"approved","revision":"revision-1"}`, CreatedAt: now, ExpiresAt: &expires}
	if err := stateStore.SaveCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}

	request := launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: projectPath,
		Arguments: []string{checkpoint.Metadata}, ProjectID: project.ID, CheckpointID: checkpoint.ID, CheckpointRevision: "revision-1", SourceProfileID: sourceProfileID, BootSessionID: "boot-a",
	}
	if _, err := stateStore.PrepareLaunch(context.Background(), request); !errors.Is(err, continuation.ErrHandoffNotReady) {
		t.Fatalf("PrepareLaunch(concurrent source) error = %v, want ErrHandoffNotReady", err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), concurrent.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	plan, err := stateStore.PrepareLaunch(context.Background(), request)
	if err != nil {
		t.Fatalf("PrepareLaunch(handoff) error = %v", err)
	}
	reserved, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || reserved.Status != continuation.StatusLaunching {
		t.Fatalf("reserved checkpoint = %#v, %v", reserved, err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 4002); err != nil {
		t.Fatalf("MarkManagedLaunchStarted(handoff) error = %v", err)
	}
	completed, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || completed.Status != continuation.StatusCompleted {
		t.Fatalf("completed checkpoint = %#v, %v", completed, err)
	}
	if plan.Environment["CODEX_HOME"] != targetHome || targetProfileID == sourceProfileID {
		t.Fatalf("target plan/profile = %#v/%q", plan, targetProfileID)
	}
}

func TestCheckpointMutationCannotOverwriteReservedHandoff(t *testing.T) {
	stateStore, _, sourceProfileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()
	_, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectPath := filepath.Join(home, "repository")
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "repository", CanonicalPath: projectPath, CreatedAt: now, UpdatedAt: now}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: projectPath, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	checkpoint := continuation.CheckpointRecord{ID: "checkpoint-1", ProjectIdentityID: project.ID, Status: continuation.StatusApproved, Metadata: `{"status":"approved","revision":"revision-1"}`, CreatedAt: now}
	if err := stateStore.SaveCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	wrongRevision := checkpoint
	wrongRevision.Status = continuation.StatusDraft
	wrongRevision.Metadata = `{"status":"draft","revision":"revision-2"}`
	wrongRevision.ExpectedStatus = continuation.StatusApproved
	wrongRevision.ExpectedRevision = "wrong-revision"
	if err := stateStore.SaveCheckpoint(context.Background(), wrongRevision); !errors.Is(err, continuation.ErrCheckpointRevisionChanged) {
		t.Fatalf("SaveCheckpoint(wrong revision) error = %v, want ErrCheckpointRevisionChanged", err)
	}

	if _, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: projectPath,
		Arguments: []string{checkpoint.Metadata}, ProjectID: project.ID, CheckpointID: checkpoint.ID, CheckpointRevision: "revision-1", SourceProfileID: sourceProfileID, BootSessionID: "boot-a",
	}); err != nil {
		t.Fatal(err)
	}
	staleEdit := wrongRevision
	staleEdit.ExpectedRevision = "revision-1"
	if err := stateStore.SaveCheckpoint(context.Background(), staleEdit); !errors.Is(err, continuation.ErrHandoffNotReady) {
		t.Fatalf("SaveCheckpoint(reserved handoff) error = %v, want ErrHandoffNotReady", err)
	}
	stored, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || stored.Status != continuation.StatusLaunching || stored.Metadata != checkpoint.Metadata {
		t.Fatalf("stored checkpoint = %#v, %v; want reserved handoff unchanged", stored, err)
	}
}

func TestHandoffLaunchReservesUnlimitedCheckpointAndCompletesItOnStart(t *testing.T) {
	stateStore, _, sourceProfileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()
	_, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectPath := filepath.Join(home, "repository")
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "repository", CanonicalPath: projectPath, CreatedAt: now, UpdatedAt: now}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: projectPath, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	checkpoint := continuation.CheckpointRecord{ID: "checkpoint-1", ProjectIdentityID: project.ID, Status: continuation.StatusApproved, Metadata: `{"status":"approved","revision":"revision-1"}`, CreatedAt: now}
	if err := stateStore.SaveCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}

	plan, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: projectPath,
		Arguments: []string{checkpoint.Metadata}, ProjectID: project.ID, CheckpointID: checkpoint.ID, CheckpointRevision: "revision-1", SourceProfileID: sourceProfileID, BootSessionID: "boot-a",
	})
	if err != nil {
		t.Fatalf("PrepareLaunch(handoff) error = %v", err)
	}
	reserved, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || reserved.Status != continuation.StatusLaunching || reserved.ExpiresAt != nil {
		t.Fatalf("reserved checkpoint = %#v, %v", reserved, err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 4002); err != nil {
		t.Fatalf("MarkManagedLaunchStarted(handoff) error = %v", err)
	}
	completed, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || completed.Status != continuation.StatusCompleted || completed.ExpiresAt != nil {
		t.Fatalf("completed checkpoint = %#v, %v", completed, err)
	}
}

func TestReconcilePendingHandoffRecoversOnlyAfterBootSessionChanges(t *testing.T) {
	stateStore, _, sourceProfileID, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()
	_, targetHome := addReadyLaunchProfile(t, stateStore, "profile-2", "Personal")
	projectPath := filepath.Join(home, "repository")
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	project := activity.ProjectRecord{ID: "project-1", Alias: "Folio", Basename: "repository", CanonicalPath: projectPath, CreatedAt: now, UpdatedAt: now}
	if err := stateStore.SaveProjectRecord(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	source, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: projectPath, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), source.LeaseID, 4001); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), source.LeaseID, 0); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(24 * time.Hour)
	checkpoint := continuation.CheckpointRecord{ID: "checkpoint-1", ProjectIdentityID: project.ID, Status: continuation.StatusApproved, Metadata: `{"status":"approved","revision":"revision-1"}`, CreatedAt: now, ExpiresAt: &expires}
	if err := stateStore.SaveCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	plan, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: projectPath, Arguments: []string{checkpoint.Metadata},
		ProjectID: project.ID, CheckpointID: checkpoint.ID, CheckpointRevision: "revision-1", SourceProfileID: sourceProfileID, BootSessionID: "boot-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := formatStoredTime(now.Add(-time.Hour))
	if _, err := stateStore.db.ExecContext(context.Background(), `UPDATE managed_launches SET started_at = ? WHERE lease_id = ?`, stale, plan.LeaseID); err != nil {
		t.Fatal(err)
	}
	scope := usage.HistoryScope{ProfileID: "*", ProjectID: project.ID, From: "all", To: "all", Classes: []string{"checkpoints"}}
	preview, err := stateStore.PurgeAnalytics(context.Background(), scope, "")
	if err != nil || len(preview.Counts) != 10 || preview.Counts[9].RecordClass != "checkpoints" || preview.Counts[9].Count != 0 {
		t.Fatalf("active handoff purge preview = %#v, %v", preview, err)
	}
	if _, err := stateStore.PurgeAnalytics(context.Background(), scope, preview.Confirmation); err != nil {
		t.Fatalf("active handoff purge = %v", err)
	}
	if err := stateStore.ReconcileManagedLaunches(context.Background(), launchInspector{bootSessionID: "boot-a"}); err != nil {
		t.Fatal(err)
	}
	reserved, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || reserved.Status != continuation.StatusLaunching || reserved.ExpiresAt == nil || !reserved.ExpiresAt.Equal(expires) {
		t.Fatalf("reserved checkpoint = %#v, %v", reserved, err)
	}
	record, err := stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil || record.State != launch.StatePending {
		t.Fatalf("reconciled launch = %#v, %v", record, err)
	}
	if _, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(targetHome, "codex"), WorkingDirectory: projectPath, Arguments: []string{checkpoint.Metadata},
		ProjectID: project.ID, CheckpointID: checkpoint.ID, CheckpointRevision: "revision-1", SourceProfileID: sourceProfileID, BootSessionID: "boot-a",
	}); !errors.Is(err, continuation.ErrHandoffNotReady) {
		t.Fatalf("PrepareLaunch(stale pending handoff) error = %v, want ErrHandoffNotReady", err)
	}
	if err := stateStore.ReconcileManagedLaunches(context.Background(), launchInspector{bootSessionID: "boot-b"}); err != nil {
		t.Fatal(err)
	}
	recovered, err := stateStore.LoadCheckpoint(context.Background(), checkpoint.ID)
	if err != nil || recovered.Status != continuation.StatusApproved || recovered.ExpiresAt == nil || !recovered.ExpiresAt.Equal(expires) {
		t.Fatalf("recovered checkpoint = %#v, %v", recovered, err)
	}
	record, err = stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil || record.State != launch.StateAbandoned {
		t.Fatalf("recovered launch = %#v, %v", record, err)
	}
}

func TestReconcileManagedLaunchesMarksUnknownExitAsAbandoned(t *testing.T) {
	stateStore, _, _, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()

	plan, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias:            "Work",
		Executable:       filepath.Join(home, "codex"),
		WorkingDirectory: home,
	})
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 9001); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	if err := stateStore.ReconcileManagedLaunches(context.Background(), launchInspector{running: false}); err != nil {
		t.Fatalf("ReconcileManagedLaunches() error = %v", err)
	}
	record, err := stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch() error = %v", err)
	}
	if record.State != launch.StateAbandoned || record.ExitStatus != nil {
		t.Fatalf("record = %#v, want abandoned without invented exit status", record)
	}
}

func TestReconcileManagedLaunchesKeepsPendingLeaseStartable(t *testing.T) {
	stateStore, _, _, home := readyLaunchStore(t)
	defer func() { _ = stateStore.Close() }()

	plan, err := stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias:            "Work",
		Executable:       filepath.Join(home, "codex"),
		WorkingDirectory: home,
	})
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if err := stateStore.ReconcileManagedLaunches(context.Background(), launchInspector{err: errors.New("pending lease inspected")}); err != nil {
		t.Fatalf("ReconcileManagedLaunches() error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), plan.LeaseID, 9002); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	record, err := stateStore.GetManagedLaunch(context.Background(), plan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch() error = %v", err)
	}
	if record.State != launch.StateRunning || record.ProcessID != 9002 {
		t.Fatalf("record = %#v, want running lease with process 9002", record)
	}
}

func TestPrepareLaunchRejectsNonReadyProfilesAndInvalidLeases(t *testing.T) {
	testRoot := t.TempDir()
	databasePath := filepath.Join(testRoot, "codex-folio.sqlite3")
	secureVault, err := vault.NewInMemoryVault(make([]byte, 32), "launch-invalid")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	stateStore, err := OpenWithOptions(Options{Path: databasePath, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	if err := stateStore.CreatePendingProfile(context.Background(), profile.PendingProfile{ID: "pending-1", Alias: "Pending", DisplayName: "Pending"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}

	_, err = stateStore.PrepareLaunch(context.Background(), launch.PrepareRequest{
		Alias:            "pending",
		Executable:       filepath.Join(testRoot, "codex"),
		WorkingDirectory: filepath.Join(testRoot, "workspace"),
	})
	if err == nil || !errors.Is(err, launch.ErrProfileUnavailable) {
		t.Fatalf("PrepareLaunch() error = %v, want unavailable profile", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(context.Background(), "missing", 1); err == nil || !errors.Is(err, launch.ErrLeaseInvalid) {
		t.Fatalf("MarkManagedLaunchStarted() error = %v, want invalid lease", err)
	}
	if err := stateStore.MarkManagedLaunchExited(context.Background(), "missing", -1); err == nil || apperrors.Code(err) != apperrors.LaunchProcessStatusInvalid {
		t.Fatalf("MarkManagedLaunchExited() error = %v, want invalid process status", err)
	}
}

func readyLaunchStore(t *testing.T) (*Store, vault.Vault, string, string) {
	return readyLaunchStoreWithClock(t, launchClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)})
}

func readyLaunchStoreWithClock(t *testing.T, clock Clock) (*Store, vault.Vault, string, string) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	home := filepath.Join(t.TempDir(), "managed-home")
	if err := mkdirForLaunchTest(home); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	if err := mkdirForLaunchTest(filepath.Join(home, "repository")); err != nil {
		t.Fatalf("MkdirAll(repository) error = %v", err)
	}
	secureVault, err := vault.NewInMemoryVault(make([]byte, 32), "launch-ready")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	stateStore, err := OpenWithOptions(Options{
		Path:  databasePath,
		Clock: clock,
		Vault: secureVault,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}
	profileID := "profile-1"
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: profileID, Alias: "Work", DisplayName: "Work"}); err != nil {
		_ = stateStore.Close()
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetManagedHome(ctx, profileID, profileID, home); err != nil {
		_ = stateStore.Close()
		t.Fatalf("SetManagedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, profileID, stage); err != nil {
			_ = stateStore.Close()
			t.Fatalf("SaveSetupStage(%s) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, profileID); err != nil {
		_ = stateStore.Close()
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	if _, err := stateStore.CompleteInitialSelection(ctx, profileID, "", ""); err != nil {
		_ = stateStore.Close()
		t.Fatalf("CompleteInitialSelection() error = %v", err)
	}
	return stateStore, secureVault, profileID, home
}

func addReadyLaunchProfile(t *testing.T, stateStore *Store, profileID, alias string) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "managed-home")
	if err := mkdirForLaunchTest(home); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: profileID, Alias: alias, DisplayName: alias}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, profileID, profileID, home); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, profileID, stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, profileID); err != nil {
		t.Fatal(err)
	}
	return profileID, home
}

func mkdirForLaunchTest(path string) error {
	return os.MkdirAll(path, 0o700)
}
