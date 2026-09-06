package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (store *Store) ResolveUsageProfile(ctx context.Context, alias string) (usage.ProfileTarget, error) {
	if store == nil || store.db == nil || profile.ValidateAlias(alias) != nil {
		return usage.ProfileTarget{}, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var target usage.ProfileTarget
	var status string
	var homeID sql.NullString
	var ciphertext []byte
	err := store.db.QueryRowContext(ctx, `SELECT ip.profile_id, a.alias, ip.status, ip.identity_home_id, h.location_ciphertext
		FROM identity_profiles ip
		JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		WHERE a.alias = ? COLLATE NOCASE
		AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = ip.profile_id)`, alias).Scan(&target.ID, &target.Alias, &status, &homeID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.ProfileTarget{}, apperrors.New(apperrors.UsageProfileNotFound, usage.ErrProfileNotFound)
	}
	if err != nil {
		return usage.ProfileTarget{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	if status != string(profile.StatusReady) || !homeID.Valid || strings.TrimSpace(homeID.String) == "" || len(ciphertext) == 0 {
		return usage.ProfileTarget{}, apperrors.New(apperrors.UsageProfileUnavailable, usage.ErrProfileUnavailable)
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return usage.ProfileTarget{}, err
	}
	target.IdentityHome, err = decryptField(ctx, secureVault, ciphertext, identityHomeAAD(homeID.String))
	if err != nil {
		return usage.ProfileTarget{}, err
	}
	target.IdentityHome = filepath.Clean(target.IdentityHome)
	if !filepath.IsAbs(target.IdentityHome) {
		return usage.ProfileTarget{}, apperrors.New(apperrors.UsageProfileUnavailable, usage.ErrProfileUnavailable)
	}
	return target, nil
}

func (store *Store) SaveUsageSnapshot(ctx context.Context, target usage.ProfileTarget, snapshot usage.Snapshot) (usage.Snapshot, error) {
	if store == nil || store.db == nil || !validUsageSnapshot(target, snapshot) {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	rollback := func() { _ = tx.Rollback() }
	snapshotID, err := newStoreIdentifier("snapshot")
	if err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO usage_snapshots (snapshot_id, profile_id, source, source_version, captured_at)
		SELECT ?, profile_id, ?, ?, ? FROM identity_profiles WHERE profile_id = ? AND status = 'ready'`, snapshotID, snapshot.Source, snapshot.SourceVersion, formatStoredTime(snapshot.CapturedAt.UTC()), target.ID)
	if err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		rollback()
		return usage.Snapshot{}, apperrors.New(apperrors.UsageProfileUnavailable, usage.ErrProfileUnavailable)
	}
	for _, metric := range usage.Registry() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_metrics (metric_key, unit, value_kind, created_at, source_class, scope, aggregation)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(metric_key) DO UPDATE SET unit = excluded.unit, value_kind = excluded.value_kind, source_class = excluded.source_class, scope = excluded.scope, aggregation = excluded.aggregation`,
			metric.Key, metric.Unit, metric.ValueKind, formatStoredTime(snapshot.CapturedAt.UTC()), metric.SourceClass, metric.Scope, metric.Aggregation); err != nil {
			rollback()
			return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
	}
	provenanceID, err := newStoreIdentifier("provenance")
	if err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	availabilityState := usage.AvailabilityAvailable
	if len(snapshot.Observations) == 0 {
		availabilityState = snapshot.Availability[0].State
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_provenance (provenance_id, source, source_version, captured_at, freshness, availability, provenance_label)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, provenanceID, snapshot.Source, snapshot.SourceVersion, formatStoredTime(snapshot.CapturedAt.UTC()), usage.FreshnessFresh, availabilityState, usage.ProvenanceProvider); err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	snapshot.ID, snapshot.ProfileID, snapshot.Alias = snapshotID, target.ID, target.Alias
	availabilityIDs := make(map[string]string, len(snapshot.Availability))
	for index := range snapshot.Availability {
		item := &snapshot.Availability[index]
		item.ID, err = newStoreIdentifier("availability")
		if err != nil {
			rollback()
			return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		availabilityIDs[item.MetricKey] = item.ID
		if _, err := tx.ExecContext(ctx, `INSERT INTO metric_availability (metric_availability_id, profile_id, metric_key, state, checked_at, provenance_id) VALUES (?, ?, ?, ?, ?, ?)`, item.ID, target.ID, item.MetricKey, item.State, formatStoredTime(item.CheckedAt.UTC()), provenanceID); err != nil {
			rollback()
			return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
	}
	for index := range snapshot.Observations {
		item := &snapshot.Observations[index]
		item.ID, err = newStoreIdentifier("observation")
		if err != nil {
			rollback()
			return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_observations (observation_id, profile_id, metric_key, provenance_id, metric_availability_id, value, unit, window_start, window_end, observed_at, snapshot_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, target.ID, item.Metric.Key, provenanceID, availabilityIDs[item.Metric.Key], item.Value, item.Metric.Unit, nullableStoredTime(item.WindowStart), nullableStoredTime(item.WindowEnd), formatStoredTime(item.ObservedAt.UTC()), snapshotID); err != nil {
			rollback()
			return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return snapshot, nil
}

func validUsageSnapshot(target usage.ProfileTarget, snapshot usage.Snapshot) bool {
	if target.ID == "" || target.Alias == "" || snapshot.Source != usage.SourceCodexAppServer || strings.TrimSpace(snapshot.SourceVersion) == "" || snapshot.CapturedAt.IsZero() || len(snapshot.Availability) != len(usage.Registry()) {
		return false
	}
	registry := make(map[string]usage.Metric, len(usage.Registry()))
	for _, metric := range usage.Registry() {
		registry[metric.Key] = metric
	}
	states := make(map[string]string, len(snapshot.Availability))
	for _, item := range snapshot.Availability {
		if _, ok := registry[item.MetricKey]; !ok || item.CheckedAt.IsZero() || item.Provenance != usage.ProvenanceProvider || !validAvailability(item.State) || states[item.MetricKey] != "" {
			return false
		}
		states[item.MetricKey] = item.State
	}
	for _, item := range snapshot.Observations {
		metric, ok := registry[item.Metric.Key]
		if !ok || item.Metric != metric || item.ObservedAt.IsZero() || item.Provenance != usage.ProvenanceProvider || item.Freshness != usage.FreshnessFresh || item.Availability != usage.AvailabilityAvailable || states[item.Metric.Key] != usage.AvailabilityAvailable {
			return false
		}
	}
	return true
}

func validAvailability(state string) bool {
	return state == usage.AvailabilityAvailable || state == usage.AvailabilityUnsupported || state == usage.AvailabilityTemporarilyUnavailable || state == usage.AvailabilityReauthenticationRequired
}

func nullableStoredTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatStoredTime(value.UTC())
}

var _ usage.Store = (*Store)(nil)
