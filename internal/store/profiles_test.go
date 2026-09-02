package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
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
	selected, err := stateStore.CompleteInitialSelection(ctx, "profile-1")
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
