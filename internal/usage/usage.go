// Package usage owns normalized Usage Snapshot semantics.
package usage

import (
	"context"
	"errors"
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
	ProvenanceProvider   = "Provider-reported Metric"
	FreshnessFresh       = "fresh"

	AvailabilityAvailable                = "available"
	AvailabilityUnsupported              = "unsupported"
	AvailabilityTemporarilyUnavailable   = "temporarily_unavailable"
	AvailabilityReauthenticationRequired = "reauthentication_required"
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
	ID           string     `json:"observation_id,omitempty"`
	Metric       Metric     `json:"metric"`
	Value        float64    `json:"value"`
	ObservedAt   time.Time  `json:"observed_at"`
	WindowStart  *time.Time `json:"window_start,omitempty"`
	WindowEnd    *time.Time `json:"window_end,omitempty"`
	Provenance   string     `json:"provenance"`
	Freshness    string     `json:"freshness"`
	Availability string     `json:"availability"`
}

type MetricAvailability struct {
	ID         string    `json:"metric_availability_id,omitempty"`
	MetricKey  string    `json:"metric_key"`
	State      string    `json:"state"`
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
}

type Clock interface {
	Now() time.Time
}

type Service struct {
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

func (service *Service) Refresh(ctx context.Context, alias, executable, sourceVersion string) (Snapshot, error) {
	if service == nil || service.store == nil || service.collector == nil || service.clock == nil || alias == "" || executable == "" || sourceVersion == "" {
		return Snapshot{}, ErrInvalid
	}
	target, err := service.store.ResolveUsageProfile(ctx, alias)
	if err != nil {
		return Snapshot{}, err
	}
	capturedAt := service.clock.Now().UTC()
	if capturedAt.IsZero() {
		return Snapshot{}, ErrInvalid
	}
	snapshot, err := service.collector.Collect(ctx, CollectionRequest{Executable: executable, IdentityHome: target.IdentityHome, SourceVersion: sourceVersion, CapturedAt: capturedAt})
	if err != nil {
		if _, saveErr := service.store.SaveUsageSnapshot(ctx, target, NewUnavailableSnapshot(sourceVersion, capturedAt, AvailabilityTemporarilyUnavailable)); saveErr != nil {
			return Snapshot{}, saveErr
		}
		return Snapshot{}, err
	}
	return service.store.SaveUsageSnapshot(ctx, target, snapshot)
}

func NewUnavailableSnapshot(sourceVersion string, capturedAt time.Time, state string) Snapshot {
	snapshot := Snapshot{Source: SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt, Observations: []Observation{}, Availability: []MetricAvailability{}}
	for _, metric := range Registry() {
		snapshot.Availability = append(snapshot.Availability, MetricAvailability{MetricKey: metric.Key, State: state, CheckedAt: capturedAt, Provenance: ProvenanceProvider})
	}
	return snapshot
}
