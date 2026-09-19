package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

var ErrDiagnosticAggregate = errors.New("diagnostic aggregate could not be stored")

// RecordDiagnostic stores only the bounded aggregate projection of an event.
// Context, causes, messages, and all authorization or user data are discarded
// before SQLite is touched.
func (store *Store) RecordDiagnostic(ctx context.Context, event diagnostics.Event) error {
	if store == nil || store.db == nil {
		return apperrors.New(apperrors.StoreDiagnosticWriteFailed, ErrDiagnosticAggregate)
	}
	if err := event.Validate(); err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	at := event.Time.UTC()
	encodedAt := formatStoredTime(at)
	aggregateID := diagnostics.AggregateID(event)

	store.operationMu.Lock()
	defer store.operationMu.Unlock()

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return diagnosticWriteError(err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	settings, err := readDiagnosticSettings(ctx, tx)
	if err != nil {
		rollback()
		return diagnosticWriteError(err)
	}
	cutoff := store.clock.Now().UTC().Add(-time.Duration(settings.RetentionDays) * 24 * time.Hour)
	if _, err := tx.ExecContext(ctx, "DELETE FROM diagnostic_aggregates WHERE last_seen_at < ?", formatStoredTime(cutoff)); err != nil {
		rollback()
		return diagnosticWriteError(err)
	}
	if !settings.Enabled || !diagnosticLevelIncludes(settings.MinimumLevel, event.Severity) || at.Before(cutoff) {
		if err := tx.Commit(); err != nil {
			rollback()
			return diagnosticWriteError(err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO diagnostic_aggregates (
		diagnostic_aggregate_id, component, error_code, severity,
		occurrence_count, first_seen_at, last_seen_at
	) VALUES (?, ?, ?, ?, 1, ?, ?)
	ON CONFLICT(diagnostic_aggregate_id) DO UPDATE SET
		occurrence_count = CASE
			WHEN diagnostic_aggregates.occurrence_count >= ? THEN ?
			ELSE diagnostic_aggregates.occurrence_count + 1
		END,
		first_seen_at = CASE
			WHEN excluded.first_seen_at < diagnostic_aggregates.first_seen_at THEN excluded.first_seen_at
			ELSE diagnostic_aggregates.first_seen_at
		END,
		last_seen_at = CASE
			WHEN excluded.last_seen_at > diagnostic_aggregates.last_seen_at THEN excluded.last_seen_at
			ELSE diagnostic_aggregates.last_seen_at
		END`,
		aggregateID,
		event.Component,
		event.ErrorCode,
		event.Severity,
		encodedAt,
		encodedAt,
		diagnostics.MaxOccurrenceCount,
		diagnostics.MaxOccurrenceCount,
	); err != nil {
		rollback()
		return diagnosticWriteError(err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM diagnostic_aggregates
		WHERE diagnostic_aggregate_id NOT IN (
			SELECT diagnostic_aggregate_id FROM diagnostic_aggregates
			ORDER BY last_seen_at DESC, diagnostic_aggregate_id ASC LIMIT ?
		)`, diagnostics.MaxAggregateCount); err != nil {
		rollback()
		return diagnosticWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return diagnosticWriteError(err)
	}
	return nil
}

// ListDiagnosticAggregates returns only the redacted aggregate projection,
// newest first with a deterministic identifier tie-breaker.
func (store *Store) ListDiagnosticAggregates(ctx context.Context) ([]diagnostics.Aggregate, error) {
	if store == nil || store.db == nil {
		return nil, apperrors.New(apperrors.StoreReadFailed, ErrDiagnosticAggregate)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	settings, err := readDiagnosticSettings(ctx, store.db)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	cutoff := formatStoredTime(store.clock.Now().UTC().Add(-time.Duration(settings.RetentionDays) * 24 * time.Hour))

	rows, err := store.db.QueryContext(ctx, `SELECT diagnostic_aggregate_id, component, error_code,
		severity, occurrence_count, first_seen_at, last_seen_at
		FROM diagnostic_aggregates WHERE last_seen_at >= ?
		ORDER BY last_seen_at DESC, diagnostic_aggregate_id ASC`, cutoff)
	if err != nil {
		return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	defer func() { _ = rows.Close() }()

	aggregates := make([]diagnostics.Aggregate, 0)
	for rows.Next() {
		var aggregate diagnostics.Aggregate
		var firstSeenAt, lastSeenAt string
		if err := rows.Scan(&aggregate.ID, &aggregate.Component, &aggregate.ErrorCode, &aggregate.Severity, &aggregate.OccurrenceCount, &firstSeenAt, &lastSeenAt); err != nil {
			return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
		}
		aggregate.FirstSeenAt, err = time.Parse(time.RFC3339Nano, firstSeenAt)
		if err != nil {
			return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
		}
		aggregate.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt)
		if err != nil {
			return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
		}
		if err := aggregate.Validate(); err != nil {
			return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
		}
		aggregates = append(aggregates, aggregate)
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.New(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	return aggregates, nil
}

// DiagnosticSettings returns the durable independent collection policy. A
// missing singleton row resolves to the reviewed safe defaults.
func (store *Store) DiagnosticSettings(ctx context.Context) (diagnostics.Settings, error) {
	if store == nil || store.db == nil {
		return diagnostics.Settings{}, coded(apperrors.StoreReadFailed, ErrDiagnosticAggregate)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	settings, err := readDiagnosticSettings(contextOrBackground(ctx), store.db)
	if err != nil {
		return diagnostics.Settings{}, coded(apperrors.StoreReadFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	return settings, nil
}

// SetDiagnosticSettings persists only validated bounded policy values.
func (store *Store) SetDiagnosticSettings(ctx context.Context, settings diagnostics.Settings) (diagnostics.Settings, error) {
	if store == nil || store.db == nil {
		return diagnostics.Settings{}, coded(apperrors.StoreWriteFailed, ErrDiagnosticAggregate)
	}
	if err := settings.Validate(); err != nil {
		return diagnostics.Settings{}, err
	}
	enabled := 0
	if settings.Enabled {
		enabled = 1
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	ctx = contextOrBackground(ctx)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return diagnostics.Settings{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO settings
		(settings_id, diagnostics_enabled, diagnostics_level, diagnostics_retention_days, updated_at)
		VALUES (1, ?, ?, ?, ?) ON CONFLICT(settings_id) DO UPDATE SET
		diagnostics_enabled = excluded.diagnostics_enabled,
		diagnostics_level = excluded.diagnostics_level,
		diagnostics_retention_days = excluded.diagnostics_retention_days,
		updated_at = excluded.updated_at`, enabled, settings.MinimumLevel, settings.RetentionDays, formatStoredTime(store.clock.Now()))
	if err != nil {
		return diagnostics.Settings{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	cutoff := store.clock.Now().UTC().Add(-time.Duration(settings.RetentionDays) * 24 * time.Hour)
	if _, err := tx.ExecContext(ctx, `DELETE FROM diagnostic_aggregates WHERE last_seen_at < ?`, formatStoredTime(cutoff)); err != nil {
		return diagnostics.Settings{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	if err := tx.Commit(); err != nil {
		return diagnostics.Settings{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrDiagnosticAggregate, err))
	}
	return settings, nil
}

type diagnosticSettingsReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readDiagnosticSettings(ctx context.Context, reader diagnosticSettingsReader) (diagnostics.Settings, error) {
	settings := diagnostics.DefaultSettings()
	var enabled int
	var level string
	err := reader.QueryRowContext(ctx, `SELECT diagnostics_enabled, diagnostics_level, diagnostics_retention_days
		FROM settings WHERE settings_id = 1`).Scan(&enabled, &level, &settings.RetentionDays)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return diagnostics.Settings{}, err
	}
	settings.Enabled = enabled == 1
	settings.MinimumLevel = diagnostics.Level(level)
	if err := settings.Validate(); err != nil {
		return diagnostics.Settings{}, err
	}
	return settings, nil
}

func diagnosticLevelIncludes(minimum diagnostics.Level, severity diagnostics.Severity) bool {
	rank := func(value string) int {
		switch value {
		case "warning":
			return 1
		case "error":
			return 2
		default:
			return 0
		}
	}
	return rank(string(severity)) >= rank(string(minimum))
}

// PurgeDiagnosticAggregates removes aggregate buckets older than before. It
// is explicit so future settings and local controls can choose the boundary
// without changing the persisted projection.
func (store *Store) PurgeDiagnosticAggregates(ctx context.Context, before time.Time) error {
	if store == nil || store.db == nil {
		return apperrors.New(apperrors.StoreDiagnosticWriteFailed, ErrDiagnosticAggregate)
	}
	if before.IsZero() {
		return apperrors.New(apperrors.StoreDiagnosticWriteFailed, errors.New("diagnostic purge time is invalid"))
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	if _, err := store.db.ExecContext(ctx, "DELETE FROM diagnostic_aggregates WHERE last_seen_at < ?", formatStoredTime(before.UTC())); err != nil {
		return diagnosticWriteError(err)
	}
	return nil
}

func diagnosticWriteError(err error) error {
	if err == nil {
		return apperrors.New(apperrors.StoreDiagnosticWriteFailed, ErrDiagnosticAggregate)
	}
	return apperrors.New(apperrors.StoreDiagnosticWriteFailed, errors.Join(ErrDiagnosticAggregate, err))
}
