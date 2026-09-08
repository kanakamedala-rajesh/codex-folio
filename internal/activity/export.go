package activity

import (
	"context"
	"slices"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

const ExportSchemaVersion = "codex-folio.analytics.v1"

type ExportRequest struct {
	Format       string   `json:"format"`
	Datasets     []string `json:"datasets"`
	Scope        string   `json:"scope"`
	ProfileID    string   `json:"profile_id"`
	ProjectID    string   `json:"project_id"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	IncludePaths bool     `json:"include_paths"`
}

type ExportDatasetPreview struct {
	Dataset     string   `json:"dataset"`
	Fields      []string `json:"fields"`
	RecordCount int      `json:"record_count"`
}

type UsageExportRecord struct {
	ObservationID     string       `json:"observation_id"`
	ProfileID         string       `json:"profile_id"`
	ProfileAlias      string       `json:"profile_alias"`
	ProjectID         string       `json:"project_id"`
	ProjectAlias      string       `json:"project_alias"`
	ProjectBasename   string       `json:"project_basename"`
	Metric            usage.Metric `json:"metric"`
	Value             float64      `json:"value"`
	Source            string       `json:"source"`
	SourceVersion     string       `json:"source_version"`
	Provenance        string       `json:"provenance"`
	Freshness         string       `json:"freshness"`
	Availability      string       `json:"availability"`
	LoginIdentity     string       `json:"login_identity"`
	Workspace         string       `json:"workspace"`
	WindowStart       string       `json:"window_start"`
	WindowEnd         string       `json:"window_end"`
	WindowTimezone    string       `json:"window_timezone"`
	ObservedAt        string       `json:"observed_at"`
	CapturedAt        string       `json:"captured_at"`
	CaptureAgeSeconds int64        `json:"capture_age_seconds"`
	Assumptions       string       `json:"assumptions"`
	Uncertainty       string       `json:"uncertainty"`
	CanonicalPath     string       `json:"canonical_path,omitempty"`
}

type AggregateExportRecord struct {
	ID              string       `json:"id"`
	ProfileID       string       `json:"profile_id"`
	ProfileAlias    string       `json:"profile_alias"`
	ProjectID       string       `json:"project_id"`
	ProjectAlias    string       `json:"project_alias"`
	ProjectBasename string       `json:"project_basename"`
	Metric          usage.Metric `json:"metric"`
	Value           float64      `json:"value"`
	Source          string       `json:"source"`
	SourceVersion   string       `json:"source_version"`
	Provenance      string       `json:"provenance"`
	Availability    string       `json:"availability"`
	Freshness       string       `json:"freshness"`
	LoginIdentity   string       `json:"login_identity"`
	Workspace       string       `json:"workspace"`
	BucketKind      string       `json:"bucket_kind"`
	BucketStart     string       `json:"bucket_start"`
	BucketEnd       string       `json:"bucket_end"`
	Timezone        string       `json:"timezone"`
	FirstObservedAt string       `json:"first_observed_at"`
	LastObservedAt  string       `json:"last_observed_at"`
	FirstCapturedAt string       `json:"first_captured_at"`
	LastCapturedAt  string       `json:"last_captured_at"`
	Samples         int64        `json:"samples"`
	Assumptions     string       `json:"assumptions"`
	Uncertainty     string       `json:"uncertainty"`
	CanonicalPath   string       `json:"canonical_path,omitempty"`
}

type AvailabilityExportRecord struct {
	ID                string       `json:"metric_availability_id"`
	ProfileID         string       `json:"profile_id"`
	ProfileAlias      string       `json:"profile_alias"`
	ProjectID         string       `json:"project_id"`
	ProjectAlias      string       `json:"project_alias"`
	ProjectBasename   string       `json:"project_basename"`
	Metric            usage.Metric `json:"metric"`
	State             string       `json:"state"`
	Reason            string       `json:"reason"`
	CheckedAt         string       `json:"checked_at"`
	Source            string       `json:"source"`
	SourceVersion     string       `json:"source_version"`
	Provenance        string       `json:"provenance"`
	Freshness         string       `json:"freshness"`
	CaptureAgeSeconds int64        `json:"capture_age_seconds"`
	LoginIdentity     string       `json:"login_identity"`
	Workspace         string       `json:"workspace"`
	CanonicalPath     string       `json:"canonical_path,omitempty"`
}

type ActivityExportRecord struct {
	TimelineRecord
	CanonicalPath string `json:"canonical_path,omitempty"`
}

type ExportRecords struct {
	Usage        *[]UsageExportRecord        `json:"usage,omitempty"`
	Availability *[]AvailabilityExportRecord `json:"availability,omitempty"`
	Aggregates   *[]AggregateExportRecord    `json:"aggregates,omitempty"`
	Activity     *[]ActivityExportRecord     `json:"activity,omitempty"`
}

type ExportResult struct {
	SchemaVersion string                 `json:"schema_version"`
	Filters       ExportRequest          `json:"filters"`
	Preview       []ExportDatasetPreview `json:"preview"`
	Records       ExportRecords          `json:"records"`
}

type ExportRepository interface {
	ExportAnalytics(context.Context, ExportRequest) (ExportRecords, error)
}

type ExportService struct{ repository ExportRepository }

func NewExportService(repository ExportRepository) *ExportService {
	return &ExportService{repository: repository}
}

func (service *ExportService) Export(ctx context.Context, request ExportRequest) (ExportResult, error) {
	if service == nil || service.repository == nil || !validExportRequest(request) {
		return ExportResult{}, apperrors.New(apperrors.AnalyticsRequestInvalid, usage.ErrInvalid)
	}
	records, err := service.repository.ExportAnalytics(contextOrBackground(ctx), request)
	if err != nil {
		return ExportResult{}, err
	}
	if request.Scope == usage.ScopeCombinedIdentity {
		records = deduplicateCombinedExport(records)
	}
	result := ExportResult{SchemaVersion: ExportSchemaVersion, Filters: request, Records: records}
	for _, dataset := range request.Datasets {
		fields := slices.Clone(exportFields[dataset])
		if request.Format == "json" {
			fields = slices.Clone(exportJSONFields[dataset])
		}
		if request.IncludePaths {
			fields = append(fields, "canonical_path")
		}
		count := exportRecordCount(records.Activity)
		if dataset == "usage" {
			count = exportRecordCount(records.Usage)
		} else if dataset == "availability" {
			count = exportRecordCount(records.Availability)
		} else if dataset == "aggregates" {
			count = exportRecordCount(records.Aggregates)
		}
		result.Preview = append(result.Preview, ExportDatasetPreview{Dataset: dataset, Fields: fields, RecordCount: count})
	}
	return result, nil
}

func deduplicateCombinedExport(records ExportRecords) ExportRecords {
	if records.Usage != nil {
		snapshots := make([]usage.Snapshot, len(*records.Usage))
		for index, record := range *records.Usage {
			snapshots[index] = usage.Snapshot{
				ProfileID: record.ProfileID, LoginIdentity: record.LoginIdentity, Workspace: record.Workspace,
				Observations: []usage.Observation{{ID: record.ObservationID, Metric: record.Metric, Value: record.Value, WindowStart: exportTimePointer(record.WindowStart), WindowEnd: exportTimePointer(record.WindowEnd)}},
			}
		}
		kept := combinedObservationIDs(snapshots)
		filtered := make([]UsageExportRecord, 0, len(kept))
		for _, record := range *records.Usage {
			if kept[record.ObservationID] {
				filtered = append(filtered, record)
			}
		}
		records.Usage = &filtered
	}
	if records.Aggregates != nil {
		snapshots := make([]usage.Snapshot, len(*records.Aggregates))
		for index, record := range *records.Aggregates {
			snapshots[index] = usage.Snapshot{
				ProfileID: record.ProfileID, LoginIdentity: record.LoginIdentity, Workspace: record.Workspace,
				Observations: []usage.Observation{{ID: record.ID, Metric: record.Metric, Value: record.Value, WindowStart: exportTimePointer(record.BucketStart), WindowEnd: exportTimePointer(record.BucketEnd)}},
			}
		}
		kept := combinedObservationIDs(snapshots)
		filtered := make([]AggregateExportRecord, 0, len(kept))
		for _, record := range *records.Aggregates {
			if kept[record.ID] {
				filtered = append(filtered, record)
			}
		}
		records.Aggregates = &filtered
	}
	return records
}

func combinedObservationIDs(snapshots []usage.Snapshot) map[string]bool {
	kept := make(map[string]bool)
	for _, snapshot := range usage.DeduplicateCombinedEvidence(snapshots) {
		for _, observation := range snapshot.Observations {
			kept[observation.ID] = true
		}
	}
	return kept
}

func exportTimePointer(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func exportRecordCount[T any](records *[]T) int {
	if records == nil {
		return 0
	}
	return len(*records)
}

var exportFields = map[string][]string{
	"usage":        {"observation_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric_key", "value", "value_kind", "unit", "metric_scope", "aggregation", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "window_start", "window_end", "window_timezone", "observed_at", "captured_at", "capture_age_seconds", "assumptions", "uncertainty"},
	"availability": {"metric_availability_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric_key", "value_kind", "unit", "metric_scope", "aggregation", "state", "reason", "checked_at", "source", "source_version", "provenance", "freshness", "capture_age_seconds", "login_identity", "workspace"},
	"aggregates":   {"id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric_key", "value", "value_kind", "unit", "metric_scope", "aggregation", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "bucket_kind", "bucket_start", "bucket_end", "timezone", "first_observed_at", "last_observed_at", "first_captured_at", "last_captured_at", "samples", "assumptions", "uncertainty"},
	"activity":     {"record_type", "id", "source_session_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "source", "source_version", "provenance", "started_at", "last_observed_at", "lifecycle", "exit_status", "model", "tokens_used", "correlation_state", "correlation_managed_launch_id", "correlation_evidence_type", "correlation_confidence"},
}

var exportJSONFields = map[string][]string{
	"usage":        {"observation_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric", "value", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "window_start", "window_end", "window_timezone", "observed_at", "captured_at", "capture_age_seconds", "assumptions", "uncertainty"},
	"availability": {"metric_availability_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric", "state", "reason", "checked_at", "source", "source_version", "provenance", "freshness", "capture_age_seconds", "login_identity", "workspace"},
	"aggregates":   {"id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "metric", "value", "source", "source_version", "provenance", "freshness", "availability", "login_identity", "workspace", "bucket_kind", "bucket_start", "bucket_end", "timezone", "first_observed_at", "last_observed_at", "first_captured_at", "last_captured_at", "samples", "assumptions", "uncertainty"},
	"activity":     {"record_type", "id", "source_session_id", "profile_id", "profile_alias", "project_id", "project_alias", "project_basename", "source", "source_version", "provenance", "started_at", "last_observed_at", "lifecycle", "exit_status", "model", "tokens_used", "correlation"},
}

func validExportRequest(request ExportRequest) bool {
	if request.Format != "json" && request.Format != "csv" || len(request.Datasets) == 0 || request.Format == "csv" && len(request.Datasets) != 1 {
		return false
	}
	seen := map[string]bool{}
	for _, dataset := range request.Datasets {
		if seen[dataset] || exportFields[dataset] == nil {
			return false
		}
		seen[dataset] = true
	}
	if request.Scope != usage.ScopeSelectedProfile && request.Scope != usage.ScopeCombinedIdentity || request.Scope == usage.ScopeCombinedIdentity && request.ProfileID != "*" || request.Scope == usage.ScopeSelectedProfile && request.ProfileID == "*" {
		return false
	}
	return (usage.HistoryScope{ProfileID: request.ProfileID, ProjectID: request.ProjectID, From: request.From, To: request.To, Classes: []string{"usage"}}).Validate() == nil
}
