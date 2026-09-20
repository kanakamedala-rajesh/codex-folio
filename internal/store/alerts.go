package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/alerts"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (store *Store) AlertEvidence(ctx context.Context, profileID string) ([]alerts.Evidence, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	query := `SELECT p.profile_id, a.alias, p.status FROM identity_profiles p JOIN cli_aliases a ON a.profile_id = p.profile_id
		WHERE NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = p.profile_id)`
	args := []any{}
	if profileID != "" {
		query += " AND p.profile_id = ?"
		args = append(args, profileID)
	}
	query += " ORDER BY lower(a.alias), p.profile_id"
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		store.operationMu.RUnlock()
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	type target struct{ id, alias, status string }
	var targets []target
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.id, &item.alias, &item.status); err != nil {
			break
		}
		targets = append(targets, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	store.operationMu.RUnlock()
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	result := make([]alerts.Evidence, 0, len(targets))
	for _, target := range targets {
		item := alerts.Evidence{ProfileID: target.id, Alias: target.alias, ProfileStatus: target.status}
		snapshots, snapshotErr := store.RecentUsageSnapshots(ctx, usage.ProfileTarget{ID: target.id, Alias: target.alias})
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		if len(snapshots) > 0 {
			item.Snapshot = snapshots[len(snapshots)-1]
			currentUseful := -1
			for index := len(snapshots) - 1; index >= 0; index-- {
				if len(snapshots[index].Observations) > 0 || snapshotRequiresReauthentication(snapshots[index]) {
					item.Snapshot = snapshots[index]
					currentUseful = index
					break
				}
			}
			for index := len(snapshots) - 1; index >= 0 && snapshotCollectionFailed(snapshots[index]); index-- {
				item.ConsecutiveFailures++
			}
			if currentUseful >= 0 {
				for index := currentUseful - 1; index >= 0; index-- {
					if len(snapshots[index].Observations) > 0 || snapshotRequiresReauthentication(snapshots[index]) {
						item.PreviousSourceVersion = snapshots[index].SourceVersion
						break
					}
				}
			}
		}
		store.operationMu.RLock()
		var scheduledFailures int
		err = store.db.QueryRowContext(ctx, `SELECT COALESCE(consecutive_failures, 0) FROM (SELECT 1) LEFT JOIN collection_schedule_state ON profile_id = ?`, target.id).Scan(&scheduledFailures)
		store.operationMu.RUnlock()
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		if scheduledFailures > item.ConsecutiveFailures {
			item.ConsecutiveFailures = scheduledFailures
		}
		result = append(result, item)
	}
	return result, nil
}

func snapshotCollectionFailed(snapshot usage.Snapshot) bool {
	if snapshot.Status != usage.AvailabilityTemporarilyUnavailable {
		return false
	}
	for _, availability := range snapshot.Availability {
		if availability.Reason == usage.ReasonCollectionFailed || availability.Reason == usage.ReasonMalformedSource {
			return true
		}
	}
	return false
}

func snapshotRequiresReauthentication(snapshot usage.Snapshot) bool {
	for _, availability := range snapshot.Availability {
		if availability.State == usage.AvailabilityReauthenticationRequired {
			return true
		}
	}
	return false
}

func (store *Store) AlertThresholds(ctx context.Context) ([]alerts.Threshold, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT profile_id, metric_key, warning_percent, critical_percent FROM alert_thresholds ORDER BY profile_id, metric_key`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	result := []alerts.Threshold{}
	for rows.Next() {
		var item alerts.Threshold
		if err := rows.Scan(&item.ProfileID, &item.MetricKey, &item.WarningPercent, &item.CriticalPercent); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

func (store *Store) SetAlertThreshold(ctx context.Context, threshold alerts.Threshold, now time.Time) error {
	if store == nil || store.db == nil || now.IsZero() {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(ctx, `INSERT INTO alert_thresholds (profile_id, metric_key, warning_percent, critical_percent, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(profile_id, metric_key) DO UPDATE SET warning_percent = excluded.warning_percent,
		critical_percent = excluded.critical_percent, updated_at = excluded.updated_at`, threshold.ProfileID, threshold.MetricKey, threshold.WarningPercent, threshold.CriticalPercent, formatStoredTime(now))
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	return nil
}

func (store *Store) SyncAlerts(ctx context.Context, profileID string, conditions []alerts.Condition, now time.Time, historyLimit int) error {
	if store == nil || store.db == nil || now.IsZero() || historyLimit < 1 {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	rollback := func() { _ = tx.Rollback() }
	active := make(map[string]bool, len(conditions))
	for _, condition := range conditions {
		active[condition.Key] = true
		var existingID, state string
		var previousEvidence sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT alert_id, state, evidence_captured_at FROM alerts WHERE condition_key = ?`, condition.Key).Scan(&existingID, &state, &previousEvidence)
		if errors.Is(err, sql.ErrNoRows) {
			existingID, err = newStoreIdentifier("alert")
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO alerts (alert_id, condition_key, profile_id, category, kind, severity, state, title, guidance, metric_key, window_start, window_end, remaining_percent, source, source_version, provenance, scope, freshness, availability_reason, evidence_captured_at, observed_at, first_seen_at, last_seen_at, occurrence_count)
					VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, existingID, condition.Key, nullableAlertProfile(condition.ProfileID), condition.Category, condition.Kind, condition.Severity, condition.Title, condition.Guidance, condition.MetricKey, nullableAlertTime(condition.WindowStart), nullableAlertTime(condition.WindowEnd), condition.RemainingPercent, condition.Source, condition.SourceVersion, condition.Provenance, condition.Scope, condition.Freshness, condition.AvailabilityReason, nullableAlertTimeValue(condition.EvidenceCapturedAt), formatStoredTime(condition.ObservedAt), formatStoredTime(now), formatStoredTime(now))
			}
		} else if err == nil {
			incomingEvidence := nullableAlertTimeValue(condition.EvidenceCapturedAt)
			incomingEvidenceText, incomingEvidenceValid := incomingEvidence.(string)
			newEvidence := state == alerts.StateResolved || previousEvidence.Valid != incomingEvidenceValid || (previousEvidence.Valid && previousEvidence.String != incomingEvidenceText)
			increment := 0
			if newEvidence {
				increment = 1
			}
			_, err = tx.ExecContext(ctx, `UPDATE alerts SET category = ?, kind = ?, severity = ?, title = ?, guidance = ?, metric_key = ?, window_start = ?, window_end = ?, remaining_percent = ?, source = ?, source_version = ?, provenance = ?, scope = ?, freshness = ?, availability_reason = ?, evidence_captured_at = ?, observed_at = ?,
				last_seen_at = CASE WHEN ? = 1 THEN ? ELSE last_seen_at END, occurrence_count = occurrence_count + ?, state = CASE WHEN state = 'resolved' THEN 'open' ELSE state END,
				acknowledged_at = CASE WHEN state = 'resolved' THEN NULL ELSE acknowledged_at END, resolved_at = NULL,
				delivery_state = CASE WHEN state = 'resolved' THEN 'pending' ELSE delivery_state END,
				delivery_attempts = CASE WHEN state = 'resolved' THEN 0 ELSE delivery_attempts END,
				last_delivery_attempt_at = CASE WHEN state = 'resolved' THEN NULL ELSE last_delivery_attempt_at END,
				next_delivery_attempt_at = CASE WHEN state = 'resolved' THEN NULL ELSE next_delivery_attempt_at END,
				delivered_at = CASE WHEN state = 'resolved' THEN NULL ELSE delivered_at END,
				delivery_error_code = CASE WHEN state = 'resolved' THEN '' ELSE delivery_error_code END WHERE alert_id = ?`,
				condition.Category, condition.Kind, condition.Severity, condition.Title, condition.Guidance, condition.MetricKey, nullableAlertTime(condition.WindowStart), nullableAlertTime(condition.WindowEnd), condition.RemainingPercent, condition.Source, condition.SourceVersion, condition.Provenance, condition.Scope, condition.Freshness, condition.AvailabilityReason, incomingEvidence, formatStoredTime(condition.ObservedAt), increment, formatStoredTime(now), increment, existingID)
		}
		if err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, err)
		}
	}
	query := `SELECT alert_id, condition_key FROM alerts WHERE state IN ('open', 'acknowledged')`
	args := []any{}
	if profileID != "" {
		query += " AND profile_id = ?"
		args = append(args, profileID)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, err)
	}
	type unresolved struct{ id, key string }
	var absent []unresolved
	for rows.Next() {
		var item unresolved
		if err = rows.Scan(&item.id, &item.key); err == nil && !active[item.key] {
			absent = append(absent, item)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, err)
	}
	for _, item := range absent {
		if _, err = tx.ExecContext(ctx, `UPDATE alerts SET state = 'resolved', resolved_at = ? WHERE alert_id = ?`, formatStoredTime(now), item.id); err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM alerts WHERE state = 'resolved' AND alert_id IN (
		SELECT alert_id FROM alerts WHERE state = 'resolved' ORDER BY rtrim(last_seen_at, 'Z') DESC, alert_id DESC LIMIT -1 OFFSET ?
	)`, historyLimit); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, err)
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, err)
	}
	return nil
}

