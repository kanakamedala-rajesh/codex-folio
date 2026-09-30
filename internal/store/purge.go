package store

import (
	"context"
	"slices"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

type historySelection struct {
	table, column, query string
	args                 []any
}

func (store *Store) PurgeAnalytics(ctx context.Context, scope usage.HistoryScope, confirmation string) (usage.PurgeResult, error) {
	if err := scope.Validate(); err != nil {
		return usage.PurgeResult{}, apperrors.New(apperrors.AnalyticsRequestInvalid, err)
	}
	result := usage.PurgeResult{Scope: scope, Counts: []usage.RecordCount{}, RecordLimit: usage.PurgeRecordLimit, Executable: true}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, coded(apperrors.StoreWriteFailed, err)
	}
	defer tx.Rollback()
	selections := purgeSelections(scope)
	ids := make([][]string, len(selections))
	identities := make([]usage.PurgeSelectionIdentity, len(selections))
	var total int64
	for index, selection := range selections {
		ids[index], err = historyIDs(ctx, tx, selection.query, selection.args)
		if err != nil {
			return result, coded(apperrors.StoreReadFailed, err)
		}
		count := int64(len(ids[index]))
		result.Counts = append(result.Counts, usage.RecordCount{RecordClass: selection.table, Count: count})
		identities[index] = usage.PurgeSelectionIdentity{RecordClass: selection.table, RecordIDs: ids[index]}
		total += count
	}
	result.Confirmation = scope.ConfirmationFor(identities)
	result.Executable = total <= usage.PurgeRecordLimit
	if confirmation == "" {
		return result, nil
	}
	if confirmation != result.Confirmation {
		return result, apperrors.New(apperrors.AnalyticsConfirmationInvalid, usage.ErrInvalid)
	}
	// ponytail: cap the entire atomic purge at 1000 affected records; add a
	// durable resumable job only if users need scopes that cannot be narrowed.
	if !result.Executable {
		return result, apperrors.New(apperrors.AnalyticsScopeTooLarge, usage.ErrInvalid)
	}
	// Materialize every selected ID before mutation so deletion cannot broaden
	// or shrink a dependent query. All classes commit or roll back together.
	for i, selection := range selections {
		if len(ids[i]) == 0 {
			continue
		}
		if selection.table == "correlation_updates" {
			args := make([]any, len(ids[i]))
			for j, id := range ids[i] {
				args[j] = id
			}
			_, err = tx.ExecContext(ctx, `UPDATE observed_sessions SET correlation_state = 'uncorrelated' WHERE observed_session_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
		} else {
			if selection.table == "checkpoints" {
				args := make([]any, len(ids[i]))
				for j, id := range ids[i] {
					args[j] = id
				}
				_, err = tx.ExecContext(ctx, `UPDATE managed_launches SET continuation_checkpoint_id = NULL, continuation_revision = NULL WHERE continuation_checkpoint_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
				if err != nil {
					return result, coded(apperrors.StoreWriteFailed, err)
				}
			}
			_, err = deleteHistoryIDs(ctx, tx, selection.table, selection.column, ids[i])
		}
		if err != nil {
			return result, coded(apperrors.StoreWriteFailed, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return result, coded(apperrors.StoreWriteFailed, err)
	}
	result.Applied = true
	return result, nil
}

func purgeSelections(scope usage.HistoryScope) []historySelection {
	selectClass := func(class, table, id, alias, profile, project, start, end string, bucket bool) historySelection {
		where, args := historyWhere(scope, profile, project, start, end, bucket)
		if !slices.Contains(scope.Classes, class) {
			where, args = "0", nil
		}
		return historySelection{table: table, column: id, query: "SELECT " + alias + "." + id + " FROM " + table + " " + alias + " WHERE " + where, args: args}
	}
	observations := selectClass("usage", "usage_observations", "observation_id", "o", "o.profile_id", "NULL", "o.observed_at", "o.observed_at", false)
	snapshots := selectClass("usage", "usage_snapshots", "snapshot_id", "s", "s.profile_id", "NULL", "s.captured_at", "s.captured_at", false)
	snapshots.query += ` AND NOT EXISTS (SELECT 1 FROM usage_observations x WHERE x.snapshot_id = s.snapshot_id AND x.observation_id NOT IN (` + observations.query + `))`
	snapshots.args = append(snapshots.args, observations.args...)
	availability := selectClass("usage", "metric_availability", "metric_availability_id", "a", "a.profile_id", "NULL", "a.checked_at", "a.checked_at", false)
	availability.query += ` AND NOT EXISTS (SELECT 1 FROM usage_observations x WHERE x.metric_availability_id = a.metric_availability_id AND x.observation_id NOT IN (` + observations.query + `))
 AND NOT EXISTS (SELECT 1 FROM usage_snapshots s JOIN metric_provenance p ON p.provenance_id = a.provenance_id WHERE s.profile_id = a.profile_id AND s.source = p.source AND s.source_version = COALESCE(p.source_version, '') AND s.captured_at = p.captured_at AND s.snapshot_id NOT IN (` + snapshots.query + `))`
	availability.args = append(availability.args, observations.args...)
	availability.args = append(availability.args, snapshots.args...)
	provenance := historySelection{table: "metric_provenance", column: "provenance_id", query: `SELECT p.provenance_id FROM metric_provenance p WHERE
 (p.provenance_id IN (SELECT x.provenance_id FROM usage_observations x WHERE x.observation_id IN (` + observations.query + `)) OR
 p.provenance_id IN (SELECT x.provenance_id FROM metric_availability x WHERE x.metric_availability_id IN (` + availability.query + `)))
 AND NOT EXISTS (SELECT 1 FROM usage_observations x WHERE x.provenance_id = p.provenance_id AND x.observation_id NOT IN (` + observations.query + `))
 AND NOT EXISTS (SELECT 1 FROM metric_availability x WHERE x.provenance_id = p.provenance_id AND x.metric_availability_id NOT IN (` + availability.query + `))`}
	provenance.args = append(provenance.args, observations.args...)
	provenance.args = append(provenance.args, availability.args...)
	provenance.args = append(provenance.args, observations.args...)
	provenance.args = append(provenance.args, availability.args...)
	sessionProfile := `CASE WHEN EXISTS (SELECT 1 FROM observed_session_assignments a WHERE a.observed_session_id = o.observed_session_id) THEN (SELECT a.profile_id FROM observed_session_assignments a WHERE a.observed_session_id = o.observed_session_id) ELSE o.profile_id END`
	sessions := selectClass("observed_sessions", "observed_sessions", "observed_session_id", "o", sessionProfile, "o.project_identity_id", "o.started_at", "COALESCE(o.last_observed_at, o.ended_at, o.started_at)", false)
	launches := selectClass("managed_launches", "managed_launches", "managed_launch_id", "m", "m.profile_id", "m.project_identity_id", "m.started_at", "COALESCE(m.ended_at, m.started_at)", false)
	launches.query += ` AND m.state IN ('exited', 'abandoned')`
	correlations := historySelection{table: "correlation_evidence", column: "correlation_evidence_id", query: `SELECT c.correlation_evidence_id FROM correlation_evidence c WHERE c.observed_session_id IN (` + sessions.query + `) OR c.managed_launch_id IN (` + launches.query + `)`}
	correlations.args = append(correlations.args, sessions.args...)
	correlations.args = append(correlations.args, launches.args...)
	updates := historySelection{table: "correlation_updates", column: "observed_session_id", query: `SELECT DISTINCT o.observed_session_id FROM observed_sessions o WHERE o.correlation_state = 'correlated' AND o.observed_session_id NOT IN (` + sessions.query + `) AND o.observed_session_id IN (SELECT c.observed_session_id FROM correlation_evidence c WHERE c.correlation_evidence_id IN (` + correlations.query + `))`}
	updates.args = append(updates.args, sessions.args...)
	updates.args = append(updates.args, correlations.args...)
	aggregates := selectClass("aggregates", "usage_aggregates", "aggregate_id", "g", "g.profile_id", "g.project_identity_id", "g.bucket_start", "g.bucket_end", true)
	checkpoints := selectClass("checkpoints", "checkpoints", "checkpoint_id", "c", "NULL", "c.project_identity_id", "c.created_at", "c.created_at", false)
	checkpoints.query += ` AND c.status <> 'launching'
 AND NOT EXISTS (SELECT 1 FROM managed_launches m WHERE m.continuation_checkpoint_id = c.checkpoint_id AND m.state IN ('pending', 'running'))`
	return []historySelection{updates, correlations, sessions, launches, observations, availability, provenance, snapshots, aggregates, checkpoints}
}

var _ usage.HistoryRepository = (*Store)(nil)
