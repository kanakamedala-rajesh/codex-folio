// Package usage owns normalized Usage Snapshot semantics.
package usage

import (
	"context"
	"errors"
	"sort"
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
	AggregationSum          = "sum"
	ScopeSelectedProfile    = "selected_profile"
	ScopeCombinedIdentity   = "combined_identity"

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
	{Key: "codex.local.tokens_used", ValueKind: "count", Unit: "tokens", SourceClass: ProvenanceLocal, Scope: "login_identity", Aggregation: AggregationSum},
	{Key: "codex.local.session_duration", ValueKind: "duration", Unit: "seconds", SourceClass: ProvenanceLocal, Scope: "workspace", Aggregation: AggregationSum},
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
	LoginIdentity string               `json:"-"`
	Workspace     string               `json:"-"`
}

type Aggregate struct {
	Metric       Metric  `json:"metric"`
	Value        float64 `json:"value"`
	ProfileCount int     `json:"profile_count"`
}

type DashboardView struct {
	Scope                string      `json:"scope"`
	EligibleProfileCount int         `json:"eligible_profile_count"`
	Profiles             []Snapshot  `json:"profiles"`
	Aggregates           []Aggregate `json:"aggregates"`
	AmbiguousMetricKeys  []string    `json:"ambiguous_metric_keys"`
}

func Combine(snapshots []Snapshot, eligibleProfileCount int) DashboardView {
	return combineWithRegistry(snapshots, eligibleProfileCount, Registry())
}

func combineWithRegistry(snapshots []Snapshot, eligibleProfileCount int, metrics []Metric) DashboardView {
	view := DashboardView{Scope: ScopeCombinedIdentity, EligibleProfileCount: eligibleProfileCount, Profiles: append([]Snapshot(nil), snapshots...), Aggregates: []Aggregate{}, AmbiguousMetricKeys: []string{}}
	registry := make(map[string]Metric, len(metrics))
	for _, metric := range metrics {
		registry[metric.Key] = metric
	}
	type evidence struct {
		observation Observation
		scopeKey    string
	}
	grouped := make(map[string][]evidence)
	seen := make(map[string][]evidence)
	profiles := make(map[string]map[string]bool)
	ambiguous := make(map[string]bool)
	for _, snapshot := range snapshots {
		for _, observation := range snapshot.Observations {
			metric, ok := registry[observation.Metric.Key]
			if !ok || metric != observation.Metric || metric.Aggregation != AggregationSum || metric.ValueKind == "percentage" {
				continue
			}
			scopeKey := "profile:" + snapshot.ProfileID
			switch metric.Scope {
			case "login_identity":
				if snapshot.LoginIdentity != "" {
					scopeKey = "login_identity:" + snapshot.LoginIdentity
				}
			case "workspace":
				if snapshot.Workspace != "" {
					scopeKey = "workspace:" + snapshot.Workspace
				}
			}
			duplicate := false
			for _, prior := range seen[metric.Key] {
				if prior.scopeKey != scopeKey || !windowsOverlap(prior.observation, observation) {
					continue
				}
				if prior.observation.Value != observation.Value {
					ambiguous[metric.Key] = true
				}
				duplicate = true
			}
			seen[metric.Key] = append(seen[metric.Key], evidence{observation: observation, scopeKey: scopeKey})
			if duplicate {
				continue
			}
			grouped[metric.Key] = append(grouped[metric.Key], evidence{observation: observation, scopeKey: scopeKey})
			if profiles[metric.Key] == nil {
				profiles[metric.Key] = make(map[string]bool)
			}
			profiles[metric.Key][snapshot.ProfileID] = true
		}
	}
	for key, items := range grouped {
		if ambiguous[key] {
			view.AmbiguousMetricKeys = append(view.AmbiguousMetricKeys, key)
			continue
		}
		var value float64
		for _, item := range items {
			value += item.observation.Value
		}
		view.Aggregates = append(view.Aggregates, Aggregate{Metric: registry[key], Value: value, ProfileCount: len(profiles[key])})
	}
	sort.Slice(view.Aggregates, func(left, right int) bool {
		return view.Aggregates[left].Metric.Key < view.Aggregates[right].Metric.Key
	})
	sort.Strings(view.AmbiguousMetricKeys)
	return view
}

func windowsOverlap(left, right Observation) bool {
	if left.WindowStart == nil || left.WindowEnd == nil || right.WindowStart == nil || right.WindowEnd == nil {
		return true
	}
	return left.WindowStart.Before(*right.WindowEnd) && right.WindowStart.Before(*left.WindowEnd)
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
	ID            string
	Alias         string
	IdentityHome  string
	LoginIdentity string
	Workspace     string
	Selected      bool
	Eligible      bool
}

type Store interface {
	ResolveUsageProfile(context.Context, string) (ProfileTarget, error)
	ListUsageProfiles(context.Context) ([]ProfileTarget, error)
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

func (service *Service) View(ctx context.Context, scope string) (DashboardView, error) {
	if service == nil || service.store == nil || service.clock == nil || (scope != "" && scope != ScopeSelectedProfile && scope != ScopeCombinedIdentity) {
		return DashboardView{}, ErrInvalid
	}
	profiles, err := service.store.ListUsageProfiles(ctx)
	if err != nil {
		return DashboardView{}, err
	}
	now := service.clock.Now().UTC()
	if now.IsZero() {
		return DashboardView{}, ErrInvalid
	}
	eligible := 0
	for _, target := range profiles {
		eligible += boolCount(target.Eligible)
	}
	if scope == "" || scope == ScopeSelectedProfile {
		for _, target := range profiles {
			if !target.Selected {
				continue
			}
			snapshot, err := service.store.LatestUsageSnapshot(ctx, target)
			if err != nil {
				return DashboardView{}, err
			}
			if err := finalizeSnapshot(&snapshot, now); err != nil {
				return DashboardView{}, err
			}
			return DashboardView{Scope: ScopeSelectedProfile, EligibleProfileCount: eligible, Profiles: []Snapshot{snapshot}, Aggregates: []Aggregate{}, AmbiguousMetricKeys: []string{}}, nil
		}
		return DashboardView{}, ErrProfileUnavailable
	}

	snapshots := make([]Snapshot, 0, len(profiles))
	for _, target := range profiles {
		snapshot, err := service.store.LatestUsageSnapshot(ctx, target)
		if errors.Is(err, ErrProfileUnavailable) {
			continue
		}
		if err != nil {
			return DashboardView{}, err
		}
		if err := finalizeSnapshot(&snapshot, now); err != nil {
			return DashboardView{}, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return Combine(snapshots, eligible), nil
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func NewUnavailableSnapshot(sourceVersion string, capturedAt time.Time, state, reason string) Snapshot {
	snapshot := Snapshot{Source: SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt, Status: state, Observations: []Observation{}, Availability: []MetricAvailability{}}
	for _, metric := range Registry() {
		snapshot.Availability = append(snapshot.Availability, MetricAvailability{MetricKey: metric.Key, State: state, Reason: reason, CheckedAt: capturedAt, Provenance: metric.SourceClass})
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
