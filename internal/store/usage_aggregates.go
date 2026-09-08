package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

type aggregateSourceScope struct{ LoginIdentity, Workspace string }

func (store *Store) aggregateObservation(ctx context.Context, tx *sql.Tx, profileID string, o usage.Observation, scope aggregateSourceScope) error {
	kind, start, end, zone, err := usage.HistoryBucket(o)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	// The lookup digest contains only queryable normalized facts, never encrypted
	// identity/workspace values. Candidate scopes are compared through the vault.
	group, _ := json.Marshal([]any{profileID, o.Metric, o.Value, o.Source, o.SourceVersion, o.Provenance, o.Availability, o.Assumptions, o.Uncertainty, kind, start, end, zone})
	digest := sha256.Sum256(group)
	key := hex.EncodeToString(digest[:])
	rows, err := tx.QueryContext(ctx, `SELECT aggregate_id, source_scope_ciphertext FROM usage_aggregates WHERE group_key = ?`, key)
	if err != nil {
		return coded(apperrors.StoreReadFailed, err)
	}
	type candidate struct {
		id         string
		ciphertext []byte
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.ciphertext); err != nil {
			rows.Close()
			return coded(apperrors.StoreReadFailed, err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return coded(apperrors.StoreReadFailed, err)
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		value, err := decryptField(ctx, secureVault, c.ciphertext, aggregateScopeAAD(c.id))
		if err != nil {
			return err
		}
		var prior aggregateSourceScope
		if err := json.Unmarshal([]byte(value), &prior); err != nil {
			return coded(apperrors.StoreReadFailed, err)
		}
		if prior != scope {
			continue
		}
		_, err = tx.ExecContext(ctx, `UPDATE usage_aggregates SET samples = samples + 1,
   first_observed_at = CASE WHEN rtrim(first_observed_at, 'Z') > rtrim(?, 'Z') THEN ? ELSE first_observed_at END,
   last_observed_at = CASE WHEN rtrim(last_observed_at, 'Z') < rtrim(?, 'Z') THEN ? ELSE last_observed_at END,
   first_captured_at = CASE WHEN rtrim(first_captured_at, 'Z') > rtrim(?, 'Z') THEN ? ELSE first_captured_at END,
   last_captured_at = CASE WHEN rtrim(last_captured_at, 'Z') < rtrim(?, 'Z') THEN ? ELSE last_captured_at END WHERE aggregate_id = ?`,
			formatStoredTime(o.ObservedAt), formatStoredTime(o.ObservedAt), formatStoredTime(o.ObservedAt), formatStoredTime(o.ObservedAt), formatStoredTime(o.CapturedAt), formatStoredTime(o.CapturedAt), formatStoredTime(o.CapturedAt), formatStoredTime(o.CapturedAt), c.id)
		if err != nil {
			return coded(apperrors.StoreWriteFailed, err)
		}
		return nil
	}
	id, err := newStoreIdentifier("aggregate")
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	plain, _ := json.Marshal(scope)
	ciphertext, err := encryptField(ctx, secureVault, plain, aggregateScopeAAD(id))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_aggregates (aggregate_id, group_key, profile_id, metric_key, value, unit, source, source_version, provenance_label, availability, assumptions, uncertainty, bucket_kind, bucket_start, bucket_end, timezone, first_observed_at, last_observed_at, first_captured_at, last_captured_at, samples, source_scope_ciphertext)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, id, key, profileID, o.Metric.Key, o.Value, o.Metric.Unit, o.Source, o.SourceVersion, o.Provenance, o.Availability, o.Assumptions, o.Uncertainty, kind, formatStoredTime(start), formatStoredTime(end), zone, formatStoredTime(o.ObservedAt), formatStoredTime(o.ObservedAt), formatStoredTime(o.CapturedAt), formatStoredTime(o.CapturedAt), ciphertext)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	return nil
}

func aggregateScopeAAD(id string) []byte {
	return []byte("codex-folio/usage-aggregates/" + id + "/source-scope")
}

