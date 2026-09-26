package activity

import (
	"math/big"
	"sort"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

// HistoricalMetric summarizes compatible retained local session measurements.
// Nil values mean no measurement; a pointer to zero means measured zero.
type HistoricalMetric struct {
	Source, SourceVersion                                      string
	Value, UnassignedValue                                     *string
	Availability                                               string
	SessionCount, MeasuredSessionCount, UnassignedSessionCount int64
	CoverageStartAt, CoverageEndAt                             time.Time
}

func HistoricalMetrics(records []TimelineRecord, scope, selectedID string) []HistoricalMetric {
	type group struct {
		metric            HistoricalMetric
		value, unassigned *big.Int
	}
	groups := map[string]*group{}
	for _, record := range records {
		if record.RecordType != RecordTypeObservedSession || record.Source != SourceLocalMetadata ||
			(scope != usage.ScopeOverallHistory && record.ProfileID == "") ||
			(scope == usage.ScopeSelectedProfile && record.ProfileID != selectedID) {
			continue
		}
		key := record.Source + "\x00" + record.SourceVersion
		current := groups[key]
		if current == nil {
			current = &group{metric: HistoricalMetric{Source: record.Source, SourceVersion: record.SourceVersion}, value: new(big.Int), unassigned: new(big.Int)}
			groups[key] = current
		}
		current.metric.SessionCount++
		if record.ProfileID == "" {
			current.metric.UnassignedSessionCount++
		}
		if current.metric.CoverageStartAt.IsZero() || record.StartedAt.Before(current.metric.CoverageStartAt) {
			current.metric.CoverageStartAt = record.StartedAt
		}
		if record.LastObservedAt.After(current.metric.CoverageEndAt) {
			current.metric.CoverageEndAt = record.LastObservedAt
		}
		if record.TokensUsed != nil {
			current.metric.MeasuredSessionCount++
			current.value.Add(current.value, big.NewInt(*record.TokensUsed))
			if record.ProfileID == "" {
				current.unassigned.Add(current.unassigned, big.NewInt(*record.TokensUsed))
				value := current.unassigned.String()
				current.metric.UnassignedValue = &value
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]HistoricalMetric, 0, len(keys))
	for _, key := range keys {
		current := groups[key]
		metric := current.metric
		switch {
		case metric.MeasuredSessionCount == 0:
			metric.Availability = "absent"
		case metric.MeasuredSessionCount < metric.SessionCount:
			metric.Availability = "partial"
		default:
			metric.Availability = "available"
		}
		if metric.MeasuredSessionCount > 0 {
			value := current.value.String()
			metric.Value = &value
		}
		result = append(result, metric)
	}
	return result
}
