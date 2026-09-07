// Package usage owns normalized Usage Snapshot semantics.
package usage

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalid            = errors.New("usage request is invalid")
	ErrProfileNotFound    = errors.New("usage profile was not found")
	ErrProfileUnavailable = errors.New("usage profile is unavailable")
	ErrCollectionFailed   = errors.New("usage collection failed")
	ErrSourceInvalid      = errors.New("usage source response is invalid")
	ErrPersistenceFailed  = errors.New("usage snapshot could not be persisted")
)

const (
	SourceCodexAppServer = "codex_app_server"
	SourceLocalMetadata  = "local_metadata"
	SourceDerived        = "derived"
	ProvenanceProvider   = "Provider-reported Metric"
	ProvenanceLocal      = "Locally-derived Metric"
	ProvenanceEstimated  = "Estimated Metric"
	ProvenanceObserved   = "Observed during session"
	FreshnessFresh       = "fresh"
	FreshnessStale       = "stale"

	AvailabilityAvailable                = "available"
	AvailabilityUnsupported              = "unsupported"
	AvailabilityTemporarilyUnavailable   = "temporarily_unavailable"
	AvailabilityStale                    = "stale"
	AvailabilityReauthenticationRequired = "reauthentication_required"
	AvailabilityPartial                  = "partial"
	AvailabilityContradictory            = "contradictory"

	ReasonUnsupported      = "capability_unsupported"
	ReasonCollectionFailed = "collection_failed"
	ReasonMalformedSource  = "malformed_source"
	ReasonReauthentication = "reauthentication_required"
	ReasonStale            = "evidence_older_than_10_minutes"
	ReasonContradictory    = "supported_sources_disagree"

	TriggerExplicitRefresh  = "explicit_refresh"
	TriggerDashboardOpen    = "dashboard_open"
	TriggerDashboardRefresh = "dashboard_refresh"
	TriggerPreLaunch        = "pre_launch"
	TriggerPostExit         = "post_exit"

	freshnessLimit = 10 * time.Minute
)

type Metric struct {
	Key         string `json:"metric_key"`
	ValueKind   string `json:"value_kind"`
	Unit        string `json:"unit"`
	SourceClass string `json:"source_class"`
	Scope       string `json:"scope"`
	Aggregation string `json:"aggregation"`
}

var registry = []Metric{
	{Key: "codex.primary.used_percent", ValueKind: "percentage", Unit: "percent", SourceClass: ProvenanceProvider, Scope: "provider_quota_window", Aggregation: "none"},
	{Key: "codex.secondary.used_percent", ValueKind: "percentage", Unit: "percent", SourceClass: ProvenanceProvider, Scope: "provider_quota_window", Aggregation: "none"},
}

func Registry() []Metric {
	return append([]Metric(nil), registry...)
}

type Observation struct {
	ID                string     `json:"observation_id,omitempty"`
	Metric            Metric     `json:"metric"`
	Value             float64    `json:"value"`
	ObservedAt        time.Time  `json:"observed_at"`
	CapturedAt        time.Time  `json:"captured_at"`
	CaptureAgeSeconds int64      `json:"capture_age_seconds"`
	WindowStart       *time.Time `json:"window_start,omitempty"`
	WindowEnd         *time.Time `json:"window_end,omitempty"`
	WindowTimezone    string     `json:"window_timezone"`
	Source            string     `json:"source"`
	SourceVersion     string     `json:"source_version"`
	Provenance        string     `json:"provenance"`
	Freshness         string     `json:"freshness"`
	Availability      string     `json:"availability"`
	Assumptions       string     `json:"assumptions"`
	Uncertainty       string     `json:"uncertainty"`
}

type MetricAvailability struct {
	ID         string    `json:"metric_availability_id,omitempty"`
	MetricKey  string    `json:"metric_key"`
	State      string    `json:"state"`
	Reason     string    `json:"reason"`
	CheckedAt  time.Time `json:"checked_at"`
	Provenance string    `json:"provenance"`
}

type Snapshot struct {
	ID            string               `json:"snapshot_id,omitempty"`
	ProfileID     string               `json:"profile_id,omitempty"`
	Alias         string               `json:"alias,omitempty"`
	Source        string               `json:"source"`
	SourceVersion string               `json:"source_version"`
	CapturedAt    time.Time            `json:"captured_at"`
	Status        string               `json:"status"`
	TriggerReason string               `json:"trigger_reason"`
	Observations  []Observation        `json:"observations"`
	Availability  []MetricAvailability `json:"availability"`
}

type CollectionRequest struct {
	Executable    string
	IdentityHome  string
	SourceVersion string
	CapturedAt    time.Time
}

type Collector interface {
	Collect(context.Context, CollectionRequest) (Snapshot, error)
}

type ProfileTarget struct {
	ID           string
	Alias        string
	IdentityHome string
}

