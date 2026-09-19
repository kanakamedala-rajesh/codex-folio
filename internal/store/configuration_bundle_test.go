package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/configbundle"
	"venkatasudha.com/codex-folio/internal/configpack"
)

func TestConfigurationBundlePreviewApplyCreatesOnlyPendingProfiles(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "bundle.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	files := map[string]string{"config.toml": "model = \"safe\"\n"}
	bundle := configbundle.Bundle{SchemaVersion: 1, Profiles: []configbundle.Profile{{Alias: "Imported", DisplayName: "Imported profile"}}, ConfigurationPacks: []configbundle.Pack{{ID: "team", Version: "1", Digest: configpack.DigestFiles(files), Files: files}}, AlertThresholds: []configbundle.Threshold{{ProfileAlias: "Imported", MetricKey: "codex.primary.used_percent", WarningPercent: 20, CriticalPercent: 10}}, OperationalPreferences: configbundle.Preferences{CollectionActiveSeconds: 600, CollectionIdleSeconds: 3600, Appearance: "dark"}}
	preview, err := state.PreviewConfigurationImport(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0].Key != "preferences:operational" || preview.ConfirmationDigest == "" || preview.Bundle == nil || preview.Bundle.Profiles[0].Alias != "Imported" {
		t.Fatalf("preview=%#v", preview)
	}
	if _, err = state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest}); err == nil {
		t.Fatal("applied without explicit review")
	}
	result, err := state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true, Resolutions: map[string]string{"preferences:operational": configbundle.ResolutionUseImported}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Counts.Profiles != 1 || result.Counts.ConfigurationPacks != 1 {
		t.Fatalf("result=%#v", result)
	}
	pending, err := state.FindPendingProfile(ctx, "Imported")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != "pending" || pending.IdentityHomeID != "" || pending.Stages.Discovery || pending.Stages.Home || pending.Stages.Authentication || pending.Stages.Validation || pending.Stages.Selection {
		t.Fatalf("unsafe imported profile=%#v", pending)
	}
	var selected int
	if err = state.db.QueryRow(`SELECT count(*) FROM selected_profile WHERE profile_id=?`, pending.ID).Scan(&selected); err != nil || selected != 0 {
		t.Fatalf("selection=%d err=%v", selected, err)
	}
	if _, err = state.GetConfigurationPack(ctx, "team", "1"); err != nil {
		t.Fatal(err)
	}
	var assignments int
	_ = state.db.QueryRow(`SELECT count(*) FROM configuration_pack_assignments`).Scan(&assignments)
	if assignments != 0 {
		t.Fatal("import assigned a pack")
	}
}

func TestConfigurationBundlePortsAppearanceAndOptionalPathFreeProjectAlias(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "bundle-project.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := formatStoredTime(state.clock.Now())
	if _, err = state.db.Exec(`INSERT INTO project_identities(project_identity_id,project_alias,repository_basename,canonical_path_ciphertext,created_at,updated_at) VALUES('project-1','Local label','folio',X'0102',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if err = state.SetConfigurationAppearance(ctx, "light"); err != nil {
		t.Fatal(err)
	}
	withoutProjects, err := state.ExportConfiguration(ctx, false)
	if err != nil || len(withoutProjects.ProjectAliases) != 0 || withoutProjects.OperationalPreferences.Appearance != "light" {
		t.Fatalf("default export=%#v err=%v", withoutProjects, err)
	}
	withProjects, err := state.ExportConfiguration(ctx, true)
	if err != nil || len(withProjects.ProjectAliases) != 1 || withProjects.ProjectAliases[0].RepositoryBasename != "folio" {
		t.Fatalf("project export=%#v err=%v", withProjects.ProjectAliases, err)
	}
	encoded, _ := json.Marshal(withProjects)
	if strings.Contains(string(encoded), "project-1") || strings.Contains(string(encoded), "canonical") || strings.Contains(string(encoded), "0102") {
		t.Fatalf("export exposed local identity or path material: %s", encoded)
	}
	withProjects.ProjectAliases[0].Alias = "Imported label"
	withProjects.OperationalPreferences.Appearance = "dark"
	preview, err := state.PreviewConfigurationImport(ctx, withProjects)
	if err != nil {
		t.Fatal(err)
	}
	resolutions := map[string]string{}
	for _, conflict := range preview.Conflicts {
		resolutions[conflict.Key] = configbundle.ResolutionUseImported
	}
	if _, err = state.ApplyConfigurationImport(ctx, withProjects, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true, Resolutions: resolutions}); err != nil {
		t.Fatal(err)
	}
	var alias, appearance string
	if err = state.db.QueryRow(`SELECT project_alias FROM project_identities WHERE project_identity_id='project-1'`).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if err = state.db.QueryRow(`SELECT appearance FROM settings WHERE settings_id=1`).Scan(&appearance); err != nil {
		t.Fatal(err)
	}
	if alias != "Imported label" || appearance != "dark" {
		t.Fatalf("imported alias/appearance=%q/%q", alias, appearance)
	}
}

func TestConfigurationBundleRequiresConflictResolutionAndRejectsStalePreview(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "bundle.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	now := formatStoredTime(state.clock.Now())
	_, err = state.db.Exec(`INSERT INTO identity_profiles(profile_id,display_name,status,created_at,updated_at) VALUES('profile-local','Local','pending',?,?); INSERT INTO cli_aliases(alias_id,profile_id,alias,created_at) VALUES('alias-local','profile-local','Work',?)`, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	bundle := configbundle.Bundle{SchemaVersion: 1, Profiles: []configbundle.Profile{{Alias: "Work", DisplayName: "Remote"}}, ConfigurationPacks: []configbundle.Pack{}, AlertThresholds: []configbundle.Threshold{}, OperationalPreferences: configbundle.Preferences{CollectionActiveSeconds: 600, CollectionIdleSeconds: 3600, Appearance: "dark"}}
	preview, err := state.PreviewConfigurationImport(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 2 {
		t.Fatalf("conflicts=%#v", preview.Conflicts)
	}
	if _, err = state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true}); !errors.Is(err, configbundle.ErrConflict) {
		t.Fatalf("unresolved err=%v", err)
	}
	_, err = state.db.Exec(`INSERT INTO settings(settings_id,collection_active_interval_seconds,collection_idle_interval_seconds,updated_at) VALUES(1,900,1800,?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true, Resolutions: map[string]string{"profile:work": "keep_local", "preferences:operational": "keep_local"}}); !errors.Is(err, configbundle.ErrStalePreview) {
		t.Fatalf("stale err=%v", err)
	}
	var display string
	_ = state.db.QueryRow(`SELECT display_name FROM identity_profiles WHERE profile_id='profile-local'`).Scan(&display)
	if display != "Local" {
		t.Fatalf("state changed: %q", display)
	}
}
