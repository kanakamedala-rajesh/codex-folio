package store

import (
	"context"
	"database/sql"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (store *Store) ExportAnalytics(ctx context.Context, request activity.ExportRequest) (activity.ExportRecords, error) {
	ctx = contextOrBackground(ctx)
	profileID, aliases, err := store.exportProfiles(ctx, request.ProfileID)
	if err != nil {
		return activity.ExportRecords{}, err
	}
	scope := usage.HistoryScope{ProfileID: profileID, ProjectID: request.ProjectID, From: request.From, To: request.To, Classes: []string{"usage"}}
	result := activity.ExportRecords{}
	if containsExportDataset(request.Datasets, "usage") {
		records, requestErr := store.exportUsage(ctx, scope)
		result.Usage, err = &records, requestErr
	}
	if err == nil && containsExportDataset(request.Datasets, "availability") {
		records, requestErr := store.exportAvailability(ctx, scope)
		result.Availability, err = &records, requestErr
	}
	if err == nil && containsExportDataset(request.Datasets, "aggregates") {
		var aggregates []usage.HistoryAggregate
		scope.Classes = []string{"aggregates"}
		aggregates, err = store.listUsageAggregates(ctx, scope, false)
		if err == nil {
			records := make([]activity.AggregateExportRecord, 0, len(aggregates))
			for _, aggregate := range aggregates {
				freshness, _, freshnessErr := usage.FreshnessAt(aggregate.LastCapturedAt, store.clock.Now())
				if freshnessErr != nil {
					return activity.ExportRecords{}, coded(apperrors.StoreReadFailed, freshnessErr)
				}
				alias := aliases[aggregate.ProfileID]
				if alias == "" {
					alias = aggregate.ProfileID
				}
				records = append(records, activity.AggregateExportRecord{
					ID: aggregate.ID, ProfileID: aggregate.ProfileID, ProfileAlias: alias, ProjectID: aggregate.ProjectID,
					Metric: aggregate.Metric, Value: aggregate.Value, Source: aggregate.Source, SourceVersion: aggregate.SourceVersion,
					Provenance: aggregate.Provenance, Availability: aggregate.Availability, Freshness: freshness, LoginIdentity: aggregate.LoginIdentity, Workspace: aggregate.Workspace,
					BucketKind: aggregate.BucketKind, BucketStart: exportTime(aggregate.BucketStart), BucketEnd: exportTime(aggregate.BucketEnd), Timezone: aggregate.Timezone,
					FirstObservedAt: exportTime(aggregate.FirstObservedAt), LastObservedAt: exportTime(aggregate.LastObservedAt), FirstCapturedAt: exportTime(aggregate.FirstCapturedAt), LastCapturedAt: exportTime(aggregate.LastCapturedAt),
					Samples: aggregate.Samples, Assumptions: aggregate.Assumptions, Uncertainty: aggregate.Uncertainty,
				})
			}
			result.Aggregates = &records
		}
	}
	if err == nil && containsExportDataset(request.Datasets, "activity") {
		var records []activity.TimelineRecord
		records, err = store.ListActivity(ctx, activity.Filters{})
		if err == nil {
			exported := []activity.ActivityExportRecord{}
			for _, record := range records {
				if request.Scope == usage.ScopeCombinedIdentity && record.ProfileID == "" {
					continue
				}
				if exportRecordMatches(record.ProfileID, record.ProjectID, record.StartedAt, record.LastObservedAt, profileID, request.ProjectID, request.From, request.To) {
					entry := activity.ActivityExportRecord{TimelineRecord: record}
					if record.RecordType == activity.RecordTypeObservedSession && record.Source == activity.SourceLocalMetadata {
						entry.MetricKey, entry.Unit, entry.Freshness = "codex.local.tokens_used", "tokens", "historical"
						entry.Availability = "absent"
						if record.TokensUsed != nil {
							entry.Availability = "available"
						}
						entry.CoverageStartAt, entry.CoverageEndAt = exportTime(record.StartedAt), exportTime(record.LastObservedAt)
					}
					exported = append(exported, entry)
				}
			}
			result.Activity = &exported
		}
	}
	if err != nil {
		return activity.ExportRecords{}, err
	}
	projects, err := store.ListProjectIdentities(ctx)
	if err != nil {
		return activity.ExportRecords{}, err
	}
	for _, project := range projects {
		if result.Aggregates != nil {
			for index := range *result.Aggregates {
				if (*result.Aggregates)[index].ProjectID == project.ID {
					(*result.Aggregates)[index].ProjectAlias, (*result.Aggregates)[index].ProjectBasename = project.Alias, project.Basename
				}
			}
		}
	}
	if request.IncludePaths {
		privateProjects, err := store.ListProjectRecords(ctx)
		if err != nil {
			return activity.ExportRecords{}, err
		}
		for _, project := range privateProjects {
			if result.Aggregates != nil {
				for index := range *result.Aggregates {
					if (*result.Aggregates)[index].ProjectID == project.ID {
						(*result.Aggregates)[index].CanonicalPath = project.CanonicalPath
					}
				}
			}
			if result.Activity != nil {
				for index := range *result.Activity {
					if (*result.Activity)[index].ProjectID == project.ID {
						(*result.Activity)[index].CanonicalPath = project.CanonicalPath
					}
				}
			}
		}
	}
	return result, nil
}

func (store *Store) exportAvailability(ctx context.Context, scope usage.HistoryScope) ([]activity.AvailabilityExportRecord, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	where, args := historyWhere(scope, "ma.profile_id", "NULL", "ma.checked_at", "ma.checked_at", false)
	rows, err := store.db.QueryContext(ctx, `SELECT ma.metric_availability_id, ma.profile_id, COALESCE(ca.alias, ma.profile_id),
		m.metric_key, m.value_kind, m.unit, m.source_class, m.scope, m.aggregation,
		CASE WHEN ma.condition <> '' THEN ma.condition ELSE ma.state END, ma.reason, ma.checked_at,
		p.source, COALESCE(p.source_version, ''), p.provenance_label, p.freshness,
		COALESCE(s.snapshot_id, ''), s.login_identity_ciphertext, s.workspace_ciphertext
		FROM metric_availability ma LEFT JOIN cli_aliases ca ON ca.profile_id = ma.profile_id
		JOIN usage_metrics m ON m.metric_key = ma.metric_key JOIN metric_provenance p ON p.provenance_id = ma.provenance_id
		LEFT JOIN usage_snapshots s ON s.snapshot_id = (SELECT candidate.snapshot_id FROM usage_snapshots candidate
			WHERE candidate.profile_id = ma.profile_id AND candidate.source = p.source
			AND candidate.source_version = COALESCE(p.source_version, '') AND candidate.captured_at = p.captured_at
			ORDER BY candidate.snapshot_id LIMIT 1)
		WHERE `+where+` ORDER BY rtrim(ma.checked_at, 'Z'), ma.metric_availability_id`, args...)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	result := []activity.AvailabilityExportRecord{}
	for rows.Next() {
		var record activity.AvailabilityExportRecord
		var checked, snapshotID string
		var loginCiphertext, workspaceCiphertext []byte
		if err := rows.Scan(&record.ID, &record.ProfileID, &record.ProfileAlias,
			&record.Metric.Key, &record.Metric.ValueKind, &record.Metric.Unit, &record.Metric.SourceClass, &record.Metric.Scope, &record.Metric.Aggregation,
			&record.State, &record.Reason, &checked, &record.Source, &record.SourceVersion, &record.Provenance, &record.Freshness,
			&snapshotID, &loginCiphertext, &workspaceCiphertext); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		checkedAt, err := parseStoredTime(checked)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		record.CheckedAt = exportTime(checkedAt)
		record.Freshness, record.CaptureAgeSeconds, err = usage.FreshnessAt(checkedAt, store.clock.Now())
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		if snapshotID != "" {
			secureVault, err := store.requireVault()
			if err != nil {
				return nil, err
			}
			if len(loginCiphertext) > 0 {
				record.LoginIdentity, err = decryptField(ctx, secureVault, loginCiphertext, usageScopeAAD(snapshotID, "login-identity"))
			}
			if err == nil && len(workspaceCiphertext) > 0 {
				record.Workspace, err = decryptField(ctx, secureVault, workspaceCiphertext, usageScopeAAD(snapshotID, "workspace"))
			}
			if err != nil {
				return nil, err
			}
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

func (store *Store) exportProfiles(ctx context.Context, requested string) (string, map[string]string, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(ctx, `SELECT ip.profile_id, a.alias, CASE WHEN s.profile_id = ip.profile_id THEN 1 ELSE 0 END
		FROM identity_profiles ip JOIN cli_aliases a ON a.profile_id = ip.profile_id LEFT JOIN selected_profile s ON s.profile_id = ip.profile_id`)
	if err != nil {
		return "", nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	selected, aliases := "", map[string]string{}
	for rows.Next() {
		var id, alias string
		var isSelected bool
		if err := rows.Scan(&id, &alias, &isSelected); err != nil {
			return "", nil, coded(apperrors.StoreReadFailed, err)
		}
		aliases[id] = alias
		if isSelected {
			selected = id
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, coded(apperrors.StoreReadFailed, err)
	}
	if requested == "selected" {
		if selected == "" {
			return "", nil, apperrors.New(apperrors.AnalyticsRequestInvalid, usage.ErrInvalid)
		}
		requested = selected
	}
	return requested, aliases, nil
}

func (store *Store) exportUsage(ctx context.Context, scope usage.HistoryScope) ([]activity.UsageExportRecord, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	where, args := historyWhere(scope, "o.profile_id", "NULL", "o.observed_at", "o.observed_at", false)
	rows, err := store.db.QueryContext(ctx, `SELECT o.observation_id, o.profile_id, COALESCE(a.alias, o.profile_id),
		m.metric_key, m.value_kind, m.unit, m.source_class, m.scope, m.aggregation, o.value,
		p.source, COALESCE(p.source_version, ''), p.provenance_label, p.freshness,
		CASE WHEN ma.condition <> '' THEN ma.condition ELSE ma.state END,
		s.snapshot_id, s.login_identity_ciphertext, s.workspace_ciphertext,
		o.window_start, o.window_end, o.window_timezone, o.observed_at, p.captured_at, o.assumptions, o.uncertainty
		FROM usage_observations o LEFT JOIN cli_aliases a ON a.profile_id = o.profile_id
		JOIN usage_metrics m ON m.metric_key = o.metric_key JOIN metric_provenance p ON p.provenance_id = o.provenance_id
		JOIN metric_availability ma ON ma.metric_availability_id = o.metric_availability_id
		JOIN usage_snapshots s ON s.snapshot_id = o.snapshot_id WHERE `+where+` ORDER BY rtrim(o.observed_at, 'Z'), o.observation_id`, args...)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	result := []activity.UsageExportRecord{}
	for rows.Next() {
		var record activity.UsageExportRecord
		var snapshotID, observed, captured string
		var loginCiphertext, workspaceCiphertext []byte
		var windowStart, windowEnd sql.NullString
		if err := rows.Scan(&record.ObservationID, &record.ProfileID, &record.ProfileAlias,
			&record.Metric.Key, &record.Metric.ValueKind, &record.Metric.Unit, &record.Metric.SourceClass, &record.Metric.Scope, &record.Metric.Aggregation, &record.Value,
			&record.Source, &record.SourceVersion, &record.Provenance, &record.Freshness, &record.Availability,
			&snapshotID, &loginCiphertext, &workspaceCiphertext, &windowStart, &windowEnd, &record.WindowTimezone, &observed, &captured, &record.Assumptions, &record.Uncertainty); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		observedAt, parseErr := parseStoredTime(observed)
		capturedAt, captureErr := parseStoredTime(captured)
		if parseErr != nil || captureErr != nil {
			return nil, coded(apperrors.StoreReadFailed, usage.ErrPersistenceFailed)
		}
		record.ObservedAt, record.CapturedAt = exportTime(observedAt), exportTime(capturedAt)
		record.Freshness, record.CaptureAgeSeconds, parseErr = usage.FreshnessAt(capturedAt, store.clock.Now())
		if parseErr != nil {
			return nil, coded(apperrors.StoreReadFailed, parseErr)
		}
		if windowStart.Valid {
			value, err := parseStoredTime(windowStart.String)
			if err != nil {
				return nil, coded(apperrors.StoreReadFailed, err)
			}
			record.WindowStart = exportTime(value)
		}
		if windowEnd.Valid {
			value, err := parseStoredTime(windowEnd.String)
			if err != nil {
				return nil, coded(apperrors.StoreReadFailed, err)
			}
			record.WindowEnd = exportTime(value)
		}
		if len(loginCiphertext) > 0 || len(workspaceCiphertext) > 0 {
			secureVault, err := store.requireVault()
			if err != nil {
				return nil, err
			}
			if len(loginCiphertext) > 0 {
				record.LoginIdentity, err = decryptField(ctx, secureVault, loginCiphertext, usageScopeAAD(snapshotID, "login-identity"))
			}
			if err == nil && len(workspaceCiphertext) > 0 {
				record.Workspace, err = decryptField(ctx, secureVault, workspaceCiphertext, usageScopeAAD(snapshotID, "workspace"))
			}
			if err != nil {
				return nil, err
			}
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return result, nil
}

func containsExportDataset(datasets []string, wanted string) bool {
	for _, dataset := range datasets {
		if dataset == wanted {
			return true
		}
	}
	return false
}

func exportRecordMatches(recordProfile, recordProject string, start, end time.Time, profile, project, from, to string) bool {
	if profile != "*" && recordProfile != profile || project == "none" && recordProject != "" || project != "*" && project != "none" && recordProject != project {
		return false
	}
	if from != "all" {
		boundary, _ := time.Parse(time.RFC3339Nano, from)
		if start.Before(boundary) {
			return false
		}
	}
	if to != "all" {
		boundary, _ := time.Parse(time.RFC3339Nano, to)
		if !end.Before(boundary) {
			return false
		}
	}
	return true
}

func exportTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

var _ activity.ExportRepository = (*Store)(nil)
