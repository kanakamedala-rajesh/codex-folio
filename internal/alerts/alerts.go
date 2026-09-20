// Package alerts evaluates the bounded operational alert set from normalized
// usage and capability evidence. It does not collect provider data itself.
package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

const (
	DefaultWarningPercent  = 20
	DefaultCriticalPercent = 10
	StaleAfter             = 10 * time.Minute
	ResetApproachingWithin = usage.ResetApproachingLead
	RepeatedFailureCount   = 3
	HistoryLimit           = 200

	CategoryCapacity          = "capacity"
	CategoryReauthentication  = "reauthentication"
	CategoryStaleData         = "stale_data"
	CategoryCollectionFailure = "collection_failure"
	CategoryCompatibility     = "compatibility"

	KindCapacityWarning           = "capacity_warning"
	KindCapacityCritical          = "capacity_critical"
	KindCapacityExhausted         = "capacity_exhausted"
	KindResetApproaching          = "reset_approaching"
	KindCreditExpiryApproaching   = "credit_expiry_approaching"
	KindReauthentication          = "reauthentication_required"
	KindStaleEvidence             = "stale_evidence"
	KindRepeatedCollectionFailure = "repeated_collection_failure"
	KindCompatibilityChanged      = "compatibility_changed"

	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

type Threshold struct {
	ProfileID       string
	MetricKey       string
	WarningPercent  float64
	CriticalPercent float64
}

func (threshold Threshold) Valid() bool {
	if threshold.ProfileID == "" || threshold.WarningPercent <= threshold.CriticalPercent || threshold.CriticalPercent < 0 || threshold.WarningPercent > 100 {
		return false
	}
	return threshold.MetricKey == "codex.primary.used_percent" || threshold.MetricKey == "codex.secondary.used_percent"
}

type Evidence struct {
	ProfileID             string
	Alias                 string
	ProfileStatus         string
	Snapshot              usage.Snapshot
	PreviousSourceVersion string
	ConsecutiveFailures   int
	CreditExpiresAt       *time.Time
}

type Condition struct {
	Key                string
	ProfileID          string
	ProfileAlias       string
	Category           string
	Kind               string
	Severity           string
	Title              string
	Guidance           string
	MetricKey          string
	WindowStart        *time.Time
	WindowEnd          *time.Time
	RemainingPercent   *float64
	Source             string
	SourceVersion      string
	Provenance         string
	Scope              string
	Freshness          string
	AvailabilityReason string
	EvidenceCapturedAt time.Time
	ObservedAt         time.Time
}

// Evaluate returns only conditions supported by the supplied normalized
// evidence. Missing, partial, contradictory, or out-of-range values never
// become capacity/reset conditions.
func Evaluate(evidence Evidence, thresholds []Threshold, now time.Time) []Condition {
	now = now.UTC()
	result := make([]Condition, 0, 6)
	availability := make(map[string]string, len(evidence.Snapshot.Availability))
	for _, item := range evidence.Snapshot.Availability {
		availability[item.MetricKey] = item.State
		if item.State == usage.AvailabilityReauthenticationRequired {
			result = appendCondition(result, operationalCondition(evidence, CategoryReauthentication, KindReauthentication, SeverityError, "Reauthentication required", "Reauthenticate this Identity Profile with installed Codex, then refresh.", now))
		}
	}
	if evidence.ProfileStatus == "needs_reauthentication" {
		result = appendCondition(result, operationalCondition(evidence, CategoryReauthentication, KindReauthentication, SeverityError, "Reauthentication required", "Reauthenticate this Identity Profile with installed Codex, then refresh.", now))
	}
	for _, observation := range evidence.Snapshot.Observations {
		if observation.Metric.ValueKind != "percentage" || observation.Metric.Unit != "percent" || observation.Provenance != usage.ProvenanceProvider || observation.Availability != usage.AvailabilityAvailable || availability[observation.Metric.Key] != usage.AvailabilityAvailable || observation.Value < 0 || observation.Value > 100 || observation.WindowStart == nil || observation.WindowEnd == nil || !observation.WindowEnd.After(*observation.WindowStart) || now.Before(observation.WindowStart.UTC()) || !now.Before(observation.WindowEnd.UTC()) {
			continue
		}
		remaining := 100 - observation.Value
		threshold := thresholdFor(evidence.ProfileID, observation.Metric.Key, thresholds)
		kind, severity, title := "", "", ""
		switch {
		case remaining <= 0:
			kind, severity, title = KindCapacityExhausted, SeverityError, "Capacity exhausted"
		case remaining <= threshold.CriticalPercent:
			kind, severity, title = KindCapacityCritical, SeverityError, "Capacity at critical threshold"
		case remaining <= threshold.WarningPercent:
			kind, severity, title = KindCapacityWarning, SeverityWarning, "Capacity at warning threshold"
		}
		reason := availabilityReason(evidence.Snapshot.Availability, observation.Metric.Key)
		if kind != "" {
			value := remaining
			result = append(result, Condition{
				Key: conditionKey(evidence.ProfileID, kind, observation.Metric.Key, observation.WindowStart, observation.WindowEnd), ProfileID: evidence.ProfileID, ProfileAlias: evidence.Alias,
				Category: CategoryCapacity, Kind: kind, Severity: severity, Title: title, Guidance: "Review the reset time and choose an eligible Identity Profile before continuing.",
				MetricKey: observation.Metric.Key, WindowStart: utcPointer(observation.WindowStart), WindowEnd: utcPointer(observation.WindowEnd), RemainingPercent: &value,
				Source: observation.Source, SourceVersion: observation.SourceVersion, Provenance: observation.Provenance, Scope: observation.Metric.Scope, Freshness: observation.Freshness, AvailabilityReason: reason,
				EvidenceCapturedAt: observation.CapturedAt.UTC(), ObservedAt: observation.ObservedAt.UTC(),
			})
		}
		untilReset := observation.WindowEnd.UTC().Sub(now)
		if untilReset > 0 && untilReset <= ResetApproachingWithin {
			result = append(result, Condition{
				Key: conditionKey(evidence.ProfileID, KindResetApproaching, observation.Metric.Key, observation.WindowStart, observation.WindowEnd), ProfileID: evidence.ProfileID, ProfileAlias: evidence.Alias,
				Category: CategoryCapacity, Kind: KindResetApproaching, Severity: SeverityInfo, Title: "Capacity window resets soon", Guidance: "Refresh after the reported reset to confirm credited capacity.",
				MetricKey: observation.Metric.Key, WindowStart: utcPointer(observation.WindowStart), WindowEnd: utcPointer(observation.WindowEnd),
				Source: observation.Source, SourceVersion: observation.SourceVersion, Provenance: observation.Provenance, Scope: observation.Metric.Scope, Freshness: observation.Freshness, AvailabilityReason: reason,
				EvidenceCapturedAt: observation.CapturedAt.UTC(), ObservedAt: observation.ObservedAt.UTC(),
			})
		}
	}
	if evidence.CreditExpiresAt != nil {
		untilExpiry := evidence.CreditExpiresAt.UTC().Sub(now)
		if untilExpiry > 0 && untilExpiry <= ResetApproachingWithin {
			end := evidence.CreditExpiresAt.UTC()
			result = append(result, Condition{Key: conditionKey(evidence.ProfileID, KindCreditExpiryApproaching, "credits", nil, &end), ProfileID: evidence.ProfileID, ProfileAlias: evidence.Alias, Category: CategoryCapacity, Kind: KindCreditExpiryApproaching, Severity: SeverityWarning, Title: "Reset credit expires soon", Guidance: "Use supported reset credit before its provider-reported expiry.", WindowEnd: &end, Source: evidence.Snapshot.Source, SourceVersion: evidence.Snapshot.SourceVersion, Provenance: usage.ProvenanceProvider, EvidenceCapturedAt: evidence.Snapshot.CapturedAt.UTC(), ObservedAt: evidence.Snapshot.CapturedAt.UTC()})
		}
	}
	if !evidence.Snapshot.CapturedAt.IsZero() && now.Sub(evidence.Snapshot.CapturedAt.UTC()) > StaleAfter {
		result = appendCondition(result, operationalCondition(evidence, CategoryStaleData, KindStaleEvidence, SeverityWarning, "Usage evidence is stale", "Refresh this Identity Profile before relying on its capacity.", now))
	}
	if evidence.ConsecutiveFailures >= RepeatedFailureCount {
		result = appendCondition(result, operationalCondition(evidence, CategoryCollectionFailure, KindRepeatedCollectionFailure, SeverityError, "Collection has failed repeatedly", "Check Codex availability and authentication, then retry refresh.", now))
	}
	if evidence.PreviousSourceVersion != "" && evidence.Snapshot.SourceVersion != "" && evidence.PreviousSourceVersion != evidence.Snapshot.SourceVersion {
		result = appendCondition(result, operationalCondition(evidence, CategoryCompatibility, KindCompatibilityChanged, SeverityWarning, "Provider compatibility changed", "Review the new source version and refresh before relying on capacity guidance.", now))
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].Key < result[right].Key })
	return result
}