func (store *Store) ListUsageAggregates(ctx context.Context, scope usage.HistoryScope) ([]usage.HistoryAggregate, error) {
	if err := scope.Validate(); err != nil {
		return nil, apperrors.New(apperrors.AnalyticsRequestInvalid, err)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	where, args := historyWhere(scope, "g.profile_id", "g.project_identity_id", "g.bucket_start", "g.bucket_end", true)
	rows, err := store.db.QueryContext(ctx, `SELECT g.aggregate_id, g.profile_id, COALESCE(g.project_identity_id, ''),
 g.metric_key, g.value, g.unit, m.value_kind, m.source_class, m.scope, m.aggregation,
 g.source, g.source_version, g.provenance_label, g.availability, g.assumptions, g.uncertainty,
 g.bucket_kind, g.bucket_start, g.bucket_end, g.timezone, g.first_observed_at, g.last_observed_at,
 g.first_captured_at, g.last_captured_at, g.samples, g.source_scope_ciphertext
 FROM usage_aggregates g JOIN usage_metrics m ON m.metric_key = g.metric_key WHERE `+where+` ORDER BY g.bucket_start DESC, g.aggregate_id LIMIT 1000`, args...)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	result := []usage.HistoryAggregate{}
	for rows.Next() {
		var a usage.HistoryAggregate
		var start, end, firstObserved, lastObserved, firstCaptured, lastCaptured string
		var ciphertext []byte
		err := rows.Scan(&a.ID, &a.ProfileID, &a.ProjectID, &a.Metric.Key, &a.Value, &a.Metric.Unit, &a.Metric.ValueKind, &a.Metric.SourceClass, &a.Metric.Scope, &a.Metric.Aggregation, &a.Source, &a.SourceVersion, &a.Provenance, &a.Availability, &a.Assumptions, &a.Uncertainty, &a.BucketKind, &start, &end, &a.Timezone, &firstObserved, &lastObserved, &firstCaptured, &lastCaptured, &a.Samples, &ciphertext)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		for _, item := range []struct {
			value  string
			target *time.Time
		}{{start, &a.BucketStart}, {end, &a.BucketEnd}, {firstObserved, &a.FirstObservedAt}, {lastObserved, &a.LastObservedAt}, {firstCaptured, &a.FirstCapturedAt}, {lastCaptured, &a.LastCapturedAt}} {
			if *item.target, err = parseStoredTime(item.value); err != nil {
				return nil, coded(apperrors.StoreReadFailed, err)
			}
		}
		secureVault, err := store.requireVault()
		if err != nil {
			return nil, err
		}
		plain, err := decryptField(ctx, secureVault, ciphertext, aggregateScopeAAD(a.ID))
		if err != nil {
			return nil, err
		}
		var sourceScope aggregateSourceScope
		if err := json.Unmarshal([]byte(plain), &sourceScope); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		a.LoginIdentity, a.Workspace = sourceScope.LoginIdentity, sourceScope.Workspace
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

// All stored times are UTC. Removing Z keeps RFC3339Nano's optional fractional
// seconds chronologically ordered, including sub-millisecond boundaries.
func historyWhere(scope usage.HistoryScope, profile, project, start, end string, bucket bool) (string, []any) {
	parts, args := []string{"1 = 1"}, []any{}
	if scope.ProfileID != "*" {
		parts = append(parts, profile+" = ?")
		args = append(args, scope.ProfileID)
	}
	if scope.ProjectID == "none" {
		parts = append(parts, project+" IS NULL")
	} else if scope.ProjectID != "*" {
		parts = append(parts, project+" = ?")
		args = append(args, scope.ProjectID)
	}
	if scope.From != "all" {
		at, _ := time.Parse(time.RFC3339Nano, scope.From)
		parts = append(parts, "rtrim("+start+", 'Z') >= ?")
		args = append(args, strings.TrimSuffix(formatStoredTime(at), "Z"))
	}
	if scope.To != "all" {
		at, _ := time.Parse(time.RFC3339Nano, scope.To)
		comparison := " < ?"
		if bucket {
			comparison = " <= ?"
		}
		parts = append(parts, "rtrim("+end+", 'Z')"+comparison)
		args = append(args, strings.TrimSuffix(formatStoredTime(at), "Z"))
	}
	return strings.Join(parts, " AND "), args
}

func historyIDs(ctx context.Context, tx *sql.Tx, query string, args []any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func deleteHistoryIDs(ctx context.Context, tx *sql.Tx, table, column string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
