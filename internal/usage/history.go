package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // Named historical zones must also work on Windows installations without zoneinfo.
	"unicode"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	RetentionBatchSize = 100
	PurgeRecordLimit   = 1000
)

// HistoryScope always spells out every dimension. "*" selects all identities,
// "none" selects unassociated projects, and "all" is an open date boundary.
// Dates are UTC instants, inclusive From and exclusive To. Aggregates must fit
// wholly inside the requested interval; they cannot be split after compaction.
type HistoryScope struct {
	ProfileID string   `json:"profile_id"`
	ProjectID string   `json:"project_id"`
	From      string   `json:"from"`
	To        string   `json:"to"`
	Classes   []string `json:"classes"`
}

func (scope HistoryScope) Validate() error {
	for _, value := range []string{scope.ProfileID, scope.ProjectID} {
		if value == "" || len(value) > 200 || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return ErrInvalid
		}
	}
	var bounds [2]time.Time
	for i, value := range []string{scope.From, scope.To} {
		if value == "all" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.IsZero() || parsed.Year() < 1 || parsed.Year() > 9999 {
			return ErrInvalid
		}
		bounds[i] = parsed
	}
	if !bounds[0].IsZero() && !bounds[1].IsZero() && !bounds[0].Before(bounds[1]) {
		return ErrInvalid
	}
	if len(scope.Classes) == 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, class := range scope.Classes {
		if seen[class] || !slices.Contains([]string{"usage", "aggregates", "observed_sessions", "managed_launches", "checkpoints"}, class) {
			return ErrInvalid
		}
		seen[class] = true
	}
	// Sanitized checkpoints have Project Identity, but no profile attribution.
	if seen["checkpoints"] && scope.ProfileID != "*" {
		return ErrInvalid
	}
	return nil
}

func (scope HistoryScope) Confirmation() string {
	scope.Classes = slices.Clone(scope.Classes)
	slices.Sort(scope.Classes)
	for _, bound := range []*string{&scope.From, &scope.To} {
		if parsed, err := time.Parse(time.RFC3339Nano, *bound); err == nil {
			*bound = parsed.UTC().Format(time.RFC3339Nano)
		}
	}
	body, _ := json.Marshal(scope)
	digest := sha256.Sum256(body)
	return "purge-" + hex.EncodeToString(digest[:12])
}

type RecordCount struct {
	RecordClass string `json:"record_class"`
	Count       int64  `json:"count"`
}

type PurgeResult struct {
	Scope        HistoryScope  `json:"scope"`
	Counts       []RecordCount `json:"counts"`
	Confirmation string        `json:"confirmation"`
	RecordLimit  int64         `json:"record_limit"`
	Executable   bool          `json:"executable"`
	Applied      bool          `json:"applied"`
}

type RetentionResult struct {
	Setting   string `json:"setting"`
	Processed int64  `json:"processed"`
	More      bool   `json:"more"`
}

// HistoryAggregate compacts identical normalized readings. Value is a distinct
// observed fact, never a sum of repeated readings or provider percentages.
// Samples counts readings, not sessions, tokens, or independent provider facts.
type HistoryAggregate struct {
	ID              string    `json:"id"`
	ProfileID       string    `json:"profile_id"`
	ProjectID       string    `json:"project_id"`
	Metric          Metric    `json:"metric"`
	Value           float64   `json:"value"`
	Source          string    `json:"source"`
	SourceVersion   string    `json:"source_version"`
	Provenance      string    `json:"provenance"`
	Availability    string    `json:"availability"`
	Assumptions     string    `json:"assumptions"`
	Uncertainty     string    `json:"uncertainty"`
	BucketKind      string    `json:"bucket_kind"`
	BucketStart     time.Time `json:"bucket_start"`
	BucketEnd       time.Time `json:"bucket_end"`
	Timezone        string    `json:"timezone"`
	FirstObservedAt time.Time `json:"first_observed_at"`
	LastObservedAt  time.Time `json:"last_observed_at"`
	FirstCapturedAt time.Time `json:"first_captured_at"`
	LastCapturedAt  time.Time `json:"last_captured_at"`
	Samples         int64     `json:"samples"`
	// Private source scope stays encrypted at rest and outside browser projections.
	LoginIdentity string `json:"-"`
	Workspace     string `json:"-"`
}

// HistoryBucket keeps documented windows intact. Only observations without a
// source window receive a calendar bucket, in their recorded zone or UTC.
func HistoryBucket(observation Observation) (kind string, start, end time.Time, zone string, err error) {
	if observation.WindowStart != nil && observation.WindowEnd != nil {
		return "source_window", observation.WindowStart.UTC(), observation.WindowEnd.UTC(), observation.WindowTimezone, nil
	}
	zone = observation.WindowTimezone
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return "", start, end, "", ErrInvalid
	}
	at := observation.ObservedAt.In(location)
	start = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, location)
	return "calendar_day", start.UTC(), start.AddDate(0, 0, 1).UTC(), zone, nil
}

type HistoryRepository interface {
	AnalyticsRetention(context.Context) (Retention, error)
	SetAnalyticsRetention(context.Context, string) (Retention, error)
	RetainAnalytics(context.Context) (RetentionResult, error)
	PurgeAnalytics(context.Context, HistoryScope, string) (PurgeResult, error)
	ListUsageAggregates(context.Context, HistoryScope) ([]HistoryAggregate, error)
}

type HistoryService struct{ repository HistoryRepository }

func NewHistoryService(repository HistoryRepository) *HistoryService {
	return &HistoryService{repository: repository}
}

func (service *HistoryService) Retention(ctx context.Context, setting string, run bool) (RetentionResult, error) {
	if setting != "" {
		if _, err := ParseRetention(setting); err != nil {
			return RetentionResult{}, apperrors.New(apperrors.AnalyticsRequestInvalid, err)
		}
		if _, err := service.repository.SetAnalyticsRetention(ctx, setting); err != nil {
			return RetentionResult{}, err
		}
	}
	if run {
		return service.repository.RetainAnalytics(ctx)
	}
	policy, err := service.repository.AnalyticsRetention(ctx)
	return RetentionResult{Setting: policy.String()}, err
}

func (service *HistoryService) Purge(ctx context.Context, scope HistoryScope, confirmation string) (PurgeResult, error) {
	return service.repository.PurgeAnalytics(ctx, scope, confirmation)
}

func (service *HistoryService) Aggregates(ctx context.Context, scope HistoryScope) ([]HistoryAggregate, error) {
	return service.repository.ListUsageAggregates(ctx, scope)
}
