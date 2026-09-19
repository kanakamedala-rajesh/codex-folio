package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/telemetry"
)

var ErrTelemetryState = errors.New("telemetry state could not be stored")

func (store *Store) TelemetryState(ctx context.Context) (telemetry.State, error) {
	if store == nil || store.db == nil {
		return telemetry.State{}, coded(apperrors.StoreReadFailed, ErrTelemetryState)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var state telemetry.State
	var enabled int
	var consentedAt sql.NullString
	err := store.db.QueryRowContext(contextOrBackground(ctx), `SELECT enabled, schema_version, consented_at, installation_id FROM telemetry_state WHERE telemetry_state_id = 1`).Scan(
		&enabled, &state.Consent.SchemaVersion, &consentedAt, &state.InstallationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return telemetry.State{}, nil
	}
	if err != nil {
		return telemetry.State{}, coded(apperrors.StoreReadFailed, errors.Join(ErrTelemetryState, err))
	}
	state.Consent.Enabled = enabled == 1
	if consentedAt.Valid {
		state.Consent.ConsentedAt, err = time.Parse(time.RFC3339Nano, consentedAt.String)
		if err != nil {
			return telemetry.State{}, coded(apperrors.StoreReadFailed, errors.Join(ErrTelemetryState, err))
		}
	}
	if err := telemetry.ValidateState(state); err != nil {
		return telemetry.State{}, coded(apperrors.StoreReadFailed, errors.Join(ErrTelemetryState, err))
	}
	return state, nil
}

func (store *Store) SetTelemetryState(ctx context.Context, state telemetry.State) (telemetry.State, error) {
	if store == nil || store.db == nil {
		return telemetry.State{}, coded(apperrors.StoreWriteFailed, ErrTelemetryState)
	}
	if err := telemetry.ValidateState(state); err != nil {
		return telemetry.State{}, err
	}
	enabled := 0
	var consentedAt any
	if state.Consent.Enabled {
		if state.Consent.SchemaVersion <= 0 || state.Consent.ConsentedAt.IsZero() {
			return telemetry.State{}, telemetry.ErrInvalid
		}
		enabled = 1
		consentedAt = formatStoredTime(state.Consent.ConsentedAt.UTC())
	} else if state.Consent.SchemaVersion != 0 || !state.Consent.ConsentedAt.IsZero() {
		return telemetry.State{}, telemetry.ErrInvalid
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(contextOrBackground(ctx), `INSERT INTO telemetry_state (telemetry_state_id, enabled, schema_version, consented_at, installation_id)
		VALUES (1, ?, ?, ?, ?) ON CONFLICT(telemetry_state_id) DO UPDATE SET
		enabled = excluded.enabled, schema_version = excluded.schema_version,
		consented_at = excluded.consented_at, installation_id = excluded.installation_id`,
		enabled, state.Consent.SchemaVersion, consentedAt, state.InstallationID)
	if err != nil {
		return telemetry.State{}, coded(apperrors.StoreWriteFailed, errors.Join(ErrTelemetryState, err))
	}
	return state, nil
}
