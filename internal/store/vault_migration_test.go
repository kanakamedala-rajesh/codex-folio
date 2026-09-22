package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestVaultMigrationAllowlistMatchesEveryProtectedSchemaColumn(t *testing.T) {
	got := make(map[string][]string)
	for _, protected := range vaultProtectedColumns {
		got[protected.table] = append(got[protected.table], protected.column)
	}
	for table := range got {
		sort.Strings(got[table])
	}
	if len(got) != len(sensitiveColumns) {
		t.Fatalf("migration tables = %#v, schema protected tables = %#v", got, sensitiveColumns)
	}
	for table, columns := range sensitiveColumns {
		want := append([]string(nil), columns...)
		sort.Strings(want)
		if !equalStrings(got[table], want) {
			t.Fatalf("migration columns for %s = %#v, want %#v", table, got[table], want)
		}
	}
}

func TestReprotectVaultStatePreservesEveryRetainedProtectedClass(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "codex-folio.sqlite3")
	source, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x31}, 32), "passphrase-generation")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x72}, 32), "native-generation")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	stateStore, err := OpenWithOptions(Options{Path: databasePath, Vault: source, Clock: profileStoreClock{now: now}})
	if err != nil {
		t.Fatal(err)
	}
	home := addReadyProfile(t, stateStore, "profile-1", "Work")
	if _, err := stateStore.CompleteInitialSelection(ctx, "profile-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.SaveDocumentedMetadata(ctx, "profile-1", profile.DocumentedMetadata{LoginIdentity: "login@example.test", Workspace: "workspace-1"}); err != nil {
		t.Fatal(err)
	}
	project := ProjectIdentity{ProjectIdentityID: "project-1", ProjectAlias: "Private", RepositoryBasename: "repo", CanonicalPath: filepath.Join(t.TempDir(), "private", "repo"), CreatedAt: now, UpdatedAt: now}
	if err := stateStore.PutProjectIdentity(ctx, project); err != nil {
		t.Fatal(err)
	}
	goal, completed, pending := "goal", "completed", "pending"
	validation, risks, next, recovery := "validation", "risks", "next", `{"revision":"one"}`
	checkpoint := Checkpoint{CheckpointID: "checkpoint-1", ProjectIdentityID: project.ProjectIdentityID, Status: "draft", Goal: &goal, CompletedWork: &completed, PendingWork: &pending, Validation: &validation, Risks: &risks, NextAction: &next, RecoveryMetadata: &recovery, CreatedAt: now}
	if err := stateStore.PutCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	target := usage.ProfileTarget{ID: "profile-1", Alias: "Work", IdentityHome: home, LoginIdentity: "login@example.test", Workspace: "workspace-1", Selected: true, Eligible: true}
	metric := usage.Registry()[0]
	observation := usage.Observation{Metric: metric, Value: 12, Source: usage.SourceCodexAppServer, SourceVersion: "fixture", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable, ObservedAt: now, CapturedAt: now, WindowTimezone: "UTC"}
	snapshot := usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: "fixture", CapturedAt: now, Status: usage.AvailabilityAvailable, TriggerReason: usage.TriggerExplicitRefresh, Observations: []usage.Observation{observation}, Availability: completeUsageAvailability(now, []usage.MetricAvailability{{MetricKey: metric.Key, State: usage.AvailabilityAvailable, CheckedAt: now, Provenance: usage.ProvenanceProvider}})}
	if _, err := stateStore.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
		t.Fatal(err)
	}
	tx, err := stateStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.aggregateObservation(ctx, tx, target.ID, observation, aggregateSourceScope{LoginIdentity: target.LoginIdentity, Workspace: target.Workspace}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.VerifyProtectedState(ctx); err != nil {
		t.Fatalf("source verification: %v", err)
	}
	if err := stateStore.ReprotectVaultState(ctx, destination); err != nil {
		t.Fatalf("ReprotectVaultState(): %v", err)
	}
	if err := stateStore.VerifyProtectedState(ctx); err != nil {
		t.Fatalf("destination verification before reopen: %v", err)
	}
	if err := stateStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWithVault(databasePath, destination)
	if err != nil {
		t.Fatalf("destination reopen: %v", err)
	}
	defer reopened.Close()
	if err := reopened.VerifyProtectedState(ctx); err != nil {
		t.Fatalf("destination protected-state reopen: %v", err)
	}
	profileState, err := reopened.GetProfile(ctx, "Work")
	if err != nil || profileState.IdentityHomePath != home {
		t.Fatalf("profile after migration = %#v, %v", profileState, err)
	}
	projectState, err := reopened.GetProjectIdentity(ctx, project.ProjectIdentityID)
	if err != nil || projectState.CanonicalPath != project.CanonicalPath {
		t.Fatalf("project after migration = %#v, %v", projectState, err)
	}
	checkpointState, err := reopened.GetCheckpoint(ctx, checkpoint.CheckpointID)
	if err != nil || checkpointState.Goal == nil || *checkpointState.Goal != goal || checkpointState.RecoveryMetadata == nil || *checkpointState.RecoveryMetadata != recovery {
		t.Fatalf("checkpoint after migration = %#v, %v", checkpointState, err)
	}
	latest, err := reopened.LatestUsageSnapshot(ctx, target)
	if err != nil || latest.LoginIdentity != target.LoginIdentity || latest.Workspace != target.Workspace {
		t.Fatalf("usage after migration = %#v, %v", latest, err)
	}
	aggregates, err := reopened.ListUsageAggregates(ctx, usage.HistoryScope{ProfileID: target.ID, ProjectID: "*", From: "all", To: "all", Classes: []string{"aggregates"}})
	if err != nil || len(aggregates) != 1 {
		t.Fatalf("aggregates after migration = %#v, %v", aggregates, err)
	}

	oldGeneration, err := OpenWithVault(databasePath, source)
	if err != nil {
		t.Fatal(err)
	}
	defer oldGeneration.Close()
	if err := oldGeneration.VerifyProtectedState(ctx); err == nil {
		t.Fatal("old passphrase generation still opened reprotected fields")
	}
}

func TestReprotectVaultStateRollsBackEveryFieldWhenDestinationFails(t *testing.T) {
	ctx := context.Background()
	source, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x19}, 32), "source-generation")
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := OpenWithVault(filepath.Join(t.TempDir(), "codex-folio.sqlite3"), source)
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	project := ProjectIdentity{ProjectIdentityID: "project-1", ProjectAlias: "Private", RepositoryBasename: "repo", CanonicalPath: filepath.Join(t.TempDir(), "repo"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := stateStore.PutProjectIdentity(ctx, project); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err := stateStore.db.QueryRowContext(ctx, `SELECT canonical_path_ciphertext FROM project_identities WHERE project_identity_id = ?`, project.ProjectIdentityID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	destination, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x28}, 32), Generation: "destination-generation"})
	if err != nil {
		t.Fatal(err)
	}
	destination.SetUnavailable(errors.New("injected destination failure"))
	if err := stateStore.ReprotectVaultState(ctx, destination); err == nil {
		t.Fatal("destination failure was accepted")
	}
	var after []byte
	if err := stateStore.db.QueryRowContext(ctx, `SELECT canonical_path_ciphertext FROM project_identities WHERE project_identity_id = ?`, project.ProjectIdentityID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed migration changed protected database state")
	}
	got, err := stateStore.GetProjectIdentity(ctx, project.ProjectIdentityID)
	if err != nil || got.CanonicalPath != project.CanonicalPath {
		t.Fatalf("source state after rollback = %#v, %v", got, err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
