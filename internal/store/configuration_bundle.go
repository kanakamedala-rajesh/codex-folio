package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"venkatasudha.com/codex-folio/internal/alerts"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configbundle"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/profile"
)

var ErrConfigurationBundle = errors.New("portable configuration operation failed")

type bundleQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (store *Store) ExportConfiguration(ctx context.Context, includeProjectAliases bool) (configbundle.Bundle, error) {
	if store == nil || store.db == nil {
		return configbundle.Bundle{}, coded(apperrors.StoreReadFailed, ErrConfigurationBundle)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return store.exportConfiguration(ctx, store.db, includeProjectAliases)
}

func (store *Store) SetConfigurationAppearance(ctx context.Context, appearance string) error {
	if store == nil || store.db == nil || (appearance != "system" && appearance != "light" && appearance != "dark") {
		return apperrors.New(apperrors.ConfigurationBundleInvalid, configbundle.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(ctx, `INSERT INTO settings(settings_id,appearance,updated_at) VALUES(1,?,?) ON CONFLICT(settings_id) DO UPDATE SET appearance=excluded.appearance,updated_at=excluded.updated_at`, appearance, formatStoredTime(store.clock.Now()))
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	return nil
}

func (store *Store) exportConfiguration(ctx context.Context, q bundleQuerier, includeProjectAliases bool) (configbundle.Bundle, error) {
	b := configbundle.Bundle{SchemaVersion: configbundle.SchemaVersion, Profiles: []configbundle.Profile{}, ConfigurationPacks: []configbundle.Pack{}, AlertThresholds: []configbundle.Threshold{}, ProjectAliases: []configbundle.ProjectAlias{}}
	rows, err := q.QueryContext(ctx, `SELECT a.alias,p.display_name FROM identity_profiles p JOIN cli_aliases a ON a.profile_id=p.profile_id WHERE NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id=p.profile_id) ORDER BY a.alias COLLATE NOCASE`)
	if err != nil {
		return b, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationBundle, err))
	}
	for rows.Next() {
		var p configbundle.Profile
		if err = rows.Scan(&p.Alias, &p.DisplayName); err != nil {
			rows.Close()
			return b, coded(apperrors.StoreReadFailed, err)
		}
		b.Profiles = append(b.Profiles, p)
	}
	if err = rows.Close(); err != nil {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	rows, err = q.QueryContext(ctx, `SELECT configuration_pack_id,pack_version,content_digest,content_json FROM configuration_pack_versions WHERE state='approved' ORDER BY configuration_pack_id,pack_version`)
	if err != nil {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	for rows.Next() {
		var p configbundle.Pack
		var digest, content []byte
		if err = rows.Scan(&p.ID, &p.Version, &digest, &content); err != nil {
			rows.Close()
			return b, coded(apperrors.StoreReadFailed, err)
		}
		p.Digest = hex.EncodeToString(digest)
		p.Files, err = configpack.UnmarshalFiles(content)
		if err != nil {
			rows.Close()
			return b, err
		}
		b.ConfigurationPacks = append(b.ConfigurationPacks, p)
	}
	if err = rows.Close(); err != nil {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	rows, err = q.QueryContext(ctx, `SELECT a.alias,t.metric_key,t.warning_percent,t.critical_percent FROM alert_thresholds t JOIN cli_aliases a ON a.profile_id=t.profile_id WHERE NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id=t.profile_id) ORDER BY a.alias COLLATE NOCASE,t.metric_key`)
	if err != nil {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	for rows.Next() {
		var t configbundle.Threshold
		if err = rows.Scan(&t.ProfileAlias, &t.MetricKey, &t.WarningPercent, &t.CriticalPercent); err != nil {
			rows.Close()
			return b, coded(apperrors.StoreReadFailed, err)
		}
		b.AlertThresholds = append(b.AlertThresholds, t)
	}
	if err = rows.Close(); err != nil {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	b.OperationalPreferences.CollectionActiveSeconds = 300
	b.OperationalPreferences.CollectionIdleSeconds = 1800
	b.OperationalPreferences.Appearance = "system"
	var appearance sql.NullString
	err = q.QueryRowContext(ctx, `SELECT collection_active_interval_seconds,collection_idle_interval_seconds,appearance FROM settings WHERE settings_id=1`).Scan(&b.OperationalPreferences.CollectionActiveSeconds, &b.OperationalPreferences.CollectionIdleSeconds, &appearance)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, coded(apperrors.StoreReadFailed, err)
	}
	if appearance.Valid && appearance.String != "" {
		b.OperationalPreferences.Appearance = appearance.String
	}
	if includeProjectAliases {
		rows, err = q.QueryContext(ctx, `SELECT repository_basename,project_alias FROM project_identities WHERE repository_basename <> '' ORDER BY lower(repository_basename),project_identity_id`)
		if err != nil {
			return b, coded(apperrors.StoreReadFailed, err)
		}
		aliases := map[string][]configbundle.ProjectAlias{}
		for rows.Next() {
			var item configbundle.ProjectAlias
			if err = rows.Scan(&item.RepositoryBasename, &item.Alias); err != nil {
				rows.Close()
				return b, coded(apperrors.StoreReadFailed, err)
			}
			key := strings.ToLower(item.RepositoryBasename)
			aliases[key] = append(aliases[key], item)
		}
		if err = rows.Close(); err != nil {
			return b, coded(apperrors.StoreReadFailed, err)
		}
		for _, items := range aliases {
			if len(items) == 1 {
				b.ProjectAliases = append(b.ProjectAliases, items[0])
			}
		}
	}
	configbundle.Sort(&b)
	return b, nil
}

func (store *Store) PreviewConfigurationImport(ctx context.Context, b configbundle.Bundle) (configbundle.Preview, error) {
	if store == nil || store.db == nil || validateConfigurationBundle(b) != nil {
		return configbundle.Preview{}, apperrors.New(apperrors.ConfigurationBundleInvalid, configbundle.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return store.previewConfigurationImport(ctx, store.db, b)
}
func (store *Store) previewConfigurationImport(ctx context.Context, q bundleQuerier, b configbundle.Bundle) (configbundle.Preview, error) {
	local, err := store.exportConfiguration(ctx, q, true)
	if err != nil {
		return configbundle.Preview{}, err
	}
	conflicts := []configbundle.Conflict{}
	quarantinedProfiles := map[string]bool{}
	rows, err := q.QueryContext(ctx, `SELECT a.alias FROM cli_aliases a JOIN profile_quarantine q ON q.profile_id=a.profile_id ORDER BY a.alias COLLATE NOCASE`)
	if err != nil {
		return configbundle.Preview{}, coded(apperrors.StoreReadFailed, err)
	}
	for rows.Next() {
		var alias string
		if err = rows.Scan(&alias); err != nil {
			rows.Close()
			return configbundle.Preview{}, coded(apperrors.StoreReadFailed, err)
		}
		quarantinedProfiles[strings.ToLower(alias)] = true
	}
	if err = rows.Close(); err != nil {
		return configbundle.Preview{}, coded(apperrors.StoreReadFailed, err)
	}
	localProfiles := map[string]configbundle.Profile{}
	for _, p := range local.Profiles {
		localProfiles[strings.ToLower(p.Alias)] = p
	}
	for _, p := range b.Profiles {
		key := strings.ToLower(p.Alias)
		if quarantinedProfiles[key] {
			conflicts = append(conflicts, configbundle.Conflict{Key: "profile:" + key, Kind: "quarantined_profile", Detail: "A quarantined local profile already reserves this alias; restore or purge it before importing the profile definition.", Resolutions: []string{configbundle.ResolutionSkip}})
		} else if current, ok := localProfiles[key]; ok && current.DisplayName != p.DisplayName {
			conflicts = append(conflicts, configbundle.Conflict{Key: "profile:" + strings.ToLower(p.Alias), Kind: "existing_profile", Detail: fmt.Sprintf("A local profile already uses this alias; imported display name is %q.", p.DisplayName), Resolutions: []string{configbundle.ResolutionKeepLocal, configbundle.ResolutionUseImported}})
		}
	}
	localPacks := map[string]configbundle.Pack{}
	for _, p := range local.ConfigurationPacks {
		localPacks[p.ID+"@"+p.Version] = p
	}
	for _, p := range b.ConfigurationPacks {
		if lp, ok := localPacks[p.ID+"@"+p.Version]; ok && lp.Digest != p.Digest {
			conflicts = append(conflicts, configbundle.Conflict{Key: "pack:" + p.ID + "@" + p.Version, Kind: "different_immutable_pack", Detail: fmt.Sprintf("The local immutable version differs; imported digest is %s and cannot overwrite it.", p.Digest), Resolutions: []string{configbundle.ResolutionSkip, configbundle.ResolutionKeepLocal}})
		}
	}
	localThresholds := map[string]configbundle.Threshold{}
	for _, threshold := range local.AlertThresholds {
		localThresholds[strings.ToLower(threshold.ProfileAlias)+"\x00"+threshold.MetricKey] = threshold
	}
	for _, threshold := range b.AlertThresholds {
		key := strings.ToLower(threshold.ProfileAlias) + "\x00" + threshold.MetricKey
		if current, ok := localThresholds[key]; ok && (current.WarningPercent != threshold.WarningPercent || current.CriticalPercent != threshold.CriticalPercent) {
			conflicts = append(conflicts, configbundle.Conflict{Key: "threshold:" + strings.ToLower(threshold.ProfileAlias) + ":" + threshold.MetricKey, Kind: "different_alert_threshold", Detail: fmt.Sprintf("The local alert threshold differs; imported warning is %.2f%% and critical is %.2f%%.", threshold.WarningPercent, threshold.CriticalPercent), Resolutions: []string{configbundle.ResolutionKeepLocal, configbundle.ResolutionUseImported}})
		}
	}
	localProjects := map[string]configbundle.ProjectAlias{}
	for _, project := range local.ProjectAliases {
		localProjects[strings.ToLower(project.RepositoryBasename)] = project
	}
	for _, project := range b.ProjectAliases {
		key := "project:" + strings.ToLower(project.RepositoryBasename)
		current, ok := localProjects[strings.ToLower(project.RepositoryBasename)]
		if !ok {
			conflicts = append(conflicts, configbundle.Conflict{Key: key, Kind: "missing_local_project", Detail: fmt.Sprintf("No unambiguous local Project Identity has repository basename %q; imported alias %q will be skipped and no path inferred.", project.RepositoryBasename, project.Alias), Resolutions: []string{configbundle.ResolutionSkip}})
		} else if current.Alias != project.Alias {
			conflicts = append(conflicts, configbundle.Conflict{Key: key, Kind: "different_project_alias", Detail: fmt.Sprintf("The local Project Alias differs; imported alias is %q.", project.Alias), Resolutions: []string{configbundle.ResolutionKeepLocal, configbundle.ResolutionUseImported}})
		}
	}
	if local.OperationalPreferences != b.OperationalPreferences {
		conflicts = append(conflicts, configbundle.Conflict{Key: "preferences:operational", Kind: "different_operational_preferences", Detail: fmt.Sprintf("Imported preferences are active %d seconds, idle %d seconds, appearance %q.", b.OperationalPreferences.CollectionActiveSeconds, b.OperationalPreferences.CollectionIdleSeconds, b.OperationalPreferences.Appearance), Resolutions: []string{configbundle.ResolutionKeepLocal, configbundle.ResolutionUseImported}})
	}
	digest, err := configbundle.Digest(struct {
		Bundle    configbundle.Bundle     `json:"bundle"`
		Local     configbundle.Bundle     `json:"local"`
		Conflicts []configbundle.Conflict `json:"conflicts"`
	}{b, local, conflicts})
	if err != nil {
		return configbundle.Preview{}, err
	}
	return configbundle.Preview{Direction: "import", SchemaVersion: configbundle.SchemaVersion, Fields: configbundle.Fields(b), Counts: configbundle.Count(b), ExcludedFields: append([]string(nil), configbundle.ExcludedFields...), Conflicts: conflicts, ConfirmationDigest: digest, Bundle: &b}, nil
}

func (store *Store) ApplyConfigurationImport(ctx context.Context, b configbundle.Bundle, request configbundle.ApplyRequest) (configbundle.ApplyResult, error) {
	if store == nil || store.db == nil || validateConfigurationBundle(b) != nil || !request.Reviewed {
		return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleInvalid, configbundle.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
	}
	defer tx.Rollback()
	preview, err := store.previewConfigurationImport(ctx, tx, b)
	if err != nil {
		return configbundle.ApplyResult{}, err
	}
	if preview.ConfirmationDigest != request.ConfirmationDigest {
		return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleStale, configbundle.ErrStalePreview)
	}
	conflictKeys := make(map[string]configbundle.Conflict, len(preview.Conflicts))
	for _, c := range preview.Conflicts {
		conflictKeys[c.Key] = c
		r := request.Resolutions[c.Key]
		allowed := false
		for _, candidate := range c.Resolutions {
			allowed = allowed || candidate == r
		}
		if !allowed {
			return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleConflict, configbundle.ErrConflict)
		}
	}
	for key := range request.Resolutions {
		if _, ok := conflictKeys[key]; !ok {
			return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleConflict, configbundle.ErrConflict)
		}
	}
	now := formatStoredTime(store.clock.Now())
	result := configbundle.ApplyResult{Applied: true, Skipped: []string{}}
	resolved := map[string]string{}
	skippedProfiles := map[string]bool{}
	for _, p := range b.Profiles {
		aliasKey := strings.ToLower(p.Alias)
		key := "profile:" + aliasKey
		var id string
		err = tx.QueryRowContext(ctx, `SELECT profile_id FROM cli_aliases WHERE alias=? COLLATE NOCASE`, p.Alias).Scan(&id)
		if err == nil {
			if _, conflicted := conflictKeys[key]; conflicted && request.Resolutions[key] == configbundle.ResolutionSkip {
				skippedProfiles[aliasKey] = true
				result.Skipped = append(result.Skipped, key)
				continue
			}
			resolved[aliasKey] = id
			if _, conflicted := conflictKeys[key]; conflicted && request.Resolutions[key] == configbundle.ResolutionUseImported {
				if _, err = tx.ExecContext(ctx, `UPDATE identity_profiles SET display_name=?,updated_at=? WHERE profile_id=?`, p.DisplayName, now, id); err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE pending_profiles SET display_name=?,updated_at=? WHERE pending_profile_id=?`, p.DisplayName, now, id)
				}
				if err != nil {
					return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
				}
				result.Counts.Profiles++
				continue
			}
			result.Skipped = append(result.Skipped, key)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		id, err = newStoreIdentifier("profile")
		if err != nil {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		statements := []struct {
			q string
			a []any
		}{{`INSERT INTO identity_profiles(profile_id,display_name,status,identity_home_id,created_at,updated_at) VALUES(?,?,'pending',NULL,?,?)`, []any{id, p.DisplayName, now, now}}, {`INSERT INTO cli_aliases(alias_id,profile_id,alias,created_at) VALUES(?,?,?,?)`, []any{"alias-" + id, id, p.Alias, now}}, {`INSERT INTO pending_profiles(pending_profile_id,display_name,requested_alias,state,identity_home_id,created_at,updated_at) VALUES(?,?,?,'pending',NULL,?,?)`, []any{id, p.DisplayName, p.Alias, now, now}}, {`INSERT INTO profile_setup_stages(profile_id,discovery_completed,home_completed,authentication_completed,validation_completed,selection_completed,updated_at) VALUES(?,0,0,0,0,0,?)`, []any{id, now}}}
		for _, s := range statements {
			if _, err = tx.ExecContext(ctx, s.q, s.a...); err != nil {
				return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
			}
		}
		resolved[aliasKey] = id
		result.Counts.Profiles++
	}
	for _, p := range b.ConfigurationPacks {
		if configpack.DigestFiles(p.Files) != p.Digest {
			return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleInvalid, configbundle.ErrInvalid)
		}
		var digest []byte
		err = tx.QueryRowContext(ctx, `SELECT content_digest FROM configuration_pack_versions WHERE configuration_pack_id=? AND pack_version=?`, p.ID, p.Version).Scan(&digest)
		if err == nil {
			result.Skipped = append(result.Skipped, "pack:"+p.ID+"@"+p.Version)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		digest, _ = hex.DecodeString(p.Digest)
		content, marshalErr := configpack.MarshalFiles(p.Files)
		if marshalErr != nil {
			return configbundle.ApplyResult{}, marshalErr
		}
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO configuration_packs(configuration_pack_id,pack_version,state,content_digest,created_at) VALUES(?,?,'approved',?,?)`, p.ID, p.Version, digest, now)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO configuration_pack_versions(configuration_pack_version_id,configuration_pack_id,pack_version,state,content_digest,content_json,created_at) VALUES(?,?,?,'approved',?,?,?)`, configurationPackVersionID(configpack.Pack{ID: p.ID, Version: p.Version}), p.ID, p.Version, digest, content, now)
		}
		if err != nil {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		result.Counts.ConfigurationPacks++
	}
	for _, t := range b.AlertThresholds {
		aliasKey := strings.ToLower(t.ProfileAlias)
		key := "threshold:" + aliasKey + ":" + t.MetricKey
		if skippedProfiles[aliasKey] {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		if _, conflicted := conflictKeys[key]; conflicted && request.Resolutions[key] != configbundle.ResolutionUseImported {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		id := resolved[aliasKey]
		threshold := alerts.Threshold{ProfileID: id, MetricKey: t.MetricKey, WarningPercent: t.WarningPercent, CriticalPercent: t.CriticalPercent}
		if !threshold.Valid() {
			return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleInvalid, configbundle.ErrInvalid)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_thresholds(profile_id,metric_key,warning_percent,critical_percent,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(profile_id,metric_key) DO UPDATE SET warning_percent=excluded.warning_percent,critical_percent=excluded.critical_percent,updated_at=excluded.updated_at`, id, t.MetricKey, t.WarningPercent, t.CriticalPercent, now)
		if err != nil {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		result.Counts.AlertThresholds++
	}
	for _, project := range b.ProjectAliases {
		key := "project:" + strings.ToLower(project.RepositoryBasename)
		if _, conflicted := conflictKeys[key]; conflicted && request.Resolutions[key] != configbundle.ResolutionUseImported {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		resultRow, updateErr := tx.ExecContext(ctx, `UPDATE project_identities SET project_alias=?,updated_at=? WHERE lower(repository_basename)=lower(?)`, project.Alias, now, project.RepositoryBasename)
		if updateErr != nil {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, updateErr)
		}
		if affected, affectedErr := resultRow.RowsAffected(); affectedErr != nil || affected != 1 {
			return configbundle.ApplyResult{}, apperrors.New(apperrors.ConfigurationBundleConflict, configbundle.ErrConflict)
		}
		result.Counts.ProjectAliases++
	}
	preferencesKey := "preferences:operational"
	if _, conflicted := conflictKeys[preferencesKey]; conflicted && request.Resolutions[preferencesKey] != configbundle.ResolutionUseImported {
		result.Skipped = append(result.Skipped, preferencesKey)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO settings(settings_id,collection_active_interval_seconds,collection_idle_interval_seconds,appearance,updated_at) VALUES(1,?,?,?,?) ON CONFLICT(settings_id) DO UPDATE SET collection_active_interval_seconds=excluded.collection_active_interval_seconds,collection_idle_interval_seconds=excluded.collection_idle_interval_seconds,appearance=excluded.appearance,updated_at=excluded.updated_at`, b.OperationalPreferences.CollectionActiveSeconds, b.OperationalPreferences.CollectionIdleSeconds, b.OperationalPreferences.Appearance, now)
		if err != nil {
			return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
		}
		result.Counts.OperationalPreferences = 1
	}
	if err = tx.Commit(); err != nil {
		return configbundle.ApplyResult{}, coded(apperrors.StoreWriteFailed, err)
	}
	return result, nil
}

var _ configbundle.Repository = (*Store)(nil)

func validateConfigurationBundle(bundle configbundle.Bundle) error {
	if err := configbundle.Validate(bundle); err != nil {
		return err
	}
	for _, item := range bundle.Profiles {
		if err := profile.ValidateAlias(item.Alias); err != nil {
			return configbundle.ErrInvalid
		}
	}
	for _, item := range bundle.ConfigurationPacks {
		pack := configpack.Pack{ID: item.ID, Version: item.Version, State: configpack.StateApproved, Digest: item.Digest, Files: item.Files}
		if err := pack.Validate(); err != nil {
			return configbundle.ErrInvalid
		}
	}
	return nil
}
