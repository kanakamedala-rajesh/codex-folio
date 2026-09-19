package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestProfileStatePersistsEachSetupStageAndKeepsHomeOpaque(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "profiles.sqlite3")
	homePath := filepath.Join(t.TempDir(), "managed-homes", "profile-1")
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x4a}, 32), "profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	clock := profileStoreClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)}
	stateStore, err := OpenWithOptions(Options{Path: databasePath, Clock: clock, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}

	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "home-1", homePath); err != nil {
		t.Fatalf("SetManagedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatalf("SaveSetupStage(%q) error = %v", stage, err)
		}
	}
	pending, err := stateStore.FindPendingProfile(ctx, "wOrK")
	if err != nil {
		t.Fatalf("FindPendingProfile() error = %v", err)
	}
	if pending.IdentityHomePath != homePath || pending.IdentityHomeOwnership != profile.HomeOwnershipManaged || !pending.Stages.Discovery || !pending.Stages.Home || pending.Stages.Authentication {
		t.Fatalf("pending = %#v, want discovered managed-home state", pending)
	}

	if err := stateStore.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(databaseBytes, []byte(homePath)) {
		t.Fatalf("database contains plaintext managed home path %q", homePath)
	}

	stateStore, err = OpenWithOptions(Options{Path: databasePath, Clock: clock, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	pending, err = stateStore.FindPendingProfile(ctx, "WORK")
	if err != nil {
		t.Fatalf("FindPendingProfile() after reopen error = %v", err)
	}
	if pending.IdentityHomePath != homePath || !pending.Stages.Discovery || !pending.Stages.Home {
		t.Fatalf("reopened pending = %#v, want persisted stages and home", pending)
	}
	if err := stateStore.SaveSetupStage(ctx, "profile-1", profile.StageAuthentication); err != nil {
		t.Fatalf("SaveSetupStage(authentication) error = %v", err)
	}
	if err := stateStore.SaveSetupStage(ctx, "profile-1", profile.StageValidation); err != nil {
		t.Fatalf("SaveSetupStage(validation) error = %v", err)
	}
	ready, err := stateStore.PromotePendingProfile(ctx, "profile-1")
	if err != nil {
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	if ready.Status != profile.StatusReady || ready.Selected {
		t.Fatalf("ready = %#v, want ready and not selected before initial selection", ready)
	}
	selected, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "", "")
	if err != nil {
		t.Fatalf("CompleteInitialSelection() error = %v", err)
	}
	if selected.Status != profile.StatusReady || !selected.Selected {
		t.Fatalf("selected = %#v, want ready and selected", selected)
	}
	if _, err := stateStore.GetPendingProfile(ctx, "profile-1"); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("GetPendingProfile() after selection error = %v, want not-found", err)
	}
	var stageSelectionValue int
	if err := stateStore.db.QueryRowContext(ctx, "SELECT selection_completed FROM profile_setup_stages WHERE profile_id = ?", "profile-1").Scan(&stageSelectionValue); err != nil {
		t.Fatalf("read selection stage: %v", err)
	}
	var pendingCount int
	if err := stateStore.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pending_profiles").Scan(&pendingCount); err != nil {
		t.Fatalf("read pending count: %v", err)
	}
	var selectedProfile string
	if err := stateStore.db.QueryRowContext(ctx, "SELECT profile_id FROM selected_profile WHERE selection_id = 1").Scan(&selectedProfile); err != nil {
		t.Fatalf("read selected profile: %v", err)
	}
	if stageSelectionValue != 1 || pendingCount != 0 || selectedProfile != "profile-1" {
		t.Fatalf("selection state = stage:%d pending:%d selected:%q, want 1, 0, profile-1", stageSelectionValue, pendingCount, selectedProfile)
	}
}

func TestProfileStateRejectsAliasCollisionAndIncompletePromotion(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	first := profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}
	if err := stateStore.CreatePendingProfile(ctx, first); err != nil {
		t.Fatalf("first CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-2", Alias: "work", DisplayName: "Other"}); err == nil || apperrors.Code(err) != apperrors.ProfileAliasTaken {
		t.Fatalf("case-insensitive collision error = %v, want alias-taken", err)
	}
	if _, err := stateStore.PromotePendingProfile(ctx, first.ID); err == nil || apperrors.Code(err) != apperrors.ProfileValidationFailed {
		t.Fatalf("incomplete promotion error = %v, want validation-failed", err)
	}
}

