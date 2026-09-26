package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	var ciphertext, loginCiphertext, workspaceCiphertext []byte
	err := store.db.QueryRowContext(ctx, `SELECT ip.profile_id, a.alias, ip.status, ip.identity_home_id, h.location_ciphertext,
		h.documented_login_identity_ciphertext, h.documented_workspace_ciphertext
		FROM identity_profiles ip
		JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		WHERE a.alias = ? COLLATE NOCASE
		AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = ip.profile_id)`, alias).Scan(&target.ID, &target.Alias, &status, &homeID, &ciphertext, &loginCiphertext, &workspaceCiphertext)
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
	if len(loginCiphertext) > 0 {
		target.LoginIdentity, err = decryptField(ctx, secureVault, loginCiphertext, documentedMetadataAAD(target.ID, "login-identity"))
		if err != nil {
			return usage.ProfileTarget{}, err
		}
	}
	if len(workspaceCiphertext) > 0 {
		target.Workspace, err = decryptField(ctx, secureVault, workspaceCiphertext, documentedMetadataAAD(target.ID, "workspace"))
		if err != nil {
			return usage.ProfileTarget{}, err
		}
	}
	target.Eligible = true
	return target, nil
}

func (store *Store) ListUsageProfiles(ctx context.Context) ([]usage.ProfileTarget, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, usage.ErrPersistenceFailed)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT ip.profile_id, a.alias,
		CASE WHEN s.profile_id = ip.profile_id THEN 1 ELSE 0 END,
		CASE WHEN ip.status = 'ready' AND h.location_ciphertext IS NOT NULL AND h.ownership IN ('managed', 'referenced')
			AND COALESCE((SELECT us.status FROM usage_snapshots us WHERE us.profile_id = ip.profile_id AND us.status <> 'temporarily_unavailable' ORDER BY rtrim(us.captured_at, 'Z') DESC, us.snapshot_id DESC LIMIT 1), '') <> 'reauthentication_required'
			THEN 1 ELSE 0 END, h.identity_home_id, h.location_ciphertext
		FROM identity_profiles ip
		JOIN cli_aliases a ON a.profile_id = ip.profile_id
		LEFT JOIN identity_homes h ON h.identity_home_id = ip.identity_home_id
		LEFT JOIN selected_profile s ON s.profile_id = ip.profile_id
		WHERE NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = ip.profile_id)
		ORDER BY CASE WHEN s.profile_id = ip.profile_id THEN 0 ELSE 1 END, a.alias COLLATE NOCASE`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	defer rows.Close()
	profiles := []usage.ProfileTarget{}
	for rows.Next() {
		var target usage.ProfileTarget
		var homeID sql.NullString
		var ciphertext []byte
		if err := rows.Scan(&target.ID, &target.Alias, &target.Selected, &target.Eligible, &homeID, &ciphertext); err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		if target.Eligible {
			secureVault, err := store.requireVault()
			if err != nil {
				return nil, err
			}
			home, err := decryptField(ctx, secureVault, ciphertext, identityHomeAAD(homeID.String))
			if err != nil {
				return nil, err
			}
			info, statErr := os.Stat(filepath.Clean(home))
			target.Eligible = filepath.IsAbs(home) && statErr == nil && info.IsDir()
		}
		profiles = append(profiles, target)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return profiles, nil
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
	var loginCiphertext, workspaceCiphertext []byte
	if target.LoginIdentity != "" || target.Workspace != "" {
		secureVault, vaultErr := store.requireVault()
		if vaultErr != nil {
			rollback()
			return usage.Snapshot{}, vaultErr
		}
		if target.LoginIdentity != "" {
			loginCiphertext, err = encryptField(ctx, secureVault, []byte(target.LoginIdentity), usageScopeAAD(snapshotID, "login-identity"))
		}
		if err == nil && target.Workspace != "" {
			workspaceCiphertext, err = encryptField(ctx, secureVault, []byte(target.Workspace), usageScopeAAD(snapshotID, "workspace"))
		}
	}
	if err != nil {
		rollback()
		return usage.Snapshot{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO usage_snapshots (snapshot_id, profile_id, source, source_version, captured_at, status, trigger_reason, login_identity_ciphertext, workspace_ciphertext)
		SELECT ?, profile_id, ?, ?, ?, ?, ?, ?, ? FROM identity_profiles WHERE profile_id = ? AND status = 'ready'`, snapshotID, snapshot.Source, snapshot.SourceVersion, formatStoredTime(snapshot.CapturedAt.UTC()), snapshot.Status, snapshot.TriggerReason, loginCiphertext, workspaceCiphertext, target.ID)
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
		availabilityState = storedProvenanceAvailabilityState(snapshot.Availability[0].State)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metric_provenance (provenance_id, source, source_version, captured_at, freshness, availability, provenance_label)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, provenanceID, snapshot.Source, snapshot.SourceVersion, formatStoredTime(snapshot.CapturedAt.UTC()), usage.FreshnessFresh, availabilityState, usage.ProvenanceProvider); err != nil {
		rollback()
		return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	availabilityProvenance := map[string]string{usage.ProvenanceProvider: provenanceID}
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
		itemProvenanceID := availabilityProvenance[item.Provenance]
		if itemProvenanceID == "" {
			itemProvenanceID, err = newStoreIdentifier("provenance")
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO metric_provenance (provenance_id, source, source_version, captured_at, freshness, availability, provenance_label) VALUES (?, ?, ?, ?, ?, ?, ?)`, itemProvenanceID, snapshot.Source, snapshot.SourceVersion, formatStoredTime(snapshot.CapturedAt.UTC()), usage.FreshnessFresh, storedProvenanceAvailabilityState(item.State), item.Provenance)
			}
			if err != nil {
				rollback()
				return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
			}
			availabilityProvenance[item.Provenance] = itemProvenanceID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO metric_availability (metric_availability_id, profile_id, metric_key, state, checked_at, provenance_id, reason, condition) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, target.ID, item.MetricKey, storedAvailabilityState(item.State), formatStoredTime(item.CheckedAt.UTC()), itemProvenanceID, item.Reason, storedAvailabilityCondition(item.State)); err != nil {
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
		itemProvenanceID := provenanceID
		if item.Source != snapshot.Source || item.SourceVersion != snapshot.SourceVersion || item.CapturedAt != snapshot.CapturedAt || item.Provenance != usage.ProvenanceProvider {
			itemProvenanceID, err = newStoreIdentifier("provenance")
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO metric_provenance (provenance_id, source, source_version, captured_at, freshness, availability, provenance_label) VALUES (?, ?, ?, ?, ?, 'available', ?)`, itemProvenanceID, item.Source, item.SourceVersion, formatStoredTime(item.CapturedAt.UTC()), item.Freshness, item.Provenance)
			}
			if err != nil {
				rollback()
				return usage.Snapshot{}, coded(apperrors.StoreWriteFailed, errors.Join(usage.ErrPersistenceFailed, err))
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_observations (observation_id, profile_id, metric_key, provenance_id, metric_availability_id, value, unit, window_start, window_end, observed_at, snapshot_id, window_timezone, assumptions, uncertainty)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, target.ID, item.Metric.Key, itemProvenanceID, availabilityIDs[item.Metric.Key], item.Value, item.Metric.Unit, nullableStoredTime(item.WindowStart), nullableStoredTime(item.WindowEnd), formatStoredTime(item.ObservedAt.UTC()), snapshotID, item.WindowTimezone, item.Assumptions, item.Uncertainty); err != nil {
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

func (store *Store) LastUsageObservations(ctx context.Context, target usage.ProfileTarget) ([]usage.Observation, error) {
	if store == nil || store.db == nil || target.ID == "" {
		return nil, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return store.lastUsageObservations(ctx, target, "")
}

func (store *Store) LatestUsageSnapshot(ctx context.Context, target usage.ProfileTarget) (usage.Snapshot, error) {
	return store.usageSnapshot(ctx, target, "")
}

func (store *Store) usageSnapshot(ctx context.Context, target usage.ProfileTarget, snapshotID string) (usage.Snapshot, error) {
	if store == nil || store.db == nil || target.ID == "" || target.Alias == "" {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	snapshot := usage.Snapshot{ProfileID: target.ID, Alias: target.Alias, Observations: []usage.Observation{}, Availability: []usage.MetricAvailability{}}
	var capturedAt string
	var loginCiphertext, workspaceCiphertext []byte
	err := store.db.QueryRowContext(ctx, `SELECT snapshot_id, source, source_version, captured_at, status, trigger_reason, login_identity_ciphertext, workspace_ciphertext
		FROM usage_snapshots WHERE profile_id = ? AND (? = '' OR snapshot_id = ?) ORDER BY rtrim(captured_at, 'Z') DESC, snapshot_id DESC LIMIT 1`, target.ID, snapshotID, snapshotID).
		Scan(&snapshot.ID, &snapshot.Source, &snapshot.SourceVersion, &capturedAt, &snapshot.Status, &snapshot.TriggerReason, &loginCiphertext, &workspaceCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageProfileUnavailable, usage.ErrProfileUnavailable)
	}
	if err != nil {
		return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	if snapshot.CapturedAt, err = parseStoredTime(capturedAt); err != nil {
		return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	if len(loginCiphertext) > 0 || len(workspaceCiphertext) > 0 {
		secureVault, vaultErr := store.requireVault()
		if vaultErr != nil {
			return usage.Snapshot{}, vaultErr
		}
		if len(loginCiphertext) > 0 {
			snapshot.LoginIdentity, err = decryptField(ctx, secureVault, loginCiphertext, usageScopeAAD(snapshot.ID, "login-identity"))
		}
		if err == nil && len(workspaceCiphertext) > 0 {
			snapshot.Workspace, err = decryptField(ctx, secureVault, workspaceCiphertext, usageScopeAAD(snapshot.ID, "workspace"))
		}
	}
	if err != nil {
		return usage.Snapshot{}, err
	}
	rows, err := store.db.QueryContext(ctx, `SELECT a.metric_availability_id, a.metric_key, a.state, a.reason, a.condition, a.checked_at, p.provenance_label
		FROM metric_availability a JOIN metric_provenance p ON p.provenance_id = a.provenance_id
		WHERE a.profile_id = ? AND p.source = ? AND COALESCE(p.source_version, '') = ? AND p.captured_at = ?
		ORDER BY a.metric_key`, target.ID, snapshot.Source, snapshot.SourceVersion, capturedAt)
	if err != nil {
		return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	defer rows.Close()
	for rows.Next() {
		var item usage.MetricAvailability
		var checkedAt, condition string
		if err := rows.Scan(&item.ID, &item.MetricKey, &item.State, &item.Reason, &condition, &checkedAt, &item.Provenance); err != nil {
			return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		if condition != "" {
			item.State = condition
		}
		if item.CheckedAt, err = parseStoredTime(checkedAt); err != nil {
			return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		snapshot.Availability = append(snapshot.Availability, item)
	}
	if err := rows.Err(); err != nil {
		return usage.Snapshot{}, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	snapshot.Observations, err = store.lastUsageObservations(ctx, target, snapshotID)
	if err != nil {
		return usage.Snapshot{}, err
	}
	return snapshot, nil
}

func (store *Store) lastUsageObservations(ctx context.Context, target usage.ProfileTarget, snapshotID string) ([]usage.Observation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT * FROM (SELECT o.observation_id, o.metric_key, o.value, o.observed_at, o.window_start, o.window_end,
		o.window_timezone, o.assumptions, o.uncertainty, p.source, COALESCE(p.source_version, ''), p.captured_at, p.provenance_label, p.freshness, a.condition
		FROM usage_observations o JOIN metric_provenance p ON p.provenance_id = o.provenance_id
		JOIN metric_availability a ON a.metric_availability_id = o.metric_availability_id
		WHERE o.profile_id = ? AND (? = '' OR o.snapshot_id = ?)
 UNION ALL SELECT g.aggregate_id, g.metric_key, g.value, g.last_observed_at,
 CASE WHEN g.bucket_kind = 'source_window' THEN g.bucket_start ELSE NULL END,
 CASE WHEN g.bucket_kind = 'source_window' THEN g.bucket_end ELSE NULL END,
 g.timezone, g.assumptions, g.uncertainty, g.source, g.source_version, g.last_captured_at, g.provenance_label, 'stale',
 CASE WHEN g.availability = 'contradictory' THEN g.availability ELSE '' END
 FROM usage_aggregates g WHERE g.profile_id = ? AND ? = ''
 ) ORDER BY rtrim(captured_at, 'Z') DESC`, target.ID, snapshotID, snapshotID, target.ID, snapshotID)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	defer rows.Close()
	registry := make(map[string]usage.Metric, len(usage.Registry()))
	for _, metric := range usage.Registry() {
		registry[metric.Key] = metric
	}
	latest := make(map[string]time.Time, len(registry))
	var observations []usage.Observation
	for rows.Next() {
		var item usage.Observation
		var key, observedAt, capturedAt, condition string
		var windowStart, windowEnd sql.NullString
		if err := rows.Scan(&item.ID, &key, &item.Value, &observedAt, &windowStart, &windowEnd, &item.WindowTimezone, &item.Assumptions, &item.Uncertainty, &item.Source, &item.SourceVersion, &capturedAt, &item.Provenance, &item.Freshness, &condition); err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		item.Metric = registry[key]
		item.ObservedAt, err = parseStoredTime(observedAt)
		if err == nil {
			item.CapturedAt, err = parseStoredTime(capturedAt)
		}
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		evidenceKey := strings.Join([]string{key, item.Source, item.SourceVersion, item.Provenance}, "\x00")
		if previous, ok := latest[evidenceKey]; ok && previous != item.CapturedAt {
			continue
		}
		latest[evidenceKey] = item.CapturedAt
		if item.WindowStart, err = parseNullableUsageTime(windowStart); err == nil {
			item.WindowEnd, err = parseNullableUsageTime(windowEnd)
		}
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
		}
		item.Availability = usage.AvailabilityAvailable
		if condition == usage.AvailabilityContradictory {
			item.Availability = condition
		}
		observations = append(observations, item)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(usage.ErrPersistenceFailed, err))
	}
	return observations, nil
}

func validUsageSnapshot(target usage.ProfileTarget, snapshot usage.Snapshot) bool {
	if target.ID == "" || target.Alias == "" || snapshot.Source != usage.SourceCodexAppServer || strings.TrimSpace(snapshot.SourceVersion) == "" || snapshot.CapturedAt.IsZero() || !validSnapshotStatus(snapshot.Status) || !usage.ValidTriggerReason(snapshot.TriggerReason) || len(snapshot.Availability) != len(usage.Registry()) {
		return false
	}
	registry := make(map[string]usage.Metric, len(usage.Registry()))
	for _, metric := range usage.Registry() {
		registry[metric.Key] = metric
	}
	states := make(map[string]string, len(snapshot.Availability))
	for _, item := range snapshot.Availability {
		if _, ok := registry[item.MetricKey]; !ok || item.CheckedAt.IsZero() || !validUsageProvenance(item.Provenance) || !validAvailability(item.State) || states[item.MetricKey] != "" {
			return false
		}
		states[item.MetricKey] = item.State
	}
	for _, item := range snapshot.Observations {
		metric, ok := registry[item.Metric.Key]
		if !ok || item.Metric != metric || item.ObservedAt.IsZero() || item.CapturedAt.IsZero() || item.CapturedAt.After(snapshot.CapturedAt) || !validUsageSource(item.Source) || strings.TrimSpace(item.SourceVersion) == "" || !validUsageProvenance(item.Provenance) || (item.Provenance == usage.ProvenanceEstimated && item.Assumptions == "" && item.Uncertainty == "") || !validFreshness(item.Freshness) || !validObservationAvailability(item.Availability) || states[item.Metric.Key] != item.Availability || (item.WindowStart == nil) != (item.WindowEnd == nil) || (item.WindowStart != nil && item.WindowTimezone == "") {
			return false
		}
	}
	return true
}

func validSnapshotStatus(value string) bool {
	return validAvailability(value) || value == usage.AvailabilityPartial
}

func validUsageSource(value string) bool {
	return value == usage.SourceCodexAppServer || value == usage.SourceLocalMetadata || value == usage.SourceDerived
}

func validUsageProvenance(value string) bool {
	return value == usage.ProvenanceProvider || value == usage.ProvenanceLocal || value == usage.ProvenanceEstimated || value == usage.ProvenanceObserved
}

func validAvailability(state string) bool {
	return state == usage.AvailabilityAvailable || state == usage.AvailabilityUnsupported || state == usage.AvailabilityTemporarilyUnavailable || state == usage.AvailabilityStale || state == usage.AvailabilityReauthenticationRequired || state == usage.AvailabilityContradictory || state == usage.AvailabilityNoActivity
}

func validFreshness(value string) bool {
	return value == usage.FreshnessFresh || value == usage.FreshnessStale
}

func validObservationAvailability(value string) bool {
	return value == usage.AvailabilityAvailable || value == usage.AvailabilityStale || value == usage.AvailabilityContradictory
}

func storedAvailabilityState(value string) string {
	if value == usage.AvailabilityContradictory {
		return usage.AvailabilityAvailable
	}
	return value
}

func storedProvenanceAvailabilityState(value string) string {
	if value == usage.AvailabilityNoActivity {
		return usage.AvailabilityTemporarilyUnavailable
	}
	return storedAvailabilityState(value)
}

func storedAvailabilityCondition(value string) string {
	if value == usage.AvailabilityContradictory {
		return value
	}
	return ""
}

func parseNullableUsageTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseStoredTime(value.String)
	return &parsed, err
}

func nullableStoredTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatStoredTime(value.UTC())
}

func usageScopeAAD(snapshotID, field string) []byte {
	return []byte("codex-folio/usage-snapshots/" + snapshotID + "/" + field)
}

var _ usage.Store = (*Store)(nil)

// LatestSuccessfulUsageRefresh returns the newest capture that persisted at
// least one normalized observation. Unlike RecentUsageSnapshots, this query is
// not bounded by the dashboard trace depth.
func (store *Store) LatestSuccessfulUsageRefresh(ctx context.Context, target usage.ProfileTarget) (time.Time, error) {
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var capturedAt string
	err := store.db.QueryRowContext(ctx, `SELECT s.captured_at FROM usage_snapshots s
		WHERE s.profile_id = ? AND EXISTS (SELECT 1 FROM usage_observations o WHERE o.snapshot_id = s.snapshot_id)
		ORDER BY rtrim(s.captured_at, 'Z') DESC, s.snapshot_id DESC LIMIT 1`, target.ID).Scan(&capturedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, coded(apperrors.StoreReadFailed, err)
	}
	result, err := parseStoredTime(capturedAt)
	if err != nil {
		return time.Time{}, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

// RecentUsageSnapshots returns the last twelve raw captures, including failed
// captures as gaps. It never fills a historical gap with last-known evidence.
func (store *Store) RecentUsageSnapshots(ctx context.Context, target usage.ProfileTarget) ([]usage.Snapshot, error) {
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	rows, err := store.db.QueryContext(ctx, `SELECT snapshot_id FROM usage_snapshots WHERE profile_id = ? ORDER BY rtrim(captured_at, 'Z') DESC, snapshot_id DESC LIMIT 12`, target.ID)
	if err != nil {
		store.operationMu.RUnlock()
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	store.operationMu.RUnlock()
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	result := make([]usage.Snapshot, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		snapshot, err := store.usageSnapshot(ctx, target, ids[i])
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}