func (store *Store) AcknowledgeAlert(ctx context.Context, id string, now time.Time) error {
	if store == nil || store.db == nil || id == "" || now.IsZero() {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE alerts SET state = 'acknowledged', acknowledged_at = ? WHERE alert_id = ? AND state = 'open'`, formatStoredTime(now), id)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	return nil
}

func (store *Store) ListAlerts(ctx context.Context, limit int) ([]alerts.Record, error) {
	if store == nil || store.db == nil || limit < 1 {
		return nil, apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT a.alert_id, a.condition_key, COALESCE(a.profile_id, ''), COALESCE(c.alias, ''), a.category, a.kind, a.severity, a.state,
		a.title, a.guidance, a.metric_key, a.window_start, a.window_end, a.remaining_percent, a.source, a.source_version, a.provenance, a.scope, a.freshness, a.availability_reason, a.evidence_captured_at, a.observed_at, a.first_seen_at, a.last_seen_at, a.acknowledged_at, a.resolved_at, a.occurrence_count
		, a.delivery_state, a.delivery_attempts, a.last_delivery_attempt_at, a.next_delivery_attempt_at, a.delivered_at, a.delivery_error_code
		FROM alerts a LEFT JOIN cli_aliases c ON c.profile_id = a.profile_id
		WHERE a.state <> 'resolved' OR a.alert_id IN (
			SELECT alert_id FROM alerts WHERE state = 'resolved' ORDER BY rtrim(last_seen_at, 'Z') DESC, alert_id DESC LIMIT ?
		)
		ORDER BY CASE a.state WHEN 'open' THEN 0 WHEN 'acknowledged' THEN 1 ELSE 2 END, rtrim(a.last_seen_at, 'Z') DESC, a.alert_id DESC`, limit)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	result := []alerts.Record{}
	for rows.Next() {
		var item alerts.Record
		var start, end, evidenceCaptured, acknowledged, resolved, lastDeliveryAttempt, nextDeliveryAttempt, delivered sql.NullString
		var remaining sql.NullFloat64
		var observed, firstSeen, lastSeen string
		if err := rows.Scan(&item.ID, &item.Key, &item.ProfileID, &item.ProfileAlias, &item.Category, &item.Kind, &item.Severity, &item.State, &item.Title, &item.Guidance, &item.MetricKey, &start, &end, &remaining, &item.Source, &item.SourceVersion, &item.Provenance, &item.Scope, &item.Freshness, &item.AvailabilityReason, &evidenceCaptured, &observed, &firstSeen, &lastSeen, &acknowledged, &resolved, &item.OccurrenceCount, &item.DeliveryState, &item.DeliveryAttempts, &lastDeliveryAttempt, &nextDeliveryAttempt, &delivered, &item.DeliveryErrorCode); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		item.FirstSeenAt, err = parseStoredTime(firstSeen)
		if err == nil {
			item.LastSeenAt, err = parseStoredTime(lastSeen)
		}
		if err == nil {
			item.EvidenceCapturedAt, err = parseOptionalAlertTimeValue(evidenceCaptured)
		}
		if err == nil {
			item.ObservedAt, err = parseStoredTime(observed)
		}
		if err == nil {
			item.WindowStart, err = parseOptionalAlertTime(start)
		}
		if err == nil {
			item.WindowEnd, err = parseOptionalAlertTime(end)
		}
		if err == nil {
			item.AcknowledgedAt, err = parseOptionalAlertTime(acknowledged)
		}
		if err == nil {
			item.ResolvedAt, err = parseOptionalAlertTime(resolved)
		}
		if err == nil {
			item.LastDeliveryAttemptAt, err = parseOptionalAlertTime(lastDeliveryAttempt)
		}
		if err == nil {
			item.NextDeliveryAttemptAt, err = parseOptionalAlertTime(nextDeliveryAttempt)
		}
		if err == nil {
			item.DeliveredAt, err = parseOptionalAlertTime(delivered)
		}
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		if remaining.Valid {
			item.RemainingPercent = &remaining.Float64
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

func (store *Store) NotificationPreference(ctx context.Context) (alerts.NotificationPreference, error) {
	if store == nil || store.db == nil {
		return alerts.NotificationPreference{}, coded(apperrors.StoreReadFailed, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var enabled int
	var updated string
	err := store.db.QueryRowContext(ctx, `SELECT notification_detail_enabled, updated_at FROM settings WHERE settings_id = 1`).Scan(&enabled, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return alerts.NotificationPreference{}, nil
	}
	if err != nil {
		return alerts.NotificationPreference{}, coded(apperrors.StoreReadFailed, err)
	}
	parsed, err := parseStoredTime(updated)
	if err != nil {
		return alerts.NotificationPreference{}, coded(apperrors.StoreReadFailed, err)
	}
	return alerts.NotificationPreference{DetailEnabled: enabled == 1, UpdatedAt: parsed}, nil
}

func (store *Store) SetNotificationDetail(ctx context.Context, enabled bool, now time.Time) (alerts.NotificationPreference, error) {
	if store == nil || store.db == nil || now.IsZero() {
		return alerts.NotificationPreference{}, apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	value := 0
	if enabled {
		value = 1
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	_, err := store.db.ExecContext(ctx, `INSERT INTO settings (settings_id, notification_detail_enabled, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(settings_id) DO UPDATE SET notification_detail_enabled = excluded.notification_detail_enabled, updated_at = excluded.updated_at`, value, formatStoredTime(now))
	if err != nil {
		return alerts.NotificationPreference{}, coded(apperrors.StoreWriteFailed, err)
	}
	return alerts.NotificationPreference{DetailEnabled: enabled, UpdatedAt: now.UTC()}, nil
}

func (store *Store) ClaimAlertDelivery(ctx context.Context, id string, now time.Time) (bool, error) {
	if store == nil || store.db == nil || id == "" || now.IsZero() {
		return false, apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	result, err := store.db.ExecContext(ctx, `UPDATE alerts SET delivery_state = 'attempting', delivery_attempts = delivery_attempts + 1,
		last_delivery_attempt_at = ?, next_delivery_attempt_at = NULL, delivery_error_code = ''
		WHERE alert_id = ? AND state = 'open' AND delivery_state IN ('pending', 'failed', 'unavailable') AND delivery_attempts < ?
		AND (next_delivery_attempt_at IS NULL OR rtrim(next_delivery_attempt_at, 'Z') <= rtrim(?, 'Z'))`,
		formatStoredTime(now), id, alerts.DeliveryMaxAttempts, formatStoredTime(now))
	if err != nil {
		return false, coded(apperrors.StoreWriteFailed, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, coded(apperrors.StoreWriteFailed, err)
	}
	return updated == 1, nil
}

func (store *Store) RecordAlertDelivery(ctx context.Context, id string, outcome alerts.DeliveryOutcome) error {
	if store == nil || store.db == nil || id == "" || outcome.AttemptedAt.IsZero() || outcome.Attempts < 0 || outcome.Attempts > alerts.DeliveryMaxAttempts || (outcome.State != alerts.DeliveryDelivered && outcome.State != alerts.DeliveryFailed && outcome.State != alerts.DeliveryUnavailable) {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	query := `UPDATE alerts SET delivery_state = ?, delivery_attempts = ?, last_delivery_attempt_at = ?, next_delivery_attempt_at = ?, delivered_at = ?, delivery_error_code = ? WHERE alert_id = ? AND state = 'open'`
	args := []any{outcome.State, outcome.Attempts, formatStoredTime(outcome.AttemptedAt), nullableAlertTime(outcome.NextAttemptAt), nullableAlertTime(outcome.DeliveredAt), outcome.ErrorCode, id}
	if outcome.State == alerts.DeliveryUnavailable {
		query += ` AND delivery_state IN ('pending', 'failed', 'unavailable')`
	} else {
		query += ` AND delivery_state = 'attempting' AND delivery_attempts = ?`
		args = append(args, outcome.Attempts)
	}
	result, err := store.db.ExecContext(ctx, query, args...)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return apperrors.New(apperrors.UsageRequestInvalid, alerts.ErrInvalid)
	}
	return nil
}

func nullableAlertProfile(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAlertTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatStoredTime(value.UTC())
}

func nullableAlertTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatStoredTime(value.UTC())
}

func parseOptionalAlertTimeValue(value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, nil
	}
	return parseStoredTime(value.String)
}

func parseOptionalAlertTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseStoredTime(value.String)
	return &parsed, err
}

var _ alerts.Repository = (*Store)(nil)
