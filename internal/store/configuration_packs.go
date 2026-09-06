package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/profile"
)

var ErrConfigurationPackState = errors.New("configuration pack state could not be persisted")

var _ configpack.Repository = (*Store)(nil)

func (store *Store) CreateConfigurationPack(ctx context.Context, pack configpack.Pack) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrConfigurationPackState)
	}
	if pack.State != configpack.StateDraft {
		return configurationPackInvalid()
	}
	digest, content, err := packStorage(pack)
	if err != nil {
		return err
	}
	now, err := store.configurationPackNow()
	if err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	encodedNow := formatStoredTime(now)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO configuration_packs (
		configuration_pack_id, pack_version, state, content_digest, created_at
	) VALUES (?, ?, 'draft', ?, ?)`, pack.ID, pack.Version, digest, encodedNow); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO configuration_pack_versions (
		configuration_pack_version_id, configuration_pack_id, pack_version, state,
		content_digest, content_json, created_at
	) VALUES (?, ?, ?, 'draft', ?, ?, ?)`, configurationPackVersionID(pack), pack.ID, pack.Version, digest, content, encodedNow); err != nil {
		rollback()
		return apperrors.New(apperrors.ConfigurationPackInvalid, errors.Join(configpack.ErrInvalid, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return nil
}

func (store *Store) GetConfigurationPack(ctx context.Context, id, version string) (configpack.Pack, error) {
	if store == nil || store.db == nil {
		return configpack.Pack{}, coded(apperrors.StoreReadFailed, ErrConfigurationPackState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return store.getConfigurationPack(ctx, id, version)
}

func (store *Store) getConfigurationPack(ctx context.Context, id, version string) (configpack.Pack, error) {
	var state, createdAt string
	var digest, content []byte
	err := store.db.QueryRowContext(ctx, `SELECT state, content_digest, content_json, created_at
		FROM configuration_pack_versions WHERE configuration_pack_id = ? AND pack_version = ?`, id, version).Scan(&state, &digest, &content, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return configpack.Pack{}, configurationPackNotFound()
	}
	if err != nil {
		return configpack.Pack{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	files, err := configpack.UnmarshalFiles(content)
	if err != nil {
		return configpack.Pack{}, err
	}
	if !validPackDigest(digest, files) {
		return configpack.Pack{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
	}
	created, err := parseStoredTime(createdAt)
	if err != nil {
		return configpack.Pack{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	pack := configpack.Pack{ID: id, Version: version, State: configpack.State(state), Digest: hex.EncodeToString(digest), Files: files, CreatedAt: created}
	if err := pack.Validate(); err != nil {
		return configpack.Pack{}, err
	}
	return pack, nil
}

func (store *Store) ApproveConfigurationPack(ctx context.Context, id, version string) (configpack.Pack, error) {
	if store == nil || store.db == nil {
		return configpack.Pack{}, coded(apperrors.StoreWriteFailed, ErrConfigurationPackState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return configpack.Pack{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	pack, err := getConfigurationPackTx(ctx, tx, id, version)
	if err != nil {
		rollback()
		return configpack.Pack{}, err
	}
	if pack.State != configpack.StateDraft {
		rollback()
		return configpack.Pack{}, configurationPackInvalid()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_pack_versions SET state = 'approved'
		WHERE configuration_pack_id = ? AND pack_version = ? AND state = 'draft'`, id, version); err != nil {
		rollback()
		return configpack.Pack{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	digest, _ := hex.DecodeString(pack.Digest)
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_packs SET pack_version = ?, state = 'approved', content_digest = ? WHERE configuration_pack_id = ?`, version, digest, id); err != nil {
		rollback()
		return configpack.Pack{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return configpack.Pack{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	pack.State = configpack.StateApproved
	return pack, nil
}

func (store *Store) PromoteConfigurationPack(ctx context.Context, pack configpack.Pack, fromVersion string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrConfigurationPackState)
	}
	if pack.State != configpack.StateApproved {
		return configurationPackInvalid()
	}
	digest, content, err := packStorage(pack)
	if err != nil {
		return err
	}
	if fromVersion == pack.Version {
		return configurationPackInvalid()
	}
	now, err := store.configurationPackNow()
	if err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM configuration_pack_versions WHERE configuration_pack_id = ? AND pack_version = ?`, pack.ID, fromVersion).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		rollback()
		return configurationPackNotFound()
	} else if err != nil {
		rollback()
		return coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	} else if state != string(configpack.StateApproved) {
		rollback()
		return apperrors.New(apperrors.ConfigurationPackNotApproved, configpack.ErrNotApproved)
	}
	createdAt := formatStoredTime(now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO configuration_pack_versions (
		configuration_pack_version_id, configuration_pack_id, pack_version, state,
		content_digest, content_json, created_at
	) VALUES (?, ?, ?, 'approved', ?, ?, ?)`, configurationPackVersionID(pack), pack.ID, pack.Version, digest, content, createdAt); err != nil {
		rollback()
		return apperrors.New(apperrors.ConfigurationPackInvalid, errors.Join(configpack.ErrInvalid, err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE configuration_packs SET pack_version = ?, state = 'approved', content_digest = ? WHERE configuration_pack_id = ?`, pack.Version, digest, pack.ID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return nil
}

func (store *Store) AssignConfigurationPack(ctx context.Context, alias, id, version string) (configpack.Assignment, error) {
	if store == nil || store.db == nil {
		return configpack.Assignment{}, coded(apperrors.StoreWriteFailed, ErrConfigurationPackState)
	}
	target, err := store.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return configpack.Assignment{}, err
	}
	if target.Status != configpack.TargetStatusReady || target.HomeOwnership != configpack.TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" {
		return configpack.Assignment{}, apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, configpack.ErrAssignmentInvalid)
	}
	ctx = contextOrBackground(ctx)
	now, err := store.configurationPackNow()
	if err != nil {
		return configpack.Assignment{}, err
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return configpack.Assignment{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var state string
	var digest []byte
	if err := tx.QueryRowContext(ctx, `SELECT state, content_digest FROM configuration_pack_versions WHERE configuration_pack_id = ? AND pack_version = ?`, id, version).Scan(&state, &digest); errors.Is(err, sql.ErrNoRows) {
		rollback()
		return configpack.Assignment{}, configurationPackNotFound()
	} else if err != nil {
		rollback()
		return configpack.Assignment{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	} else if state != string(configpack.StateApproved) {
		rollback()
		return configpack.Assignment{}, apperrors.New(apperrors.ConfigurationPackNotApproved, configpack.ErrNotApproved)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO configuration_pack_assignments (
		profile_id, configuration_pack_id, pack_version, assigned_at
	) VALUES (?, ?, ?, ?)
	ON CONFLICT(profile_id) DO UPDATE SET configuration_pack_id = excluded.configuration_pack_id,
		pack_version = excluded.pack_version, assigned_at = excluded.assigned_at`, target.ID, id, version, formatStoredTime(now)); err != nil {
		rollback()
		return configpack.Assignment{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return configpack.Assignment{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return configpack.Assignment{ProfileID: target.ID, Alias: target.Alias, PackID: id, Version: version, Digest: hex.EncodeToString(digest)}, nil
}

func (store *Store) GetConfigurationPackAssignment(ctx context.Context, alias string) (configpack.Assignment, error) {
	if store == nil || store.db == nil {
		return configpack.Assignment{}, coded(apperrors.StoreReadFailed, ErrConfigurationPackState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var assignment configpack.Assignment
	var digest []byte
	err := store.db.QueryRowContext(ctx, `SELECT a.profile_id, a.alias, pa.configuration_pack_id, pa.pack_version, v.content_digest
		FROM cli_aliases a JOIN configuration_pack_assignments pa ON pa.profile_id = a.profile_id
		JOIN configuration_pack_versions v ON v.configuration_pack_id = pa.configuration_pack_id AND v.pack_version = pa.pack_version
		WHERE a.alias = ? COLLATE NOCASE AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = a.profile_id)`, alias).Scan(&assignment.ProfileID, &assignment.Alias, &assignment.PackID, &assignment.Version, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		if _, profileErr := store.profileIDForAlias(ctx, alias); profileErr != nil {
			return configpack.Assignment{}, profileErr
		}
		return configpack.Assignment{}, apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, configpack.ErrNoAssignment)
	}
	if err != nil {
		return configpack.Assignment{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	assignment.Digest = hex.EncodeToString(digest)
	return assignment, nil
}

func (store *Store) GetConfigurationOverrides(ctx context.Context, alias string) (map[string]string, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, ErrConfigurationPackState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	profileID, err := store.profileIDForAlias(ctx, alias)
	if err != nil {
		return nil, err
	}
	overrides := make(map[string]string)
	rows, err := store.db.QueryContext(ctx, `SELECT path, content FROM configuration_pack_overrides WHERE profile_id = ? ORDER BY path`, profileID)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	defer rows.Close()
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
		}
		overrides[path] = content
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return overrides, nil
}

func (store *Store) SetConfigurationOverride(ctx context.Context, alias, path, content string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrConfigurationPackState)
	}
	if err := configpack.ValidateFiles(map[string]string{path: content}); err != nil {
		return err
	}
	target, err := store.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return err
	}
	if target.Status != configpack.TargetStatusReady || target.HomeOwnership != configpack.TargetHomeOwnershipManaged {
		return apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, configpack.ErrAssignmentInvalid)
	}
	now, err := store.configurationPackNow()
	if err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err = store.db.ExecContext(ctx, `INSERT INTO configuration_pack_overrides (profile_id, path, content, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(profile_id, path) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`, target.ID, path, content, formatStoredTime(now))
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return nil
}

func (store *Store) GetConfigurationProfile(ctx context.Context, alias string) (configpack.ProfileTarget, error) {
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return store.configurationProfile(ctx, alias)
}

func (store *Store) WithStoppedConfigurationProfile(ctx context.Context, alias string, project func(configpack.ProfileTarget) error) error {
	if project == nil {
		return apperrors.New(apperrors.ConfigurationPackProjectionFailed, configpack.ErrProjectionFailed)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	target, err := store.configurationProfile(ctx, alias)
	if err != nil {
		return err
	}
	return project(target)
}

func (store *Store) configurationProfile(ctx context.Context, alias string) (configpack.ProfileTarget, error) {
	if err := profile.ValidateAlias(alias); err != nil {
		return configpack.ProfileTarget{}, err
	}
	var profileID string
	if err := store.db.QueryRowContext(ctx, `SELECT a.profile_id FROM cli_aliases a WHERE a.alias = ? COLLATE NOCASE
		AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = a.profile_id)`, alias).Scan(&profileID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return configpack.ProfileTarget{}, profile.ErrNotFound
		}
		return configpack.ProfileTarget{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	item, err := store.getIdentityProfile(ctx, profileID)
	if err != nil {
		return configpack.ProfileTarget{}, err
	}
	if err := store.populateIdentityHomePath(ctx, &item); err != nil {
		return configpack.ProfileTarget{}, err
	}
	var active int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_launches WHERE profile_id = ? AND state IN ('pending', 'running')`, item.ID).Scan(&active); err != nil {
		return configpack.ProfileTarget{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return configpack.ProfileTarget{
		ID: item.ID, Alias: item.Alias, Status: string(item.Status),
		HomeOwnership: string(item.IdentityHomeOwnership), IdentityHome: item.IdentityHomePath, ActiveLaunch: active != 0,
	}, nil
}

func (store *Store) profileIDForAlias(ctx context.Context, alias string) (string, error) {
	var profileID string
	err := store.db.QueryRowContext(ctx, `SELECT a.profile_id FROM cli_aliases a
		WHERE a.alias = ? COLLATE NOCASE AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = a.profile_id)`, alias).Scan(&profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", profile.ErrNotFound
	}
	if err != nil {
		return "", coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	return profileID, nil
}

func (store *Store) configurationPackNow() (time.Time, error) {
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return time.Time{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrConfigurationPackState, errors.New("configuration pack clock returned zero")))
	}
	return now, nil
}

func getConfigurationPackTx(ctx context.Context, tx *sql.Tx, id, version string) (configpack.Pack, error) {
	var state, createdAt string
	var digest, content []byte
	err := tx.QueryRowContext(ctx, `SELECT state, content_digest, content_json, created_at
		FROM configuration_pack_versions WHERE configuration_pack_id = ? AND pack_version = ?`, id, version).Scan(&state, &digest, &content, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return configpack.Pack{}, configurationPackNotFound()
	}
	if err != nil {
		return configpack.Pack{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	files, err := configpack.UnmarshalFiles(content)
	if err != nil {
		return configpack.Pack{}, err
	}
	if !validPackDigest(digest, files) {
		return configpack.Pack{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
	}
	created, err := parseStoredTime(createdAt)
	if err != nil {
		return configpack.Pack{}, coded(apperrors.StoreReadFailed, errors.Join(ErrConfigurationPackState, err))
	}
	pack := configpack.Pack{ID: id, Version: version, State: configpack.State(state), Digest: hex.EncodeToString(digest), Files: files, CreatedAt: created}
	if err := pack.Validate(); err != nil {
		return configpack.Pack{}, err
	}
	return pack, nil
}

func packStorage(pack configpack.Pack) ([]byte, []byte, error) {
	if err := pack.Validate(); err != nil {
		return nil, nil, err
	}
	content, err := configpack.MarshalFiles(pack.Files)
	if err != nil {
		return nil, nil, err
	}
	digest, err := hex.DecodeString(pack.Digest)
	if err != nil || len(digest) != 32 {
		return nil, nil, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
	}
	return digest, content, nil
}

func validPackDigest(digest []byte, files map[string]string) bool {
	return len(digest) == 32 && strings.EqualFold(hex.EncodeToString(digest), configpack.DigestFiles(files))
}

func configurationPackVersionID(pack configpack.Pack) string {
	digest := sha256.Sum256([]byte(pack.ID + "\x00" + pack.Version))
	return "configuration-pack-" + hex.EncodeToString(digest[:])
}

func configurationPackInvalid() error {
	return apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
}

func configurationPackNotFound() error {
	return apperrors.New(apperrors.ConfigurationPackNotFound, configpack.ErrNotFound)
}
