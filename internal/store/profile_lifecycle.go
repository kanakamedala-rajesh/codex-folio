package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
)

const profileQuarantinePeriod = 7 * 24 * time.Hour

func (store *Store) BeginProfileRemoval(ctx context.Context, alias, replacement string) (profile.RemovalRecord, error) {
	if store == nil || store.db == nil {
		return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return profile.RemovalRecord{}, err
	}
	ctx = contextOrBackground(ctx)
	if existing, err := store.GetQuarantinedProfile(ctx, alias); err == nil {
		return existing, nil
	} else if !errors.Is(err, profile.ErrNotFound) {
		return profile.RemovalRecord{}, err
	}
	item, err := store.GetProfile(ctx, alias)
	if err != nil {
		return profile.RemovalRecord{}, err
	}
	now := store.clock.Now().UTC()
	if now.IsZero() {
		return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, ErrProfileState)
	}

	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var running int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_launches WHERE profile_id = ? AND state IN ('pending', 'running')`, item.ID).Scan(&running); err != nil {
		rollback()
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if running != 0 {
		rollback()
		return profile.RemovalRecord{}, apperrors.New(apperrors.ProfileRemovalBlocked, profile.ErrRunningLaunch)
	}
	var selectedID sql.NullString
	selectedErr := tx.QueryRowContext(ctx, `SELECT profile_id FROM selected_profile WHERE selection_id = 1`).Scan(&selectedID)
	if selectedErr != nil && !errors.Is(selectedErr, sql.ErrNoRows) {
		rollback()
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, selectedErr))
	}
	wasSelected := selectedID.Valid && selectedID.String == item.ID
	if wasSelected {
		if err := profile.ValidateAlias(replacement); err != nil || equalFold(alias, replacement) {
			rollback()
			return profile.RemovalRecord{}, apperrors.New(apperrors.ProfileReplacementRequired, profile.ErrReplacementRequired)
		}
		var replacementID, status string
		if err := tx.QueryRowContext(ctx, `SELECT ip.profile_id, ip.status FROM identity_profiles ip
			JOIN cli_aliases a ON a.profile_id = ip.profile_id
			WHERE a.alias = ? COLLATE NOCASE AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = ip.profile_id)`, replacement).Scan(&replacementID, &status); err != nil || status != string(profile.StatusReady) {
			rollback()
			return profile.RemovalRecord{}, apperrors.New(apperrors.ProfileReplacementRequired, profile.ErrReplacementRequired)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE selected_profile SET profile_id = ?, updated_at = ? WHERE selection_id = 1`, replacementID, formatStoredTime(now)); err != nil {
			rollback()
			return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
	}

	record := profile.RemovalRecord{Profile: item}
	if item.IdentityHomeOwnership == profile.HomeOwnershipReferenced {
		if err := deleteProfileRows(ctx, tx, item.ID); err != nil {
			rollback()
			return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
		record.Action = profile.RemovalDeregistered
	} else if item.IdentityHomeOwnership == profile.HomeOwnershipManaged {
		record.QuarantinedAt = now
		record.PurgeAfter = now.Add(profileQuarantinePeriod)
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_quarantine (profile_id, state, was_selected, quarantined_at, purge_after, updated_at)
			VALUES (?, 'prepared', ?, ?, ?, ?)`, item.ID, boolInt(wasSelected), formatStoredTime(now), formatStoredTime(record.PurgeAfter), formatStoredTime(now)); err != nil {
			rollback()
			return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
		record.Action = profile.RemovalQuarantined
		record.State = profile.QuarantinePrepared
	} else {
		rollback()
		return profile.RemovalRecord{}, apperrors.New(apperrors.ProfileHomeInvalid, profile.ErrHomeInvalid)
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return profile.RemovalRecord{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return record, nil
}

func (store *Store) GetQuarantinedProfile(ctx context.Context, alias string) (profile.RemovalRecord, error) {
	if store == nil || store.db == nil {
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, ErrProfileState)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return profile.RemovalRecord{}, err
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var profileID, state, quarantinedAt, purgeAfter string
	if err := store.db.QueryRowContext(ctx, `SELECT q.profile_id, q.state, q.quarantined_at, q.purge_after
		FROM profile_quarantine q JOIN cli_aliases a ON a.profile_id = q.profile_id WHERE a.alias = ? COLLATE NOCASE`, alias).Scan(&profileID, &state, &quarantinedAt, &purgeAfter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return profile.RemovalRecord{}, profile.ErrNotFound
		}
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	item, err := store.getIdentityProfile(ctx, profileID)
	if err == nil {
		err = store.populateIdentityHomePath(ctx, &item)
	}
	if err != nil {
		return profile.RemovalRecord{}, err
	}
	quarantinedTime, err := parseStoredTime(quarantinedAt)
	if err != nil {
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	purgeTime, err := parseStoredTime(purgeAfter)
	if err != nil {
		return profile.RemovalRecord{}, coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	return profile.RemovalRecord{Profile: item, Action: profile.RemovalQuarantined, State: profile.QuarantineState(state), QuarantinedAt: quarantinedTime, PurgeAfter: purgeTime}, nil
}

func (store *Store) CompleteProfileQuarantine(ctx context.Context, profileID string) error {
	return store.updateQuarantineState(ctx, profileID, profile.QuarantinePrepared, profile.QuarantineReady)
}

func (store *Store) CancelProfileRemoval(ctx context.Context, profileID string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var wasSelected int
	if err := tx.QueryRowContext(ctx, `SELECT was_selected FROM profile_quarantine WHERE profile_id = ? AND state = 'prepared'`, profileID).Scan(&wasSelected); err != nil {
		rollback()
		return apperrors.New(apperrors.ProfileQuarantineInvalid, profile.ErrQuarantineInvalid)
	}
	if wasSelected != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE selected_profile SET profile_id = ?, updated_at = ? WHERE selection_id = 1`, profileID, formatStoredTime(store.clock.Now().UTC())); err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile_quarantine WHERE profile_id = ?`, profileID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) RestoreProfile(ctx context.Context, profileID string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	var purgeAfter string
	if err := store.db.QueryRowContext(ctx, `SELECT purge_after FROM profile_quarantine WHERE profile_id = ? AND state = 'quarantined'`, profileID).Scan(&purgeAfter); err != nil {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, profile.ErrQuarantineInvalid)
	}
	expires, err := parseStoredTime(purgeAfter)
	if err != nil {
		return coded(apperrors.StoreReadFailed, errors.Join(ErrProfileState, err))
	}
	if !store.clock.Now().UTC().Before(expires) {
		return apperrors.New(apperrors.ProfileQuarantineExpired, profile.ErrQuarantineExpired)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM profile_quarantine WHERE profile_id = ?`, profileID); err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) PurgeProfile(ctx context.Context, profileID string) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, ErrProfileState)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	rollback := func() { _ = tx.Rollback() }
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM profile_quarantine WHERE profile_id = ? AND state = 'quarantined'`, profileID).Scan(&exists); err != nil {
		rollback()
		return apperrors.New(apperrors.ProfileQuarantineInvalid, profile.ErrQuarantineInvalid)
	}
	if err := deleteProfileRows(ctx, tx, profileID); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	return nil
}

func (store *Store) updateQuarantineState(ctx context.Context, profileID string, from, to profile.QuarantineState) error {
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE profile_quarantine SET state = ?, updated_at = ? WHERE profile_id = ? AND state = ?`, to, formatStoredTime(store.clock.Now().UTC()), profileID, from)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrProfileState, err))
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return apperrors.New(apperrors.ProfileQuarantineInvalid, profile.ErrQuarantineInvalid)
	}
	return nil
}

func deleteProfileRows(ctx context.Context, tx *sql.Tx, profileID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE identity_profiles SET display_name = '', email = '', workspace = '', status = 'unavailable', identity_home_id = NULL, authentication_method = 'auto' WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	statements := []string{
		`DELETE FROM correlation_evidence WHERE managed_launch_id IN (SELECT managed_launch_id FROM managed_launches WHERE profile_id = ?)`,
		`DELETE FROM configuration_pack_overrides WHERE profile_id = ?`,
		`DELETE FROM configuration_pack_assignments WHERE profile_id = ?`,
		`UPDATE observed_sessions SET profile_id = NULL WHERE profile_id = ?`,
		`DELETE FROM managed_launches WHERE profile_id = ?`,
		`DELETE FROM selected_profile WHERE profile_id = ?`,
		`DELETE FROM profile_quarantine WHERE profile_id = ?`,
		`DELETE FROM profile_setup_stages WHERE profile_id = ?`,
		`DELETE FROM pending_profiles WHERE pending_profile_id = ?`,
		`DELETE FROM identity_homes WHERE profile_id = ?`,
		`DELETE FROM cli_aliases WHERE profile_id = ?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, profileID); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func equalFold(left, right string) bool { return strings.EqualFold(left, right) }