type Store interface {
	ResolveUsageProfile(context.Context, string) (ProfileTarget, error)
	SaveUsageSnapshot(context.Context, ProfileTarget, Snapshot) (Snapshot, error)
	LatestUsageSnapshot(context.Context, ProfileTarget) (Snapshot, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	mu        sync.Mutex
	store     Store
	collector Collector
	clock     Clock
}

func NewService(store Store, collector Collector, clock Clock) (*Service, error) {
	if store == nil || collector == nil || clock == nil {
		return nil, ErrInvalid
	}
	return &Service{store: store, collector: collector, clock: clock}, nil
}

func (service *Service) Refresh(ctx context.Context, alias, executable, sourceVersion, triggerReason string) (Snapshot, error) {
	if service == nil || service.store == nil || service.collector == nil || service.clock == nil || alias == "" || executable == "" || sourceVersion == "" || !ValidTriggerReason(triggerReason) {
		return Snapshot{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	target, err := service.store.ResolveUsageProfile(ctx, alias)
	if err != nil {
		return Snapshot{}, err
	}
	capturedAt := service.clock.Now().UTC()
	if capturedAt.IsZero() {
		return Snapshot{}, ErrInvalid
	}
	snapshot, err := service.collector.Collect(ctx, CollectionRequest{Executable: executable, IdentityHome: target.IdentityHome, SourceVersion: sourceVersion, CapturedAt: capturedAt})
	snapshot.TriggerReason = triggerReason
	if err == nil {
		err = finalizeSnapshot(&snapshot, capturedAt)
	}
	if err != nil {
		reason := ReasonCollectionFailed
		if errors.Is(err, ErrSourceInvalid) {
			reason = ReasonMalformedSource
		}
		failureSnapshot := NewUnavailableSnapshot(sourceVersion, capturedAt, AvailabilityTemporarilyUnavailable, reason)
		failureSnapshot.TriggerReason = triggerReason
		_, saveErr := service.store.SaveUsageSnapshot(ctx, target, failureSnapshot)
		if saveErr != nil {
			return Snapshot{}, saveErr
		}
		failure, saveErr := service.store.LatestUsageSnapshot(ctx, target)
		if saveErr != nil {
			return Snapshot{}, saveErr
		}
		finalizeSnapshot(&failure, capturedAt)
		return failure, err
	}
	return service.store.SaveUsageSnapshot(ctx, target, snapshot)
}

func ValidTriggerReason(value string) bool {
	return value == TriggerExplicitRefresh || value == TriggerDashboardOpen || value == TriggerDashboardRefresh || value == TriggerPreLaunch || value == TriggerPostExit
}

func (service *Service) Latest(ctx context.Context, alias string) (Snapshot, error) {
	if service == nil || service.store == nil || service.clock == nil || alias == "" {
		return Snapshot{}, ErrInvalid
	}
	target, err := service.store.ResolveUsageProfile(ctx, alias)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := service.store.LatestUsageSnapshot(ctx, target)
	if err != nil {
		return Snapshot{}, err
	}
	now := service.clock.Now().UTC()
	if now.IsZero() {
		return Snapshot{}, ErrInvalid
	}
	if err := finalizeSnapshot(&snapshot, now); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func NewUnavailableSnapshot(sourceVersion string, capturedAt time.Time, state, reason string) Snapshot {
	snapshot := Snapshot{Source: SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt, Status: state, Observations: []Observation{}, Availability: []MetricAvailability{}}
	for _, metric := range Registry() {
		snapshot.Availability = append(snapshot.Availability, MetricAvailability{MetricKey: metric.Key, State: state, Reason: reason, CheckedAt: capturedAt, Provenance: ProvenanceProvider})
	}
	return snapshot
}

func finalizeSnapshot(snapshot *Snapshot, now time.Time) error {
	states := make(map[string]*MetricAvailability, len(snapshot.Availability))
	for index := range snapshot.Availability {
		item := &snapshot.Availability[index]
		states[item.MetricKey] = item
		if item.Reason == "" {
			switch item.State {
			case AvailabilityUnsupported:
				item.Reason = ReasonUnsupported
			case AvailabilityReauthenticationRequired:
				item.Reason = ReasonReauthentication
			}
		}
	}
	values := make(map[string]float64, len(snapshot.Observations))
	seen := make(map[string]bool, len(snapshot.Observations))
	fresh := make(map[string]bool, len(snapshot.Observations))
	contradictory := make(map[string]bool, len(snapshot.Observations))
	for index := range snapshot.Observations {
		item := &snapshot.Observations[index]
		if item.CapturedAt.IsZero() {
			item.CapturedAt = item.ObservedAt
		}
		age := now.Sub(item.CapturedAt)
		if age < 0 || (item.Provenance == ProvenanceEstimated && item.Assumptions == "" && item.Uncertainty == "") {
			return ErrSourceInvalid
		}
		item.CaptureAgeSeconds = int64(age / time.Second)
		item.Freshness = FreshnessFresh
		if age > freshnessLimit {
			item.Freshness = FreshnessStale
		} else {
			fresh[item.Metric.Key] = true
		}
		if item.Source == "" {
			item.Source = snapshot.Source
		}
		if item.SourceVersion == "" {
			item.SourceVersion = snapshot.SourceVersion
		}
		if item.WindowTimezone == "" && (item.WindowStart != nil || item.WindowEnd != nil) {
			item.WindowTimezone = "UTC"
		}
		if seen[item.Metric.Key] && values[item.Metric.Key] != item.Value {
			contradictory[item.Metric.Key] = true
		} else {
			values[item.Metric.Key], seen[item.Metric.Key] = item.Value, true
		}
	}
	for key := range seen {
		availability := states[key]
		if availability == nil || availability.State != AvailabilityAvailable {
			continue
		}
		if contradictory[key] {
			availability.State, availability.Reason = AvailabilityContradictory, ReasonContradictory
		} else if !fresh[key] {
			availability.State, availability.Reason = AvailabilityStale, ReasonStale
		}
	}
	for index := range snapshot.Observations {
		item := &snapshot.Observations[index]
		if availability := states[item.Metric.Key]; availability != nil {
			if item.Availability != AvailabilityContradictory {
				item.Availability = availability.State
			}
		}
	}
	snapshot.Status = snapshotStatus(snapshot.Availability)
	return nil
}

func snapshotStatus(items []MetricAvailability) string {
	if len(items) == 0 {
		return AvailabilityUnsupported
	}
	state := items[0].State
	for _, item := range items {
		if item.State == AvailabilityContradictory {
			return AvailabilityContradictory
		}
		if item.State != state {
			state = AvailabilityPartial
		}
	}
	return state
}
