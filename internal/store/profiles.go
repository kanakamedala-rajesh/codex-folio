package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
)

var ErrProfileState = errors.New("profile state could not be persisted")

func (store *Store) FindPendingProfile(ctx context.Context, alias string) (profile.PendingProfile, error) {
	if store == nil || store.db == nil {
		return profile.PendingProfile{}, coded(apperrors.StoreReadFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	var profileID string
	err := store.db.QueryRowContext(ctx, `SELECT p.pending_profile_id
		FROM pending_profiles p
		JOIN cli_aliases a ON a.profile_id = p.pending_profile_id
		WHERE a.alias = ? COLLATE NOCASE`, alias).Scan(&profileID)
	store.operationMu.RUnlock()
	if errors.Is(err, sql.ErrNoRows) {
		return profile.PendingProfile{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.PendingProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	return store.GetPendingProfile(ctx, profileID)
}

func (store *Store) GetPendingProfile(ctx context.Context, profileID string) (profile.PendingProfile, error) {
	if store == nil || store.db == nil {
		return profile.PendingProfile{}, coded(apperrors.StoreReadFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()

	var pending profile.PendingProfile
	var status string
	var homeID sql.NullString
	var ownership sql.NullString
	var discovery, home, authentication, validation, selection int
	err := store.db.QueryRowContext(ctx, `SELECT p.pending_profile_id, a.alias, p.display_name,
		ip.status, p.identity_home_id, h.ownership,
		s.discovery_completed, s.home_completed, s.authentication_completed,
		s.validation_completed, s.selection_completed
		FROM pending_profiles p
		JOIN identity_profiles ip ON ip.profile_id = p.pending_profile_id
		JOIN cli_aliases a ON a.profile_id = p.pending_profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = p.identity_home_id
		LEFT JOIN profile_setup_stages s ON s.profile_id = p.pending_profile_id
		WHERE p.pending_profile_id = ?`, profileID).Scan(
		&pending.ID,
		&pending.Alias,
		&pending.DisplayName,
		&status,
		&homeID,
		&ownership,
		&discovery,
		&home,
		&authentication,
		&validation,
		&selection,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.PendingProfile{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.PendingProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	pending.Status = profile.Status(status)
	if homeID.Valid {
		pending.IdentityHomeID = homeID.String
	}
	if ownership.Valid {
		pending.IdentityHomeOwnership = profile.HomeOwnership(ownership.String)
	}
	pending.Stages = profile.SetupStages{
		Discovery:      discovery != 0,
		Home:           home != 0,
		Authentication: authentication != 0,
		Validation:     validation != 0,
		Selection:      selection != 0,
	}
	if pending.IdentityHomeID != "" {
		if pending.IdentityHomeOwnership != profile.HomeOwnershipManaged && pending.IdentityHomeOwnership != profile.HomeOwnershipReferenced {
			return profile.PendingProfile{}, apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
		}
		secureVault, vaultErr := store.requireVault()
		if vaultErr != nil {
			return profile.PendingProfile{}, vaultErr
		}
		var ciphertext []byte
		if err := store.db.QueryRowContext(ctx, `SELECT location_ciphertext FROM identity_homes WHERE identity_home_id = ?`, pending.IdentityHomeID).Scan(&ciphertext); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return profile.PendingProfile{}, apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
			}
			return profile.PendingProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
		}
		pending.IdentityHomePath, err = decryptField(ctx, secureVault, ciphertext, identityHomeAAD(pending.IdentityHomeID))
		if err != nil {
			return profile.PendingProfile{}, err
		}
	}
	return pending, nil
}

func (store *Store) GetProfile(ctx context.Context, alias string) (profile.IdentityProfile, error) {
	if store == nil || store.db == nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, ErrProfileState)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return profile.IdentityProfile{}, err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var profileID string
	err := store.db.QueryRowContext(ctx, `SELECT profile_id FROM cli_aliases WHERE alias = ? COLLATE NOCASE`, alias).Scan(&profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.IdentityProfile{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	item, err := store.getIdentityProfile(ctx, profileID)
	if err != nil {
		return profile.IdentityProfile{}, err
	}
	if err := store.populateIdentityHomePath(ctx, &item); err != nil {
		return profile.IdentityProfile{}, err
	}
	return item, nil
}

func (store *Store) CreatePendingProfile(ctx context.Context, pending profile.PendingProfile) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	if err := profile.ValidateAlias(pending.Alias); err != nil {
		return err
	}
	if strings.TrimSpace(pending.ID) == "" || strings.TrimSpace(pending.DisplayName) == "" {
		return apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, errors.New("profile clock returned zero")))
	}
	encodedNow := formatStoredTime(now)

	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM cli_aliases WHERE alias = ? COLLATE NOCASE LIMIT 1", pending.Alias).Scan(&exists); err == nil {
		rollback()
		return apperrors.New(apperrors.ProfileAliasTaken, profile.ErrAliasTaken)
	} else if !errors.Is(err, sql.ErrNoRows) {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO identity_profiles (profile_id, display_name, status, identity_home_id, created_at, updated_at)
			VALUES (?, ?, 'pending', NULL, ?, ?)`, []any{pending.ID, pending.DisplayName, encodedNow, encodedNow}},
		{`INSERT INTO cli_aliases (alias_id, profile_id, alias, created_at) VALUES (?, ?, ?, ?)`, []any{"alias-" + pending.ID, pending.ID, pending.Alias, encodedNow}},
		{`INSERT INTO pending_profiles (pending_profile_id, display_name, requested_alias, state, identity_home_id, created_at, updated_at)
			VALUES (?, ?, ?, 'pending', NULL, ?, ?)`, []any{pending.ID, pending.DisplayName, pending.Alias, encodedNow, encodedNow}},
		{`INSERT INTO profile_setup_stages (profile_id, discovery_completed, home_completed, authentication_completed, validation_completed, selection_completed, updated_at)
			VALUES (?, 0, 0, 0, 0, 0, ?)`, []any{pending.ID, encodedNow}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) SetManagedHome(ctx context.Context, profileID, homeID, homePath string) error {
	return store.setHome(ctx, profileID, homeID, profile.HomeOwnershipManaged, homePath)
}

func (store *Store) SetReferencedHome(ctx context.Context, profileID, homeID, homePath string) error {
	return store.setHome(ctx, profileID, homeID, profile.HomeOwnershipReferenced, homePath)
}

func (store *Store) SaveDocumentedMetadata(ctx context.Context, profileID string, metadata profile.DocumentedMetadata) (bool, error) {
	if store == nil || store.db == nil {
		return false, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	loginIdentity := strings.TrimSpace(metadata.LoginIdentity)
	workspace := strings.TrimSpace(metadata.Workspace)
	if loginIdentity == "" && workspace == "" {
		return false, nil
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return false, err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT identity_home_id, profile_id, documented_login_identity_ciphertext, documented_workspace_ciphertext FROM identity_homes`)
	if err != nil {
		return false, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	defer rows.Close()
	duplicate := false
	for rows.Next() {
		var homeID, existingProfile string
		var loginCiphertext, workspaceCiphertext []byte
		if err := rows.Scan(&homeID, &existingProfile, &loginCiphertext, &workspaceCiphertext); err != nil {
			return false, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
		}
		if existingProfile == profileID {
			continue
		}
		var existingLogin, existingWorkspace string
		if len(loginCiphertext) > 0 {
			existingLogin, err = decryptField(ctx, secureVault, loginCiphertext, documentedMetadataAAD(existingProfile, "login-identity"))
			if err != nil {
				return false, err
			}
		}
		if len(workspaceCiphertext) > 0 {
			existingWorkspace, err = decryptField(ctx, secureVault, workspaceCiphertext, documentedMetadataAAD(existingProfile, "workspace"))
			if err != nil {
				return false, err
			}
		}
		if (loginIdentity != "" && loginIdentity == existingLogin) || (workspace != "" && workspace == existingWorkspace) {
			duplicate = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	loginCiphertext, err := encryptField(ctx, secureVault, []byte(loginIdentity), documentedMetadataAAD(profileID, "login-identity"))
	if err != nil {
		return false, err
	}
	workspaceCiphertext, err := encryptField(ctx, secureVault, []byte(workspace), documentedMetadataAAD(profileID, "workspace"))
	if err != nil {
		return false, err
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE identity_homes SET documented_login_identity_ciphertext = ?, documented_workspace_ciphertext = ? WHERE profile_id = ?`, loginCiphertext, workspaceCiphertext, profileID); err != nil {
		return false, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return duplicate, nil
}

func (store *Store) setHome(ctx context.Context, profileID, homeID string, ownership profile.HomeOwnership, homePath string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	if (ownership != profile.HomeOwnershipManaged && ownership != profile.HomeOwnershipReferenced) || strings.TrimSpace(profileID) == "" || strings.TrimSpace(homeID) == "" || !filepath.IsAbs(homePath) {
		return apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	homePath = filepath.Clean(homePath)
	ciphertext, err := encryptField(ctx, secureVault, []byte(homePath), identityHomeAAD(homeID))
	if err != nil {
		return err
	}
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, errors.New("profile clock returned zero")))
	}
	encodedNow := formatStoredTime(now)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	identityResult, err := tx.ExecContext(ctx, `UPDATE identity_profiles SET identity_home_id = ?, updated_at = ? WHERE profile_id = ?`, homeID, encodedNow, profileID)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if affected, affectedErr := identityResult.RowsAffected(); affectedErr != nil || affected == 0 {
		rollback()
		if affectedErr != nil {
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, affectedErr))
		}
		return profile.ErrNotFound
	}
	pendingResult, err := tx.ExecContext(ctx, `UPDATE pending_profiles SET identity_home_id = ?, updated_at = ? WHERE pending_profile_id = ?`, homeID, encodedNow, profileID)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if affected, affectedErr := pendingResult.RowsAffected(); affectedErr != nil || affected == 0 {
		rollback()
		if affectedErr != nil {
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, affectedErr))
		}
		return profile.ErrNotFound
	}
	var existingProfileID, existingOwnership string
	if err := tx.QueryRowContext(ctx, `SELECT profile_id, ownership FROM identity_homes WHERE identity_home_id = ?`, homeID).Scan(&existingProfileID, &existingOwnership); err == nil && (existingProfileID != profileID || existingOwnership != string(ownership)) {
		rollback()
		return apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		rollback()
		return coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_homes (identity_home_id, profile_id, ownership, location_ciphertext, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(identity_home_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			ownership = excluded.ownership,
			location_ciphertext = excluded.location_ciphertext,
			updated_at = excluded.updated_at`, homeID, profileID, string(ownership), ciphertext, encodedNow, encodedNow); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) SaveSetupStage(ctx context.Context, profileID string, stage profile.SetupStage) error {
	column, ok := setupStageColumn(stage)
	if !ok {
		return apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile setup stage is invalid"))
	}
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	encodedNow := formatStoredTime(now)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	query := fmt.Sprintf("UPDATE profile_setup_stages SET %s = 1, updated_at = ? WHERE profile_id = ?", column)
	result, err := tx.ExecContext(ctx, query, encodedNow, profileID)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected == 0 {
		rollback()
		if affectedErr != nil {
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, affectedErr))
		}
		return profile.ErrNotFound
	}
	pendingResult, err := tx.ExecContext(ctx, `UPDATE pending_profiles SET state = 'pending', updated_at = ? WHERE pending_profile_id = ?`, encodedNow, profileID)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if affected, affectedErr := pendingResult.RowsAffected(); affectedErr != nil || affected == 0 {
		rollback()
		if affectedErr != nil {
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, affectedErr))
		}
		return profile.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) ResetAuthentication(ctx context.Context, profileID string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	encodedNow := formatStoredTime(now)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	if _, err := tx.ExecContext(ctx, `UPDATE profile_setup_stages SET authentication_completed = 0, validation_completed = 0, selection_completed = 0, updated_at = ? WHERE profile_id = ?`, encodedNow, profileID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE identity_profiles SET status = 'pending', updated_at = ? WHERE profile_id = ?`, encodedNow, profileID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pending_profiles SET state = 'needs_attention', updated_at = ? WHERE pending_profile_id = ?`, encodedNow, profileID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) SetAuthenticationState(ctx context.Context, profileID string, status profile.Status, method profile.AuthMethod) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	if strings.TrimSpace(profileID) == "" || !validProfileStatus(status) || !validStoredAuthMethod(method) {
		return apperrors.New(apperrors.ProfileSetupInvalid, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, errors.New("profile clock returned zero")))
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	query := `UPDATE identity_profiles SET status = ?, updated_at = ? WHERE profile_id = ?`
	args := []any{string(status), formatStoredTime(now), profileID}
	if method != "" {
		query = `UPDATE identity_profiles SET status = ?, authentication_method = ?, updated_at = ? WHERE profile_id = ?`
		args = []any{string(status), string(method), formatStoredTime(now), profileID}
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected == 0 {
		rollback()
		if affectedErr != nil {
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, affectedErr))
		}
		return profile.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) PromotePendingProfile(ctx context.Context, profileID string) (profile.IdentityProfile, error) {
	if store == nil || store.db == nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	now := store.clock.Now().UTC()
	encodedNow := formatStoredTime(now)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var authentication, validation int
	if err := tx.QueryRowContext(ctx, `SELECT authentication_completed, validation_completed FROM profile_setup_stages WHERE profile_id = ?`, profileID).Scan(&authentication, &validation); err != nil {
		rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return profile.IdentityProfile{}, profile.ErrNotFound
		}
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if authentication == 0 || validation == 0 {
		rollback()
		return profile.IdentityProfile{}, apperrors.New(apperrors.ProfileValidationFailed, profile.ErrValidationFailed)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE identity_profiles SET status = 'ready', updated_at = ? WHERE profile_id = ?`, encodedNow, profileID); err != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return store.getIdentityProfile(ctx, profileID)
}

func (store *Store) CompleteInitialSelection(ctx context.Context, profileID string) (profile.IdentityProfile, error) {
	if store == nil || store.db == nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	if store.profileHooks.BeforeSelection != nil {
		if err := store.profileHooks.BeforeSelection(profileID); err != nil {
			return profile.IdentityProfile{}, err
		}
	}
	now := store.clock.Now().UTC()
	encodedNow := formatStoredTime(now)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM identity_profiles WHERE profile_id = ?`, profileID).Scan(&status); err != nil {
		rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return profile.IdentityProfile{}, profile.ErrNotFound
		}
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if status != string(profile.StatusReady) {
		rollback()
		return profile.IdentityProfile{}, apperrors.New(apperrors.ProfileValidationFailed, profile.ErrValidationFailed)
	}
	var selected sql.NullString
	selectErr := tx.QueryRowContext(ctx, `SELECT profile_id FROM selected_profile WHERE selection_id = 1`).Scan(&selected)
	if errors.Is(selectErr, sql.ErrNoRows) || !selected.Valid || selected.String == "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO selected_profile (selection_id, profile_id, updated_at) VALUES (1, ?, ?)`, profileID, encodedNow); err != nil {
			rollback()
			return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
		selected.String = profileID
	} else if selectErr != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, selectErr))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profile_setup_stages SET selection_completed = 1, updated_at = ? WHERE profile_id = ?`, encodedNow, profileID); err != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pending_profiles WHERE pending_profile_id = ?`, profileID); err != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return profile.IdentityProfile{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	result, err := store.getIdentityProfile(ctx, profileID)
	if err != nil {
		return profile.IdentityProfile{}, err
	}
	result.Selected = selected.String == profileID
	return result, nil
}

