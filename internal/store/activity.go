package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
)

func (store *Store) ListActivitySources(ctx context.Context) ([]activity.SourceTarget, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT h.identity_home_id, a.alias, h.location_ciphertext, h.updated_at FROM identity_homes h JOIN identity_profiles p ON p.identity_home_id = h.identity_home_id JOIN cli_aliases a ON a.profile_id = p.profile_id WHERE p.status = 'ready' AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = p.profile_id) ORDER BY a.alias COLLATE NOCASE`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	defer rows.Close()
	vault, err := store.requireVault()
	if err != nil {
		return nil, err
	}
	targets := []activity.SourceTarget{}
	seenHomes := map[string]bool{}
	for rows.Next() {
		var target activity.SourceTarget
		var ciphertext []byte
		var updatedAt string
		if err := rows.Scan(&target.ID, &target.Label, &ciphertext, &updatedAt); err != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		if seenHomes[target.ID] {
			continue
		}
		seenHomes[target.ID] = true
		target.IdentityHome, err = decryptField(ctx, vault, ciphertext, identityHomeAAD(target.ID))
		if err != nil {
			return nil, err
		}
		target.IdentityHome = filepath.Clean(target.IdentityHome)
		if !filepath.IsAbs(target.IdentityHome) {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		target.ID += ":" + updatedAt
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
	}
	candidates := []activity.SourceTarget{}
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); filepath.IsAbs(configured) {
		candidates = append(candidates, activity.SourceTarget{ID: "configured", Label: "Configured Codex home", IdentityHome: filepath.Clean(configured)})
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		candidates = append(candidates, activity.SourceTarget{ID: "default", Label: "Default Codex home", IdentityHome: filepath.Join(home, ".codex")})
	}
	for _, candidate := range candidates {
		registered := false
		for _, target := range targets {
			if sameActivityHome(target.IdentityHome, candidate.IdentityHome) {
				registered = true
				break
			}
		}
		if !registered {
			targets = append(targets, candidate)
		}
	}
	return targets, nil
}

func sameActivityHome(left, right string) bool {
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = resolved
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = resolved
	}
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

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
		state, launchID, establishedProfileID, err := observedCorrelation(ctx, tx, record)
		if err != nil {
			rollback()
			return coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		observedID, err := newStoreIdentifier("observed")
		if err != nil {
			rollback()
			return coded(apperrors.StoreWriteFailed, activity.ErrActivityUnavailable)
		}
		profileID := record.ProfileID
		provenance := "legacy_profile_observation"
		if profileID == "" {
			provenance = "unassigned"
		}
		if establishedProfileID != "" {
			profileID, provenance = establishedProfileID, "managed_launch"
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO observed_sessions (
			observed_session_id, profile_id, source, started_at, ended_at, source_session_id,
			source_version, project_identity_id, last_observed_at, model, tokens_used, correlation_state, attribution_provenance
		) VALUES (?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source, source_session_id) WHERE source_session_id IS NOT NULL DO UPDATE SET
			source_version = excluded.source_version,
			profile_id = CASE WHEN excluded.attribution_provenance = 'managed_launch' THEN excluded.profile_id ELSE observed_sessions.profile_id END,
			attribution_provenance = CASE WHEN excluded.attribution_provenance = 'managed_launch' THEN excluded.attribution_provenance ELSE observed_sessions.attribution_provenance END,
			project_identity_id = COALESCE(excluded.project_identity_id, observed_sessions.project_identity_id),
			started_at = excluded.started_at,
			last_observed_at = excluded.last_observed_at,
			model = excluded.model,
			tokens_used = excluded.tokens_used,
			correlation_state = CASE WHEN excluded.attribution_provenance = 'managed_launch' THEN excluded.correlation_state ELSE observed_sessions.correlation_state END
		WHERE rtrim(excluded.last_observed_at, 'Z') >= rtrim(observed_sessions.last_observed_at, 'Z')
		RETURNING observed_session_id`, observedID, nullableString(profileID), record.Source, formatStoredTime(record.StartedAt.UTC()),
			record.SourceSessionID, record.SourceVersion, nullableString(record.ProjectID), formatStoredTime(record.LastObservedAt.UTC()),
			nullableString(record.Model), record.TokensUsed, state, provenance).Scan(&observedID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
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

func observedCorrelation(ctx context.Context, tx *sql.Tx, record activity.ObservedSessionRecord) (string, string, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT managed_launch_id, profile_id FROM managed_launches WHERE expected_session_id = ? AND state IN ('running', 'exited')`, record.SourceSessionID)
	if err != nil {
		return "", "", "", err
	}
	defer rows.Close()
	sameProfile := []string{}
	var establishedProfileID string
	conflictingProfile := false
	for rows.Next() {
		var launchID, profileID string
		if err := rows.Scan(&launchID, &profileID); err != nil {
			return "", "", "", err
		}
		if record.ProfileID == "" || profileID == record.ProfileID {
			sameProfile = append(sameProfile, launchID)
			if establishedProfileID != "" && establishedProfileID != profileID {
				conflictingProfile = true
			}
			establishedProfileID = profileID
		} else {
			conflictingProfile = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", "", err
	}
	if conflictingProfile {
		return activity.CorrelationContradictory, "", "", nil
	}
	if len(sameProfile) == 1 {
		return activity.CorrelationCorrelated, sameProfile[0], establishedProfileID, nil
	}
	if len(sameProfile) > 1 {
		return activity.CorrelationAmbiguous, "", "", nil
	}
	return activity.CorrelationUncorrelated, "", "", nil
}

// AssignSessions changes only the user override. Source identity and the
// evidence-established profile remain attached to the observed row.
func (store *Store) AssignSessions(ctx context.Context, ids []string, profileID string) error {
	if store == nil || store.db == nil || len(ids) == 0 || len(ids) > 100 {
		return apperrors.New(apperrors.ActivityRequestInvalid, activity.ErrActivityInvalid)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	defer tx.Rollback()
	if profileID != "" {
		var ready int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM identity_profiles p WHERE p.profile_id = ? AND p.status = 'ready' AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = p.profile_id)`, profileID).Scan(&ready)
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.New(apperrors.ActivityRequestInvalid, activity.ErrActivityInvalid)
		}
		if err != nil {
			return coded(apperrors.StoreReadFailed, err)
		}
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || seen[id] {
			return apperrors.New(apperrors.ActivityRequestInvalid, activity.ErrActivityInvalid)
		}
		seen[id] = true
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM observed_sessions WHERE observed_session_id = ? AND source = 'local_metadata'`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.New(apperrors.ActivityRequestInvalid, activity.ErrActivityInvalid)
		}
		if err != nil {
			return coded(apperrors.StoreReadFailed, err)
		}
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO observed_session_assignments (observed_session_id, profile_id, assigned_at) VALUES (?, ?, ?) ON CONFLICT(observed_session_id) DO UPDATE SET profile_id = excluded.profile_id, assigned_at = excluded.assigned_at`, id, nullableString(profileID), formatStoredTime(store.clock.Now().UTC())); err != nil {
			return coded(apperrors.StoreWriteFailed, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	return nil
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
	policy, err := readAnalyticsRetention(ctx, store.db)
	if err != nil {
		return nil, err
	}
	if cutoff := policy.Cutoff(store.clock.Now()); cutoff != nil {
		kept := records[:0]
		for _, record := range records {
			if record.RecordType != activity.RecordTypeObservedSession || !record.LastObservedAt.Before(*cutoff) {
				kept = append(kept, record)
			}
		}
		records = kept
	}
	sort.SliceStable(records, func(left, right int) bool {
		if records[left].StartedAt.Equal(records[right].StartedAt) {
			return records[left].RecordType == activity.RecordTypeObservedSession && records[right].RecordType != activity.RecordTypeObservedSession
		}
		return records[left].StartedAt.After(records[right].StartedAt)
	})
	return records, nil
}

func (store *Store) listObservedSessions(ctx context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT os.observed_session_id, os.source_session_id,
		COALESCE(CASE WHEN assign.observed_session_id IS NOT NULL THEN assign.profile_id ELSE os.profile_id END, ''),
		COALESCE(a.alias, CASE WHEN assign.observed_session_id IS NOT NULL THEN assign.profile_id ELSE os.profile_id END, 'deregistered'),
		COALESCE(p.project_identity_id, ''), COALESCE(p.project_alias, ''), COALESCE(p.repository_basename, ''),
		CASE WHEN assign.observed_session_id IS NOT NULL THEN 'user_assigned' ELSE os.attribution_provenance END,
		COALESCE(os.profile_id, ''), os.attribution_provenance,
		os.source, os.source_version, os.started_at, os.last_observed_at, COALESCE(os.model, ''), os.tokens_used,
		os.correlation_state, ce.managed_launch_id, ce.evidence_type, ce.confidence
		FROM observed_sessions os
		LEFT JOIN observed_session_assignments assign ON assign.observed_session_id = os.observed_session_id
		LEFT JOIN cli_aliases a ON a.profile_id = CASE WHEN assign.observed_session_id IS NOT NULL THEN assign.profile_id ELSE os.profile_id END
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
			&record.ProjectID, &record.ProjectAlias, &record.ProjectBasename, &record.AttributionProvenance, &record.OriginalProfileID, &record.OriginalAttributionProvenance, &record.Source, &record.SourceVersion,
			&startedAt, &lastObservedAt, &record.Model, &tokens, &record.Correlation.State,
			&managedLaunchID, &evidenceType, &confidence); err != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		record.RecordType, record.Provenance = activity.RecordTypeObservedSession, activity.ProvenanceObservedSession
		if record.ProfileID == "" {
			record.ProfileAlias = "Unassigned History"
		}
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
		ml.state, ml.continuation_checkpoint_id, ml.continuation_revision,
		ml.started_at, ml.ended_at, ml.exit_status, ce.observed_session_id, ce.evidence_type, ce.confidence
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
		var checkpointID, revision, observedID, evidenceType, confidence sql.NullString
		if err := rows.Scan(&record.ID, &record.ProfileID, &record.ProfileAlias, &record.ProjectID, &record.ProjectAlias,
			&record.ProjectBasename, &record.Lifecycle, &checkpointID, &revision, &startedAt, &endedAt, &exitStatus,
			&observedID, &evidenceType, &confidence); err != nil {
			return nil, coded(apperrors.StoreReadFailed, activity.ErrActivityUnavailable)
		}
		record.RecordType, record.Source, record.Provenance = activity.RecordTypeManagedLaunch, "codex_folio", activity.ProvenanceManagedLaunch
		record.ContinuationCheckpointID, record.ContinuationRevision = checkpointID.String, revision.String
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
	return strings.TrimSpace(record.SourceSessionID) != "" && record.Source == activity.SourceLocalMetadata &&
		strings.TrimSpace(record.SourceVersion) != "" && !record.StartedAt.IsZero() && !record.LastObservedAt.Before(record.StartedAt) &&
		(record.TokensUsed == nil || *record.TokensUsed >= 0)
}

var _ activity.Repository = (*Store)(nil)