func thresholdFor(profileID, metricKey string, thresholds []Threshold) Threshold {
	result := Threshold{ProfileID: profileID, MetricKey: metricKey, WarningPercent: DefaultWarningPercent, CriticalPercent: DefaultCriticalPercent}
	for _, item := range thresholds {
		if item.ProfileID == profileID && item.MetricKey == metricKey && item.Valid() {
			return item
		}
	}
	return result
}

func operationalCondition(evidence Evidence, category, kind, severity, title, guidance string, now time.Time) Condition {
	reason := ""
	switch kind {
	case KindReauthentication:
		reason = usage.ReasonReauthentication
	case KindStaleEvidence:
		reason = usage.ReasonStale
	case KindRepeatedCollectionFailure:
		reason = usage.ReasonCollectionFailed
	case KindCompatibilityChanged:
		reason = "source_version_changed"
	}
	observedAt := evidence.Snapshot.CapturedAt.UTC()
	if observedAt.IsZero() {
		observedAt = now
	}
	return Condition{Key: conditionKey(evidence.ProfileID, kind, "", nil, nil), ProfileID: evidence.ProfileID, ProfileAlias: evidence.Alias, Category: category, Kind: kind, Severity: severity, Title: title, Guidance: guidance, Source: evidence.Snapshot.Source, SourceVersion: evidence.Snapshot.SourceVersion, AvailabilityReason: reason, EvidenceCapturedAt: evidence.Snapshot.CapturedAt.UTC(), ObservedAt: observedAt}
}

func availabilityReason(items []usage.MetricAvailability, metricKey string) string {
	for _, item := range items {
		if item.MetricKey == metricKey {
			return item.Reason
		}
	}
	return ""
}

func appendCondition(conditions []Condition, candidate Condition) []Condition {
	for _, existing := range conditions {
		if existing.Key == candidate.Key {
			return conditions
		}
	}
	return append(conditions, candidate)
}

func conditionKey(profileID, kind, metricKey string, start, end *time.Time) string {
	parts := []string{profileID, kind, metricKey}
	for _, value := range []*time.Time{start, end} {
		if value == nil {
			parts = append(parts, "")
		} else {
			parts = append(parts, value.UTC().Format(time.RFC3339Nano))
		}
	}
	return strings.Join(parts, "|")
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func (condition Condition) String() string {
	return fmt.Sprintf("%s:%s", condition.Category, condition.Kind)
}
