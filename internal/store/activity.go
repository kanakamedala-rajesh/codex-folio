package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
)

func (store *Store) ResolveActivityProfile(ctx context.Context, alias string) (activity.ProfileTarget, error) {
	target, err := store.ResolveUsageProfile(ctx, alias)
	if err != nil {
		return activity.ProfileTarget{}, err
	}
	return activity.ProfileTarget{ID: target.ID, Alias: target.Alias, IdentityHome: target.IdentityHome}, nil
}

func (store *Store) SaveObservedSessions(ctx context.Context, records []activity.ObservedSessionRecord) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
	}
	rollback := func() { _ = tx.Rollback() }
	for _, record := range records {
		if !validObservedSessionRecord(record) {
			rollback()
			return apperrors.New(apperrors.StoreWriteFailed, activity.ErrActivityInvalid)
		}
		state, launchID, err := observedCorrelation(ctx, tx, record)
		if err != nil {
			rollback()
			return coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		observedID, err := newStoreIdentifier("observed")
		if err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO observed_sessions (
			observed_session_id, profile_id, source, started_at, ended_at, source_session_id,
			source_version, project_identity_id, last_observed_at, model, tokens_used, correlation_state
		) VALUES (?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, source, source_session_id) WHERE source_session_id IS NOT NULL DO UPDATE SET
			source_version = excluded.source_version,
			project_identity_id = excluded.project_identity_id,
			started_at = excluded.started_at,
			last_observed_at = excluded.last_observed_at,
			model = excluded.model,
			tokens_used = excluded.tokens_used,
			correlation_state = excluded.correlation_state
		RETURNING observed_session_id`, observedID, record.ProfileID, record.Source, formatStoredTime(record.StartedAt.UTC()),
			record.SourceSessionID, record.SourceVersion, nullableString(record.ProjectID), formatStoredTime(record.LastObservedAt.UTC()),
			nullableString(record.Model), record.TokensUsed, state).Scan(&observedID)
		if err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM correlation_evidence WHERE observed_session_id = ?`, observedID); err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
		}
		if launchID != "" {
			evidenceID, idErr := newStoreIdentifier("correlation")
			if idErr != nil {
				rollback()
				return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO correlation_evidence (correlation_evidence_id, managed_launch_id, observed_session_id, confidence, evidence_type, observed_at) VALUES (?, ?, ?, 'high', 'explicit', ?)`, evidenceID, launchID, observedID, formatStoredTime(record.LastObservedAt.UTC())); err != nil {
				rollback()
				return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
	}
	return nil
}

func observedCorrelation(ctx context.Context, tx *sql.Tx, record activity.ObservedSessionRecord) (string, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT managed_launch_id, profile_id FROM managed_launches WHERE expected_session_id = ?`, record.SourceSessionID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	sameProfile := []string{}
	conflictingProfile := false
	for rows.Next() {
		var launchID, profileID string
		if err := rows.Scan(&launchID, &profileID); err != nil {
			return "", "", err
		}
		if profileID == record.ProfileID {
			sameProfile = append(sameProfile, launchID)
		} else {
			conflictingProfile = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if conflictingProfile {
		return activity.CorrelationContradictory, "", nil
	}
	if len(sameProfile) == 1 {
		return activity.CorrelationCorrelated, sameProfile[0], nil
	}
	if len(sameProfile) > 1 {
		return activity.CorrelationAmbiguous, "", nil
	}
	return activity.CorrelationUncorrelated, "", nil
}

func (store *Store) ListActivity(ctx context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	records, err := store.listObservedSessions(ctx, filters)
	if err != nil {
		return nil, err
	}
	launches, err := store.listManagedLaunches(ctx, filters)
	if err != nil {
		return nil, err
	}
	records = append(records, launches...)
	sort.SliceStable(records, func(left, right int) bool {
		if records[left].StartedAt.Equal(records[right].StartedAt) {
			return records[left].RecordType == activity.RecordTypeObservedSession && records[right].RecordType != activity.RecordTypeObservedSession
		}
		return records[left].StartedAt.After(records[right].StartedAt)
	})
	return records, nil
}

func (store *Store) listObservedSessions(ctx context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT os.observed_session_id, os.source_session_id, os.profile_id, a.alias,
		COALESCE(p.project_identity_id, ''), COALESCE(p.project_alias, ''), COALESCE(p.repository_basename, ''),
		os.source, os.source_version, os.started_at, os.last_observed_at, COALESCE(os.model, ''), os.tokens_used,
		os.correlation_state, ce.managed_launch_id, ce.evidence_type, ce.confidence
		FROM observed_sessions os
		JOIN cli_aliases a ON a.profile_id = os.profile_id
		LEFT JOIN project_identities p ON p.project_identity_id = os.project_identity_id
		LEFT JOIN correlation_evidence ce ON ce.observed_session_id = os.observed_session_id
		WHERE (? = '' OR a.alias = ? COLLATE NOCASE) AND (? = '' OR os.project_identity_id = ?)`,
		filters.ProfileAlias, filters.ProfileAlias, filters.ProjectID, filters.ProjectID)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	defer rows.Close()
	records := []activity.TimelineRecord{}
	for rows.Next() {
		var record activity.TimelineRecord
		var startedAt, lastObservedAt string
		var tokens sql.NullInt64
		var managedLaunchID, evidenceType, confidence sql.NullString
		if err := rows.Scan(&record.ID, &record.SourceSessionID, &record.ProfileID, &record.ProfileAlias,
			&record.ProjectID, &record.ProjectAlias, &record.ProjectBasename, &record.Source, &record.SourceVersion,
			&startedAt, &lastObservedAt, &record.Model, &tokens, &record.Correlation.State,
			&managedLaunchID, &evidenceType, &confidence); err != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		record.RecordType, record.Provenance = activity.RecordTypeObservedSession, activity.ProvenanceObservedSession
		var parseErr error
		record.StartedAt, parseErr = parseStoredTime(startedAt)
		if parseErr == nil {
			record.LastObservedAt, parseErr = parseStoredTime(lastObservedAt)
		}
		if parseErr != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		if tokens.Valid {
			value := tokens.Int64
			record.TokensUsed = &value
		}
		if managedLaunchID.Valid {
			record.Correlation.ManagedLaunchID, record.Correlation.EvidenceType, record.Correlation.Confidence = managedLaunchID.String, evidenceType.String, confidence.String
		}
		records = append(records, record)
	}
	if rows.Err() != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	return records, nil
}

func (store *Store) listManagedLaunches(ctx context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT ml.managed_launch_id, ml.profile_id, a.alias,
		COALESCE(p.project_identity_id, ''), COALESCE(p.project_alias, ''), COALESCE(p.repository_basename, ''),
		ml.state, ml.started_at, ml.ended_at, ml.exit_status, ce.observed_session_id, ce.evidence_type, ce.confidence
		FROM managed_launches ml
		JOIN cli_aliases a ON a.profile_id = ml.profile_id
		LEFT JOIN project_identities p ON p.project_identity_id = ml.project_identity_id
		LEFT JOIN correlation_evidence ce ON ce.managed_launch_id = ml.managed_launch_id
		WHERE (? = '' OR a.alias = ? COLLATE NOCASE) AND (? = '' OR ml.project_identity_id = ?)`,
		filters.ProfileAlias, filters.ProfileAlias, filters.ProjectID, filters.ProjectID)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	defer rows.Close()
	records := []activity.TimelineRecord{}
	for rows.Next() {
		var record activity.TimelineRecord
		var startedAt string
		var endedAt sql.NullString
		var exitStatus sql.NullInt64
		var observedID, evidenceType, confidence sql.NullString
		if err := rows.Scan(&record.ID, &record.ProfileID, &record.ProfileAlias, &record.ProjectID, &record.ProjectAlias,
			&record.ProjectBasename, &record.Lifecycle, &startedAt, &endedAt, &exitStatus, &observedID, &evidenceType, &confidence); err != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		record.RecordType, record.Source, record.Provenance = activity.RecordTypeManagedLaunch, "codex_folio", activity.ProvenanceManagedLaunch
		record.Correlation.State = activity.CorrelationUncorrelated
		var parseErr error
		record.StartedAt, parseErr = parseStoredTime(startedAt)
		record.LastObservedAt = record.StartedAt
		if endedAt.Valid && parseErr == nil {
			record.LastObservedAt, parseErr = parseStoredTime(endedAt.String)
		}
		if parseErr != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		if exitStatus.Valid {
			value := int(exitStatus.Int64)
			record.ExitStatus = &value
		}
		if observedID.Valid {
			record.Correlation = activity.Correlation{State: activity.CorrelationCorrelated, EvidenceType: evidenceType.String, Confidence: confidence.String}
		}
		records = append(records, record)
	}
	if rows.Err() != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	return records, nil
}

func validObservedSessionRecord(record activity.ObservedSessionRecord) bool {
	return strings.TrimSpace(record.SourceSessionID) != "" && strings.TrimSpace(record.ProfileID) != "" &&
		strings.TrimSpace(record.ProfileAlias) != "" && record.Source == activity.SourceLocalMetadata &&
		strings.TrimSpace(record.SourceVersion) != "" && !record.StartedAt.IsZero() && !record.LastObservedAt.Before(record.StartedAt) &&
		(record.TokensUsed == nil || *record.TokensUsed >= 0)
}

var _ activity.Repository = (*Store)(nil)
