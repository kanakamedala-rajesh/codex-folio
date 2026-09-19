package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (store *Store) CollectionSettings(ctx context.Context) (usage.CollectionSettings, error) {
	if store == nil || store.db == nil {
		return usage.CollectionSettings{}, coded(apperrors.StoreReadFailed, usage.ErrPersistenceFailed)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	settings := usage.DefaultCollectionSettings()
	var activeSeconds, idleSeconds int64
	err := store.db.QueryRowContext(ctx, `SELECT collection_active_interval_seconds, collection_idle_interval_seconds FROM settings WHERE settings_id = 1`).Scan(&activeSeconds, &idleSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return usage.CollectionSettings{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	settings.ActiveInterval = time.Duration(activeSeconds) * time.Second
	settings.IdleInterval = time.Duration(idleSeconds) * time.Second
	if err := usage.ValidateCollectionSettings(settings); err != nil {
		return usage.CollectionSettings{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return settings, nil
}

func (store *Store) SetCollectionSettings(ctx context.Context, settings usage.CollectionSettings) (usage.CollectionSettings, error) {
	if settings.ProviderMinimum == 0 {
		settings.ProviderMinimum = usage.ProviderSafeMinimum
	}
	if store == nil || store.db == nil || usage.ValidateCollectionSettings(settings) != nil {
		return usage.CollectionSettings{}, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrScheduleInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(ctx, `INSERT INTO settings (settings_id, collection_active_interval_seconds, collection_idle_interval_seconds, updated_at)
		VALUES (1, ?, ?, ?) ON CONFLICT(settings_id) DO UPDATE SET collection_active_interval_seconds = excluded.collection_active_interval_seconds,
		collection_idle_interval_seconds = excluded.collection_idle_interval_seconds, updated_at = excluded.updated_at`,
		int64(settings.ActiveInterval/time.Second), int64(settings.IdleInterval/time.Second), formatStoredTime(store.clock.Now()))
	if err != nil {
		return usage.CollectionSettings{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return settings, nil
}

func (store *Store) SaveCollectionScheduleState(ctx context.Context, profileID string, state usage.ScheduleState) error {
	if store == nil || store.db == nil || profileID == "" || state.NextAttemptAt.IsZero() || state.ConsecutiveFailures < 0 || (state.LastOutcome != "" && state.LastOutcome != usage.ScheduleOutcomeSucceeded && state.LastOutcome != usage.ScheduleOutcomeFailed) {
		return apperrors.New(apperrors.UsageRequestInvalid, usage.ErrScheduleInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(ctx, `INSERT INTO collection_schedule_state (profile_id, last_attempt_at, next_attempt_at, consecutive_failures, last_outcome, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(profile_id) DO UPDATE SET last_attempt_at = excluded.last_attempt_at, next_attempt_at = excluded.next_attempt_at,
		consecutive_failures = excluded.consecutive_failures, last_outcome = excluded.last_outcome, updated_at = excluded.updated_at`,
		profileID, nullableScheduleTime(state.LastAttemptAt), formatStoredTime(state.NextAttemptAt), state.ConsecutiveFailures, state.LastOutcome, formatStoredTime(store.clock.Now()))
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return nil
}

func (store *Store) CollectionScheduleTargets(ctx context.Context) ([]usage.ScheduleTarget, error) {
	profiles, err := store.ListUsageProfiles(ctx)
	if err != nil {
		return nil, err
	}
	ctx = contextOrBackground(ctx)
	targets := make([]usage.ScheduleTarget, 0, len(profiles))
	for _, target := range profiles {
		if !target.Eligible {
			continue
		}
		item := usage.ScheduleTarget{Profile: target}
		if latest, latestErr := store.LatestUsageSnapshot(ctx, target); latestErr == nil {
			item.Latest = latest
		} else if !errors.Is(latestErr, usage.ErrProfileUnavailable) {
			return nil, latestErr
		}
		store.operationMu.RLock()
		var active int
		var lastAttempt, nextAttempt sql.NullString
		err = store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM managed_launches WHERE profile_id = ? AND state = 'running'),
			last_attempt_at, next_attempt_at, COALESCE(consecutive_failures, 0), COALESCE(last_outcome, '')
			FROM (SELECT 1) LEFT JOIN collection_schedule_state ON profile_id = ?`, target.ID, target.ID).
			Scan(&active, &lastAttempt, &nextAttempt, &item.State.ConsecutiveFailures, &item.State.LastOutcome)
		store.operationMu.RUnlock()
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		item.Active = active == 1
		if lastAttempt.Valid {
			item.State.LastAttemptAt, err = parseStoredTime(lastAttempt.String)
		}
		if err == nil && nextAttempt.Valid {
			item.State.NextAttemptAt, err = parseStoredTime(nextAttempt.String)
		}
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		targets = append(targets, item)
	}
	return targets, nil
}

func nullableScheduleTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatStoredTime(value)
}
