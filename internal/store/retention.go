package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (store *Store) AnalyticsRetention(ctx context.Context) (usage.Retention, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return readAnalyticsRetention(contextOrBackground(ctx), store.db)
}

func readAnalyticsRetention(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (usage.Retention, error) {
	var mode string
	var days sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT analytics_retention_mode, analytics_retention_days FROM settings WHERE settings_id = 1`).Scan(&mode, &days)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.Retention{}, nil
	}
	if err != nil {
		return usage.Retention{}, coded(apperrors.StoreReadFailed, err)
	}
	return usage.Retention{Days: int(days.Int64), Unlimited: mode == "unlimited"}, nil
}

func (store *Store) SetAnalyticsRetention(ctx context.Context, setting string) (usage.Retention, error) {
	policy, err := usage.ParseRetention(setting)
	if err != nil {
		return usage.Retention{}, apperrors.New(apperrors.AnalyticsRequestInvalid, err)
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	mode := "default"
	var days any
	if policy.Unlimited {
		mode = "unlimited"
	} else if policy.Days != 0 {
		mode, days = "days", policy.Days
	}
	_, err = store.db.ExecContext(contextOrBackground(ctx), `INSERT INTO settings (settings_id, analytics_retention_mode, analytics_retention_days, updated_at)
 VALUES (1, ?, ?, ?) ON CONFLICT(settings_id) DO UPDATE SET analytics_retention_mode = excluded.analytics_retention_mode, analytics_retention_days = excluded.analytics_retention_days, updated_at = excluded.updated_at`, mode, days, formatStoredTime(store.clock.Now()))
	if err != nil {
		return usage.Retention{}, coded(apperrors.StoreWriteFailed, err)
	}
	return policy, nil
}

// RetainAnalytics commits at most one bounded batch per record class. Each
// distinct-fact aggregate and its source deletion share the same transaction;
// interruption rolls both back, so the next collection/run can safely resume.
func (store *Store) RetainAnalytics(ctx context.Context) (usage.RetentionResult, error) {
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return usage.RetentionResult{}, coded(apperrors.StoreWriteFailed, err)
	}
	defer tx.Rollback()
	policy, err := readAnalyticsRetention(ctx, tx)
	if err != nil {
		return usage.RetentionResult{}, err
	}
	result := usage.RetentionResult{Setting: policy.String()}
	cutoff := policy.Cutoff(store.clock.Now())
	if cutoff == nil {
		return result, nil
	}
	before := strings.TrimSuffix(formatStoredTime(*cutoff), "Z")
	rows, err := tx.QueryContext(ctx, `SELECT o.observation_id, o.profile_id, o.metric_key, o.value, o.observed_at,
 o.window_start, o.window_end, o.window_timezone, o.assumptions, o.uncertainty,
 p.source, COALESCE(p.source_version, ''), p.captured_at, p.provenance_label,
 CASE WHEN a.condition <> '' THEN a.condition ELSE a.state END,
 COALESCE(s.snapshot_id, ''), s.login_identity_ciphertext, s.workspace_ciphertext
 FROM usage_observations o JOIN metric_provenance p ON p.provenance_id = o.provenance_id
 JOIN metric_availability a ON a.metric_availability_id = o.metric_availability_id
 LEFT JOIN usage_snapshots s ON s.snapshot_id = o.snapshot_id
 WHERE rtrim(o.observed_at, 'Z') < ? AND rtrim(p.captured_at, 'Z') < ?
 ORDER BY rtrim(o.observed_at, 'Z'), o.observation_id LIMIT ?`, before, before, usage.RetentionBatchSize)
	if err != nil {
		return result, coded(apperrors.StoreReadFailed, err)
	}
	type detail struct {
		observation           usage.Observation
		profileID, snapshotID string
		login, workspace      []byte
	}
	details := []detail{}
	for rows.Next() {
		var d detail
		var key, observed, captured string
		var start, end sql.NullString
		o := &d.observation
		err = rows.Scan(&o.ID, &d.profileID, &key, &o.Value, &observed, &start, &end, &o.WindowTimezone, &o.Assumptions, &o.Uncertainty, &o.Source, &o.SourceVersion, &captured, &o.Provenance, &o.Availability, &d.snapshotID, &d.login, &d.workspace)
		if err == nil {
			o.ObservedAt, err = parseStoredTime(observed)
		}
		if err == nil {
			o.CapturedAt, err = parseStoredTime(captured)
		}
		if err == nil {
			o.WindowStart, err = parseNullableUsageTime(start)
		}
		if err == nil {
			o.WindowEnd, err = parseNullableUsageTime(end)
		}
		if err != nil {
			rows.Close()
			return result, coded(apperrors.StoreReadFailed, err)
		}
		for _, metric := range usage.Registry() {
			if metric.Key == key {
				o.Metric = metric
			}
		}
		details = append(details, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, coded(apperrors.StoreReadFailed, err)
	}
	for _, d := range details {
		var scope aggregateSourceScope
		if len(d.login) > 0 || len(d.workspace) > 0 {
			secureVault, vaultErr := store.requireVault()
			if vaultErr != nil {
				return result, vaultErr
			}
			if len(d.login) > 0 {
				scope.LoginIdentity, err = decryptField(ctx, secureVault, d.login, usageScopeAAD(d.snapshotID, "login-identity"))
			}
			if err == nil && len(d.workspace) > 0 {
				scope.Workspace, err = decryptField(ctx, secureVault, d.workspace, usageScopeAAD(d.snapshotID, "workspace"))
			}
			if err != nil {
				return result, err
			}
		}
		if err := store.aggregateObservation(ctx, tx, d.profileID, d.observation, scope); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_observations WHERE observation_id = ?`, d.observation.ID); err != nil {
			return result, coded(apperrors.StoreWriteFailed, err)
		}
		result.Processed++
	}
	result.More = len(details) == usage.RetentionBatchSize
	// Keep the latest status and latest decisive authentication evidence. They
	// serve current-state projections; all expired observation detail is compacted.
	statements := []string{
		`DELETE FROM usage_snapshots WHERE snapshot_id IN (SELECT s.snapshot_id FROM usage_snapshots s
   WHERE rtrim(s.captured_at, 'Z') < ?
   AND NOT EXISTS (SELECT 1 FROM usage_observations o WHERE o.snapshot_id = s.snapshot_id)
   AND s.snapshot_id <> (SELECT n.snapshot_id FROM usage_snapshots n WHERE n.profile_id = s.profile_id ORDER BY rtrim(n.captured_at, 'Z') DESC, n.snapshot_id DESC LIMIT 1)
   AND s.snapshot_id <> COALESCE((SELECT n.snapshot_id FROM usage_snapshots n WHERE n.profile_id = s.profile_id AND n.status <> 'temporarily_unavailable' ORDER BY rtrim(n.captured_at, 'Z') DESC, n.snapshot_id DESC LIMIT 1), '') LIMIT ?)`,
		`DELETE FROM metric_availability WHERE metric_availability_id IN (SELECT a.metric_availability_id FROM metric_availability a
   JOIN metric_provenance p ON p.provenance_id = a.provenance_id WHERE rtrim(a.checked_at, 'Z') < ?
   AND NOT EXISTS (SELECT 1 FROM usage_observations o WHERE o.metric_availability_id = a.metric_availability_id)
   AND NOT EXISTS (SELECT 1 FROM usage_snapshots s WHERE s.profile_id = a.profile_id AND s.source = p.source AND s.source_version = COALESCE(p.source_version, '') AND s.captured_at = p.captured_at) LIMIT ?)`,
		`DELETE FROM metric_provenance WHERE provenance_id IN (SELECT p.provenance_id FROM metric_provenance p WHERE rtrim(p.captured_at, 'Z') < ?
   AND NOT EXISTS (SELECT 1 FROM metric_availability a WHERE a.provenance_id = p.provenance_id)
   AND NOT EXISTS (SELECT 1 FROM usage_observations o WHERE o.provenance_id = p.provenance_id) LIMIT ?)`,
	}
	for _, statement := range statements {
		changed, execErr := tx.ExecContext(ctx, statement, before, usage.RetentionBatchSize)
		if execErr != nil {
			return result, coded(apperrors.StoreWriteFailed, execErr)
		}
		count, _ := changed.RowsAffected()
		result.Processed += count
		result.More = result.More || count == usage.RetentionBatchSize
	}
	// Observed Session metadata is eligible at its last observation, never merely
	// its start. Running/pending launches and referenced launch records stay intact.
	sessionIDs, err := historyIDs(ctx, tx, `SELECT observed_session_id FROM observed_sessions WHERE rtrim(COALESCE(last_observed_at, ended_at, started_at), 'Z') < ? LIMIT ?`, []any{before, usage.RetentionBatchSize})
	if err != nil {
		return result, coded(apperrors.StoreReadFailed, err)
	}
	if len(sessionIDs) > 0 {
		if _, err := deleteHistoryIDs(ctx, tx, "correlation_evidence", "observed_session_id", sessionIDs); err != nil {
			return result, coded(apperrors.StoreWriteFailed, err)
		}
		count, err := deleteHistoryIDs(ctx, tx, "observed_sessions", "observed_session_id", sessionIDs)
		if err != nil {
			return result, coded(apperrors.StoreWriteFailed, err)
		}
		result.Processed += count
		result.More = result.More || len(sessionIDs) == usage.RetentionBatchSize
	}
	changed, err := tx.ExecContext(ctx, `DELETE FROM managed_launches WHERE managed_launch_id IN (SELECT m.managed_launch_id FROM managed_launches m WHERE m.state IN ('exited', 'abandoned') AND rtrim(COALESCE(m.ended_at, m.started_at), 'Z') < ? AND NOT EXISTS (SELECT 1 FROM correlation_evidence c WHERE c.managed_launch_id = m.managed_launch_id) LIMIT ?)`, before, usage.RetentionBatchSize)
	if err != nil {
		return result, coded(apperrors.StoreWriteFailed, err)
	}
	count, _ := changed.RowsAffected()
	result.Processed += count
	result.More = result.More || count == usage.RetentionBatchSize
	if err := tx.Commit(); err != nil {
		return result, coded(apperrors.StoreWriteFailed, err)
	}
	return result, nil
}