func (store *Store) ListEligibleProfiles(ctx context.Context) ([]profile.IdentityProfile, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT ip.profile_id, a.alias, ip.display_name, ip.status,
		COALESCE(ip.identity_home_id, ''), COALESCE(h.ownership, ''), ip.authentication_method, ip.created_at, ip.updated_at,
		CASE WHEN s.profile_id = ip.profile_id THEN 1 ELSE 0 END
		FROM identity_profiles ip
		JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		LEFT JOIN selected_profile s ON s.profile_id = ip.profile_id
		WHERE ip.status = 'ready'
		ORDER BY CASE WHEN s.profile_id = ip.profile_id THEN 0 ELSE 1 END, a.alias COLLATE NOCASE`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	defer func() { _ = rows.Close() }()
	profiles := make([]profile.IdentityProfile, 0)
	for rows.Next() {
		var item profile.IdentityProfile
		var status, ownership, authenticationMethod, createdAt, updatedAt string
		var selected int
		if err := rows.Scan(&item.ID, &item.Alias, &item.DisplayName, &status, &item.IdentityHomeID, &ownership, &authenticationMethod, &createdAt, &updatedAt, &selected); err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
		}
		item.Status = profile.Status(status)
		item.IdentityHomeOwnership = profile.HomeOwnership(ownership)
		item.AuthenticationMethod = storedAuthMethod(authenticationMethod)
		item.Selected = selected != 0
		item.CreatedAt, err = parseStoredTime(createdAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
		}
		item.UpdatedAt, err = parseStoredTime(updatedAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
		}
		profiles = append(profiles, item)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	return profiles, nil
}

func (store *Store) SelectProfile(ctx context.Context, alias string) (profile.SelectionResult, error) {
	if store == nil || store.db == nil {
		return profile.SelectionResult{}, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return profile.SelectionResult{}, err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return profile.SelectionResult{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var profileID, status string
	if err := tx.QueryRowContext(ctx, `SELECT ip.profile_id, ip.status FROM identity_profiles ip JOIN cli_aliases a ON a.profile_id = ip.profile_id WHERE a.alias = ? COLLATE NOCASE`, alias).Scan(&profileID, &status); err != nil {
		rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return profile.SelectionResult{}, apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
		}
		return profile.SelectionResult{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if status != string(profile.StatusReady) {
		rollback()
		return profile.SelectionResult{}, apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO selected_profile (selection_id, profile_id, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(selection_id) DO UPDATE SET profile_id = excluded.profile_id, updated_at = excluded.updated_at`, profileID, formatStoredTime(store.clock.Now().UTC())); err != nil {
		rollback()
		return profile.SelectionResult{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	var differingRunning int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_launches WHERE state = 'running' AND profile_id <> ?`, profileID).Scan(&differingRunning); err != nil {
		rollback()
		return profile.SelectionResult{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return profile.SelectionResult{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	selected, err := store.getIdentityProfile(ctx, profileID)
	if err != nil {
		return profile.SelectionResult{}, err
	}
	result := profile.SelectionResult{Profile: selected}
	if differingRunning > 0 {
		result.Warnings = []string{profile.RunningLaunchSelectionWarning}
	}
	return result, nil
}

func (store *Store) getIdentityProfile(ctx context.Context, profileID string) (profile.IdentityProfile, error) {
	var result profile.IdentityProfile
	var status, alias, homeID, ownership string
	var authenticationMethod string
	var selected int
	var createdAt, updatedAt string
	err := store.db.QueryRowContext(ctx, `SELECT ip.profile_id, a.alias, ip.display_name, ip.status,
		COALESCE(ip.identity_home_id, ''), COALESCE(h.ownership, ''), ip.authentication_method,
		ip.created_at, ip.updated_at,
		CASE WHEN s.profile_id = ip.profile_id THEN 1 ELSE 0 END
		FROM identity_profiles ip
		LEFT JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		LEFT JOIN selected_profile s ON s.profile_id = ip.profile_id
		WHERE ip.profile_id = ?`, profileID).Scan(
		&result.ID,
		&alias,
		&result.DisplayName,
		&status,
		&homeID,
		&ownership,
		&authenticationMethod,
		&createdAt,
		&updatedAt,
		&selected,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.IdentityProfile{}, profile.ErrNotFound
	}
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	result.Alias = alias
	result.Status = profile.Status(status)
	result.IdentityHomeID = homeID
	result.IdentityHomeOwnership = profile.HomeOwnership(ownership)
	result.AuthenticationMethod = storedAuthMethod(authenticationMethod)
	result.Selected = selected != 0
	result.CreatedAt, err = parseStoredTime(createdAt)
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	result.UpdatedAt, err = parseStoredTime(updatedAt)
	if err != nil {
		return profile.IdentityProfile{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	return result, nil
}

func (store *Store) populateIdentityHomePath(ctx context.Context, item *profile.IdentityProfile) error {
	if item == nil || item.IdentityHomeID == "" {
		return nil
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return err
	}
	var ciphertext []byte
	if err := store.db.QueryRowContext(ctx, `SELECT location_ciphertext FROM identity_homes WHERE identity_home_id = ?`, item.IdentityHomeID).Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
		}
		return coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	path, err := decryptField(ctx, secureVault, ciphertext, identityHomeAAD(item.IdentityHomeID))
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	}
	item.IdentityHomePath = path
	return nil
}

func setupStageColumn(stage profile.SetupStage) (string, bool) {
	switch stage {
	case profile.StageDiscovery:
		return "discovery_completed", true
	case profile.StageHome:
		return "home_completed", true
	case profile.StageAuthentication:
		return "authentication_completed", true
	case profile.StageValidation:
		return "validation_completed", true
	case profile.StageSelection:
		return "selection_completed", true
	default:
		return "", false
	}
}

func validProfileStatus(status profile.Status) bool {
	return status == profile.StatusPending || status == profile.StatusReady || status == profile.StatusNeedsReauthentication || status == profile.StatusUnavailable
}

func validStoredAuthMethod(method profile.AuthMethod) bool {
	return method == "" || method == profile.AuthMethodAutomatic || method == profile.AuthMethodBrowser || method == profile.AuthMethodDeviceCode
}

func storedAuthMethod(method string) profile.AuthMethod {
	if !validStoredAuthMethod(profile.AuthMethod(method)) || method == "" {
		return profile.AuthMethodAutomatic
	}
	return profile.AuthMethod(method)
}

func identityHomeAAD(homeID string) []byte {
	return []byte("codex-folio/identity-homes/" + homeID + "/location")
}

func documentedMetadataAAD(homeID, field string) []byte {
	return []byte("codex-folio/identity-homes/" + homeID + "/" + field)
}

var _ profile.AuthenticationRepository = (*Store)(nil)
