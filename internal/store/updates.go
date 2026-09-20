package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/updates"
)

var ErrUpdateState = errors.New("update state could not be stored")

func (store *Store) UpdateSettings(ctx context.Context) (updates.Settings, error) {
	if store == nil || store.db == nil {
		return updates.Settings{}, coded(apperrors.StoreReadFailed, ErrUpdateState)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var enabled int
	err := store.db.QueryRowContext(contextOrBackground(ctx), `SELECT automatic_update_checks_enabled FROM settings WHERE settings_id = 1`).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return updates.Settings{}, nil
	}
	if err != nil {
		return updates.Settings{}, coded(apperrors.StoreReadFailed, errors.Join(ErrUpdateState, err))
	}
	return updates.Settings{AutomaticChecks: enabled == 1}, nil
}

func (store *Store) SetUpdateSettings(ctx context.Context, settings updates.Settings) (updates.Settings, error) {
	if store == nil || store.db == nil {
		return updates.Settings{}, coded(apperrors.StoreWriteFailed, ErrUpdateState)
	}
	enabled := 0
	if settings.AutomaticChecks {
		enabled = 1
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(contextOrBackground(ctx), `INSERT INTO settings (settings_id, automatic_update_checks_enabled, updated_at)
		VALUES (1, ?, ?) ON CONFLICT(settings_id) DO UPDATE SET
		automatic_update_checks_enabled = excluded.automatic_update_checks_enabled,
		updated_at = excluded.updated_at`, enabled, formatStoredTime(store.clock.Now().UTC()))
	if err != nil {
		return updates.Settings{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrUpdateState, err))
	}
	return settings, nil
}

func (store *Store) UpdateCheckState(ctx context.Context) (updates.CheckState, error) {
	if store == nil || store.db == nil {
		return updates.CheckState{}, coded(apperrors.StoreReadFailed, ErrUpdateState)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var state updates.CheckState
	var checkedAt, nextCheckAt sql.NullString
	err := store.db.QueryRowContext(contextOrBackground(ctx), `SELECT status, current_version, available_version,
		release_notes, download_url, installer_guidance, checked_at, next_check_at, error_code
		FROM update_check_state WHERE update_check_state_id = 1`).Scan(
		&state.Status, &state.CurrentVersion, &state.AvailableVersion, &state.ReleaseNotes,
		&state.DownloadURL, &state.InstallerGuidance, &checkedAt, &nextCheckAt, &state.ErrorCode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return updates.CheckState{Status: updates.StatusNeverChecked}, nil
	}
	if err != nil {
		return updates.CheckState{}, coded(apperrors.StoreReadFailed, errors.Join(ErrUpdateState, err))
	}
	if checkedAt.Valid {
		state.CheckedAt, err = time.Parse(time.RFC3339Nano, checkedAt.String)
		if err != nil {
			return updates.CheckState{}, coded(apperrors.StoreReadFailed, errors.Join(ErrUpdateState, err))
		}
	}
	if nextCheckAt.Valid {
		state.NextCheckAt, err = time.Parse(time.RFC3339Nano, nextCheckAt.String)
		if err != nil {
			return updates.CheckState{}, coded(apperrors.StoreReadFailed, errors.Join(ErrUpdateState, err))
		}
	}
	if err := updates.ValidateCheckState(state); err != nil {
		return updates.CheckState{}, coded(apperrors.StoreReadFailed, errors.Join(ErrUpdateState, err))
	}
	return state, nil
}

func (store *Store) SetUpdateCheckState(ctx context.Context, state updates.CheckState) (updates.CheckState, error) {
	if store == nil || store.db == nil {
		return updates.CheckState{}, coded(apperrors.StoreWriteFailed, ErrUpdateState)
	}
	if err := updates.ValidateCheckState(state); err != nil || state.Status == updates.StatusDisabled {
		return updates.CheckState{}, updates.ErrInvalid
	}
	var checkedAt, nextCheckAt any
	if !state.CheckedAt.IsZero() {
		checkedAt = formatStoredTime(state.CheckedAt.UTC())
	}
	if !state.NextCheckAt.IsZero() {
		nextCheckAt = formatStoredTime(state.NextCheckAt.UTC())
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(contextOrBackground(ctx), `INSERT INTO update_check_state (
		update_check_state_id, status, current_version, available_version, release_notes,
		download_url, installer_guidance, checked_at, next_check_at, error_code
	) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(update_check_state_id) DO UPDATE SET
		status = excluded.status,
		current_version = excluded.current_version,
		available_version = excluded.available_version,
		release_notes = excluded.release_notes,
		download_url = excluded.download_url,
		installer_guidance = excluded.installer_guidance,
		checked_at = excluded.checked_at,
		next_check_at = excluded.next_check_at,
		error_code = excluded.error_code`,
		state.Status, state.CurrentVersion, state.AvailableVersion, state.ReleaseNotes,
		state.DownloadURL, state.InstallerGuidance, checkedAt, nextCheckAt, state.ErrorCode,
	)
	if err != nil {
		return updates.CheckState{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrUpdateState, err))
	}
	return state, nil
}
