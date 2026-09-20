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

func TestConfigurationBundleExcludesAndExplicitlySkipsQuarantinedProfiles(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	addReadyProfile(t, state, "profile-quarantined", "Work")
	now := formatStoredTime(state.clock.Now())
	if _, err = state.db.Exec(`INSERT INTO alert_thresholds(profile_id,metric_key,warning_percent,critical_percent,updated_at) VALUES('profile-quarantined','codex.primary.used_percent',20,10,?); INSERT INTO profile_quarantine(profile_id,state,was_selected,quarantined_at,purge_after,updated_at) VALUES('profile-quarantined','quarantined',0,?,?,?)`, now, now, now, now); err != nil {
		t.Fatal(err)
	}

	exported, err := state.ExportConfiguration(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Profiles) != 0 || len(exported.AlertThresholds) != 0 {
		t.Fatalf("quarantined export=%#v", exported)
	}
	service, err := configbundle.NewService(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExportPreview(ctx, false); err != nil {
		t.Fatalf("export preview with quarantined threshold: %v", err)
	}

	bundle := configbundle.Bundle{
		SchemaVersion:      configbundle.SchemaVersion,
		Profiles:           []configbundle.Profile{{Alias: "Work", DisplayName: "Imported Work"}},
		ConfigurationPacks: []configbundle.Pack{},
		AlertThresholds:    []configbundle.Threshold{{ProfileAlias: "Work", MetricKey: "codex.primary.used_percent", WarningPercent: 25, CriticalPercent: 15}},
		OperationalPreferences: configbundle.Preferences{
			CollectionActiveSeconds: 300,
			CollectionIdleSeconds:   1800,
			Appearance:              "system",
		},
	}
	preview, err := state.PreviewConfigurationImport(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0].Key != "profile:work" || preview.Conflicts[0].Kind != "quarantined_profile" || len(preview.Conflicts[0].Resolutions) != 1 || preview.Conflicts[0].Resolutions[0] != configbundle.ResolutionSkip {
		t.Fatalf("quarantined preview=%#v", preview)
	}
	if _, err = state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true}); !errors.Is(err, configbundle.ErrConflict) {
		t.Fatalf("unresolved quarantine conflict=%v", err)
	}
	result, err := state.ApplyConfigurationImport(ctx, bundle, configbundle.ApplyRequest{ConfirmationDigest: preview.ConfirmationDigest, Reviewed: true, Resolutions: map[string]string{"profile:work": configbundle.ResolutionSkip}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Counts.Profiles != 0 || result.Counts.AlertThresholds != 0 || len(result.Skipped) != 2 {
		t.Fatalf("quarantined import result=%#v", result)
	}
	var display string
	var warning, critical float64
	if err = state.db.QueryRow(`SELECT display_name FROM identity_profiles WHERE profile_id='profile-quarantined'`).Scan(&display); err != nil {
		t.Fatal(err)
	}
	if err = state.db.QueryRow(`SELECT warning_percent,critical_percent FROM alert_thresholds WHERE profile_id='profile-quarantined' AND metric_key='codex.primary.used_percent'`).Scan(&warning, &critical); err != nil {
		t.Fatal(err)
	}
	if display != "Work" || warning != 20 || critical != 10 {
		t.Fatalf("quarantined state changed: display=%q threshold=%v/%v", display, warning, critical)
	}
}