func TestProfileSelectionAndPackAssignmentCommitTogether(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "Work", DisplayName: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.SetManagedHome(ctx, "profile-1", "home-1", filepath.Join(t.TempDir(), "home")); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "missing", "1"); !errors.Is(err, configpack.ErrNotFound) {
		t.Fatalf("CompleteInitialSelection() error = %v, want missing pack", err)
	}
	pending, err := stateStore.GetPendingProfile(ctx, "profile-1")
	if err != nil || pending.Status != profile.StatusPending || pending.Stages.Selection {
		t.Fatalf("pending after assignment failure = %#v/%v", pending, err)
	}
	var selected int
	if err := stateStore.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM selected_profile WHERE profile_id = 'profile-1'`).Scan(&selected); err != nil || selected != 0 {
		t.Fatalf("selected rows = %d/%v, want 0", selected, err)
	}
}

func TestProfileInventoryAndEditsPersistWithoutChangingIdentity(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	ctx := context.Background()
	home := addReadyProfile(t, stateStore, "profile-1", "Work")
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-2", Alias: "Pending", DisplayName: "Pending"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	plan, err := stateStore.PrepareLaunch(ctx, launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: home})
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	newAlias, displayName, email, workspace := "Client", "Client work", "user@example.com", "Example"
	updated, err := stateStore.EditProfile(ctx, "work", profile.ProfileEdits{
		Alias: &newAlias, DisplayName: &displayName, Email: &email, Workspace: &workspace,
	})
	if err != nil {
		t.Fatalf("EditProfile() error = %v", err)
	}
	if updated.ID != "profile-1" || updated.IdentityHomeID != "profile-1" || updated.IdentityHomePath != home || updated.Alias != newAlias {
		t.Fatalf("updated profile = %#v, want stable identity/home and new alias", updated)
	}
	if _, err := stateStore.GetProfile(ctx, "work"); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("old alias lookup error = %v, want not found", err)
	}
	launchRecord, err := stateStore.GetManagedLaunch(ctx, plan.LeaseID)
	if err != nil || launchRecord.ProfileID != "profile-1" {
		t.Fatalf("launch after alias edit = %#v/%v, want immutable profile-1 reference", launchRecord, err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stateStore, err = OpenWithOptions(Options{Path: stateStore.Path(), Clock: profileStoreClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)}, Vault: stateStore.vault})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	profiles, err := stateStore.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles() error = %v", err)
	}
	if len(profiles) != 2 || profiles[0].ID != "profile-1" || profiles[0].Email != email || profiles[0].Workspace != workspace || profiles[1].Status != profile.StatusPending {
		t.Fatalf("profiles = %#v, want persisted ready and pending inventory", profiles)
	}
	collision := "pending"
	if _, err := stateStore.EditProfile(ctx, "client", profile.ProfileEdits{Alias: &collision}); apperrors.Code(err) != apperrors.ProfileAliasTaken {
		t.Fatalf("case-insensitive edit collision error = %v, want alias taken", err)
	}
	pendingAlias := "Setup"
	if _, err := stateStore.EditProfile(ctx, "pending", profile.ProfileEdits{Alias: &pendingAlias, Email: &email, Workspace: &workspace}); err != nil {
		t.Fatalf("pending metadata edit with duplicate display labels error = %v", err)
	}
	pending, err := stateStore.FindPendingProfile(ctx, "setup")
	if err != nil || pending.ID != "profile-2" {
		t.Fatalf("pending lookup after alias edit = %#v/%v, want stable profile-2", pending, err)
	}
}

func TestSelectProfilePersistsAndWarnsWithoutChangingRunningLaunches(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	ctx := context.Background()
	firstHome := addReadyProfile(t, stateStore, "profile-1", "Work")
	addReadyProfile(t, stateStore, "profile-2", "Personal")
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "", ""); err != nil {
		t.Fatalf("CompleteInitialSelection() error = %v", err)
	}
	plan, err := stateStore.PrepareLaunch(ctx, launch.PrepareRequest{
		Alias: "Work", Executable: filepath.Join(firstHome, "codex"), WorkingDirectory: firstHome,
	})
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(ctx, plan.LeaseID, 1234); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	personalPlan, err := stateStore.PrepareLaunch(ctx, launch.PrepareRequest{
		Alias: "Personal", Executable: filepath.Join(firstHome, "codex"), WorkingDirectory: firstHome,
	})
	if err != nil {
		t.Fatalf("PrepareLaunch(Personal) error = %v", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(ctx, personalPlan.LeaseID, 5678); err != nil {
		t.Fatalf("MarkManagedLaunchStarted(Personal) error = %v", err)
	}

	result, err := stateStore.SelectProfile(ctx, "personal")
	if err != nil {
		t.Fatalf("SelectProfile() error = %v", err)
	}
	if result.Profile.ID != "profile-2" || !result.Profile.Selected || len(result.Warnings) != 1 {
		t.Fatalf("selection = %#v, want selected personal profile and one running-launch warning", result)
	}
	launchRecord, err := stateStore.GetManagedLaunch(ctx, plan.LeaseID)
	if err != nil {
		t.Fatalf("GetManagedLaunch() error = %v", err)
	}
	if launchRecord.ProfileID != "profile-1" {
		t.Fatalf("running Launch Profile = %q, want immutable profile-1", launchRecord.ProfileID)
	}
	personalLaunch, err := stateStore.GetManagedLaunch(ctx, personalPlan.LeaseID)
	if err != nil || personalLaunch.ProfileID != "profile-2" {
		t.Fatalf("Personal running Launch Profile = %#v/%v, want immutable profile-2", personalLaunch, err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stateStore, err = OpenWithOptions(Options{Path: stateStore.Path(), Clock: profileStoreClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)}, Vault: stateStore.vault})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	profiles, err := stateStore.ListEligibleProfiles(ctx)
	if err != nil {
		t.Fatalf("ListEligibleProfiles() error = %v", err)
	}
	if len(profiles) != 2 || profiles[0].Alias != "Personal" || !profiles[0].Selected || profiles[1].Alias != "Work" || profiles[1].Selected {
		t.Fatalf("eligible profiles = %#v, want selected profile first after restart", profiles)
	}
}

func TestSelectProfileRejectsMissingAndNonReadyProfiles(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "pending-1", Alias: "Pending", DisplayName: "Pending"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	for _, alias := range []string{"pending", "missing"} {
		if _, err := stateStore.SelectProfile(ctx, alias); err == nil || apperrors.Code(err) != apperrors.ProfileNotSelectable {
			t.Fatalf("SelectProfile(%q) error = %v, want not-selectable", alias, err)
		}
	}
}

func TestReferencedProfileStatePersistsOwnershipAndOpaqueHome(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "profiles.sqlite3")
	homePath := filepath.Join(t.TempDir(), "codex-home")
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x72}, 32), "referenced-profile-generation")
	if err != nil {
		t.Fatalf("NewInMemoryVault() error = %v", err)
	}
	clock := profileStoreClock{now: time.Date(2026, time.September, 2, 13, 0, 0, 0, time.UTC)}
	stateStore, err := OpenWithOptions(Options{Path: databasePath, Clock: clock, Vault: secureVault})
	if err != nil {
		t.Fatalf("OpenWithOptions() error = %v", err)
	}

	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-1", Alias: "external", DisplayName: "External"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetReferencedHome(ctx, "profile-1", "home-1", homePath); err != nil {
		t.Fatalf("SetReferencedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-1", stage); err != nil {
			t.Fatalf("SaveSetupStage(%q) error = %v", stage, err)
		}
	}
	pending, err := stateStore.FindPendingProfile(ctx, "EXTERNAL")
	if err != nil {
		t.Fatalf("FindPendingProfile() error = %v", err)
	}
	if pending.IdentityHomePath != homePath || pending.IdentityHomeOwnership != profile.HomeOwnershipReferenced {
		t.Fatalf("pending = %#v, want persisted referenced home", pending)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	stateStore, err = OpenWithOptions(Options{Path: databasePath, Clock: clock, Vault: secureVault})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	pending, err = stateStore.FindPendingProfile(ctx, "external")
	if err != nil {
		t.Fatalf("FindPendingProfile() after reopen error = %v", err)
	}
	if pending.IdentityHomePath != homePath || pending.IdentityHomeOwnership != profile.HomeOwnershipReferenced {
		t.Fatalf("reopened pending = %#v, want referenced home", pending)
	}
	ready, err := stateStore.PromotePendingProfile(ctx, "profile-1")
	if err != nil {
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	if ready.Status != profile.StatusReady || ready.IdentityHomeOwnership != profile.HomeOwnershipReferenced {
		t.Fatalf("ready = %#v, want ready referenced profile", ready)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(databaseBytes, []byte(homePath)) {
		t.Fatalf("database contains plaintext referenced home path %q", homePath)
	}
}

func TestDocumentedMetadataPersistsEncryptedAndMatchesByEitherIdentifier(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	for _, id := range []string{"profile-1", "profile-2"} {
		if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: id, Alias: id, DisplayName: id}); err != nil {
			t.Fatalf("CreatePendingProfile(%q) error = %v", id, err)
		}
		if err := stateStore.SetManagedHome(ctx, id, "home-"+id, filepath.Join(t.TempDir(), id)); err != nil {
			t.Fatalf("SetManagedHome(%q) error = %v", id, err)
		}
	}
	if duplicate, err := stateStore.SaveDocumentedMetadata(ctx, "profile-1", profile.DocumentedMetadata{LoginIdentity: "login-1", Workspace: "workspace-1"}); err != nil || duplicate {
		t.Fatalf("first metadata save = duplicate:%v error:%v, want no duplicate", duplicate, err)
	}
	duplicate, err := stateStore.SaveDocumentedMetadata(ctx, "profile-2", profile.DocumentedMetadata{Workspace: "workspace-1"})
	if err != nil || !duplicate {
		t.Fatalf("matching workspace save = duplicate:%v error:%v, want duplicate", duplicate, err)
	}
	duplicate, err = stateStore.SaveDocumentedMetadata(ctx, "profile-2", profile.DocumentedMetadata{LoginIdentity: "login-2"})
	if err != nil || duplicate {
		t.Fatalf("distinct login save = duplicate:%v error:%v, want no duplicate", duplicate, err)
	}
	data, err := os.ReadFile(stateStore.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, value := range []string{"login-1", "workspace-1", "login-2"} {
		if bytes.Contains(data, []byte(value)) {
			t.Fatalf("database contains plaintext documented metadata %q", value)
		}
	}
}

func TestAuthenticationStatePersistsPreferenceAndStatusWithoutExposingHome(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	homePath := addReadyProfile(t, stateStore, "profile-1", "Work")
	ctx := context.Background()
	if err := stateStore.SetAuthenticationState(ctx, "profile-1", profile.StatusNeedsReauthentication, profile.AuthMethodDeviceCode); err != nil {
		t.Fatalf("SetAuthenticationState() error = %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	stateStore, err = OpenWithOptions(Options{Path: stateStore.Path(), Vault: stateStore.vault})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	item, err := stateStore.GetProfile(ctx, "work")
	if err != nil {
		t.Fatalf("GetProfile() error = %v", err)
	}
	if item.Status != profile.StatusNeedsReauthentication || item.AuthenticationMethod != profile.AuthMethodDeviceCode || item.IdentityHomePath != homePath {
		t.Fatalf("profile = %#v, want persisted reauthentication state and home", item)
	}
	data, err := os.ReadFile(stateStore.Path())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if bytes.Contains(data, []byte(homePath)) {
		t.Fatalf("database contains plaintext Identity Home path %q", homePath)
	}
}

func TestProfileRemovalHonorsOwnershipSelectionAndRunningLaunches(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	managedHome := addReadyProfile(t, stateStore, "profile-1", "Work")
	_ = addReadyProfile(t, stateStore, "profile-2", "Personal")
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "", ""); err != nil {
		t.Fatalf("CompleteInitialSelection() error = %v", err)
	}

	plan, err := stateStore.PrepareLaunch(ctx, launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(managedHome, "codex"), WorkingDirectory: managedHome})
	if err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
	if _, err := stateStore.BeginProfileRemoval(ctx, "Work", "Personal"); !errors.Is(err, profile.ErrRunningLaunch) {
		t.Fatalf("BeginProfileRemoval() pending error = %v, want ErrRunningLaunch", err)
	}
	if err := stateStore.MarkManagedLaunchStarted(ctx, plan.LeaseID, 42); err != nil {
		t.Fatalf("MarkManagedLaunchStarted() error = %v", err)
	}
	if _, err := stateStore.BeginProfileRemoval(ctx, "Work", "Personal"); !errors.Is(err, profile.ErrRunningLaunch) {
		t.Fatalf("BeginProfileRemoval() running error = %v, want ErrRunningLaunch", err)
	}
	if err := stateStore.MarkManagedLaunchExited(ctx, plan.LeaseID, 0); err != nil {
		t.Fatalf("MarkManagedLaunchExited() error = %v", err)
	}

	record, err := stateStore.BeginProfileRemoval(ctx, "Work", "Personal")
	if err != nil {
		t.Fatalf("BeginProfileRemoval() error = %v", err)
	}
	if record.Profile.ID != "profile-1" || record.Profile.IdentityHomePath != managedHome || record.PurgeAfter.Sub(record.QuarantinedAt) != 7*24*time.Hour {
		t.Fatalf("removal record = %#v", record)
	}
	if _, err := stateStore.GetProfile(ctx, "Work"); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("GetProfile(quarantined) error = %v, want not found", err)
	}
	personal, err := stateStore.GetProfile(ctx, "Personal")
	if err != nil || !personal.Selected {
		t.Fatalf("replacement = %#v, error = %v, want selected", personal, err)
	}
	if err := stateStore.CancelProfileRemoval(ctx, record.Profile.ID); err != nil {
		t.Fatalf("CancelProfileRemoval() error = %v", err)
	}
	work, err := stateStore.GetProfile(ctx, "Work")
	if err != nil || !work.Selected {
		t.Fatalf("cancelled removal profile = %#v, error = %v, want selected", work, err)
	}
	record, err = stateStore.BeginProfileRemoval(ctx, "Work", "Personal")
	if err != nil {
		t.Fatalf("second BeginProfileRemoval() error = %v", err)
	}
	if err := stateStore.CompleteProfileQuarantine(ctx, record.Profile.ID); err != nil {
		t.Fatalf("CompleteProfileQuarantine() error = %v", err)
	}
	if err := stateStore.RestoreProfile(ctx, record.Profile.ID); err != nil {
		t.Fatalf("RestoreProfile() error = %v", err)
	}
	restored, err := stateStore.GetProfile(ctx, "Work")
	if err != nil || restored.ID != "profile-1" || restored.IdentityHomeOwnership != profile.HomeOwnershipManaged {
		t.Fatalf("restored = %#v, error = %v", restored, err)
	}
}

func TestReferencedProfileRemovalDeregistersWithoutTouchingExternalHome(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	externalHome := t.TempDir()
	marker := filepath.Join(externalHome, "external-state")
	if err := os.WriteFile(marker, []byte("owned by Codex"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ctx := context.Background()
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: "profile-external", Alias: "External", DisplayName: "External"}); err != nil {
		t.Fatalf("CreatePendingProfile() error = %v", err)
	}
	if err := stateStore.SetReferencedHome(ctx, "profile-external", "home-external", externalHome); err != nil {
		t.Fatalf("SetReferencedHome() error = %v", err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, "profile-external", stage); err != nil {
			t.Fatalf("SaveSetupStage(%q) error = %v", stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, "profile-external"); err != nil {
		t.Fatalf("PromotePendingProfile() error = %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO usage_metrics (metric_key, unit, value_kind, created_at) VALUES ('requests', 'count', 'count', '2026-09-01T00:00:00Z')`,
		`INSERT INTO metric_provenance (provenance_id, source, captured_at, freshness, availability) VALUES ('source-1', 'codex_app_server', '2026-09-01T00:00:00Z', 'fresh', 'available')`,
		`INSERT INTO usage_snapshots (snapshot_id, profile_id, source, source_version, captured_at, status, trigger_reason) VALUES ('snapshot-1', 'profile-external', 'codex_app_server', '', '2026-09-01T00:00:00Z', 'available', 'explicit_refresh')`,
		`INSERT INTO metric_availability (metric_availability_id, profile_id, metric_key, state, checked_at, provenance_id) VALUES ('availability-1', 'profile-external', 'requests', 'available', '2026-09-01T00:00:00Z', 'source-1')`,
		`INSERT INTO usage_observations (observation_id, profile_id, metric_key, provenance_id, metric_availability_id, value, unit, observed_at, snapshot_id) VALUES ('observation-1', 'profile-external', 'requests', 'source-1', 'availability-1', 1, 'count', '2026-09-01T00:00:00Z', 'snapshot-1')`,
		`INSERT INTO alerts (alert_id, condition_key, profile_id, category, kind, severity, state, title, guidance, observed_at, first_seen_at, last_seen_at, occurrence_count) VALUES ('alert-1', 'profile-external|capacity', 'profile-external', 'capacity', 'capacity_warning', 'warning', 'open', 'Capacity warning', '', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', 1)`,
	} {
		if _, err := stateStore.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed analytics: %v", err)
		}
	}
	if err := stateStore.SaveObservedSessions(ctx, []activity.ObservedSessionRecord{{SourceSessionID: "session-1", ProfileID: "profile-external", ProfileAlias: "External", Source: activity.SourceLocalMetadata, SourceVersion: "state_5", StartedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 1, 0, 1, 0, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}
	record, err := stateStore.BeginProfileRemoval(ctx, "External", "")
	if err != nil || record.Action != profile.RemovalDeregistered {
		t.Fatalf("BeginProfileRemoval() = %#v, %v", record, err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "owned by Codex" {
		t.Fatalf("external marker = %q, error = %v", data, err)
	}
	if _, err := stateStore.GetProfile(ctx, "External"); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("GetProfile() error = %v, want not found", err)
	}
	for _, table := range []string{"metric_availability", "usage_observations", "alerts"} {
		var count int
		if err := stateStore.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE profile_id = 'profile-external'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preserved %s rows = %d, error = %v, want 1", table, count, err)
		}
	}
	timeline, err := stateStore.ListActivity(ctx, activity.Filters{})
	if err != nil || len(timeline) != 1 || timeline[0].ProfileID != "profile-external" || timeline[0].ProfileAlias != "profile-external" {
		t.Fatalf("retained activity = %#v/%v", timeline, err)
	}
	exported, err := stateStore.ExportAnalytics(ctx, activity.ExportRequest{Format: "json", Datasets: []string{"usage"}, Scope: usage.ScopeSelectedProfile, ProfileID: "profile-external", ProjectID: "*", From: "all", To: "all"})
	if err != nil || exported.Usage == nil || len(*exported.Usage) != 1 || (*exported.Usage)[0].ProfileAlias != "profile-external" {
		t.Fatalf("retained usage export = %#v/%v", exported, err)
	}
}

func TestExpiredProfileQuarantineCannotBeRestored(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	_ = addReadyProfile(t, stateStore, "profile-1", "Work")
	record, err := stateStore.BeginProfileRemoval(ctx, "Work", "")
	if err != nil {
		t.Fatalf("BeginProfileRemoval() error = %v", err)
	}
	if err := stateStore.CompleteProfileQuarantine(ctx, record.Profile.ID); err != nil {
		t.Fatalf("CompleteProfileQuarantine() error = %v", err)
	}
	if _, err := stateStore.db.ExecContext(ctx, `UPDATE profile_quarantine SET purge_after = ? WHERE profile_id = ?`, "2026-09-01T00:00:00Z", record.Profile.ID); err != nil {
		t.Fatalf("expire quarantine: %v", err)
	}
	if err := stateStore.RestoreProfile(ctx, record.Profile.ID); !errors.Is(err, profile.ErrQuarantineExpired) {
		t.Fatalf("RestoreProfile() error = %v, want ErrQuarantineExpired", err)
	}
}

type profileStoreClock struct{ now time.Time }

func (clock profileStoreClock) Now() time.Time { return clock.now }

func openProfileTestStore(t *testing.T) (*Store, error) {
	t.Helper()
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x3b}, 32), "profile-test-generation")
	if err != nil {
		return nil, err
	}
	return OpenWithOptions(Options{
		Path:  filepath.Join(t.TempDir(), "profiles.sqlite3"),
		Clock: profileStoreClock{now: time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)},
		Vault: secureVault,
	})
}

func addReadyProfile(t *testing.T, stateStore *Store, id, alias string) string {
	t.Helper()
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), id)
	if err := stateStore.CreatePendingProfile(ctx, profile.PendingProfile{ID: id, Alias: alias, DisplayName: alias}); err != nil {
		t.Fatalf("CreatePendingProfile(%q) error = %v", alias, err)
	}
	if err := stateStore.SetManagedHome(ctx, id, id, home); err != nil {
		t.Fatalf("SetManagedHome(%q) error = %v", alias, err)
	}
	for _, stage := range []profile.SetupStage{profile.StageDiscovery, profile.StageHome, profile.StageAuthentication, profile.StageValidation} {
		if err := stateStore.SaveSetupStage(ctx, id, stage); err != nil {
			t.Fatalf("SaveSetupStage(%q, %q) error = %v", alias, stage, err)
		}
	}
	if _, err := stateStore.PromotePendingProfile(ctx, id); err != nil {
		t.Fatalf("PromotePendingProfile(%q) error = %v", alias, err)
	}
	return home
}
