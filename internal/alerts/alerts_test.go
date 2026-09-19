package alerts

import (
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

func TestEvaluateCapacityThresholdsAndReset(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-4 * time.Hour)
	windowEnd := now.Add(20 * time.Minute)
	evidence := Evidence{
		ProfileID: "profile-1", Alias: "Work", ProfileStatus: "ready",
		Snapshot: usage.Snapshot{
			ProfileID: "profile-1", Alias: "Work", SourceVersion: "2.7.0", CapturedAt: now.Add(-time.Minute),
			Observations: []usage.Observation{{
				Metric: usage.Metric{Key: "codex.primary.used_percent", Unit: "percent", ValueKind: "percentage", SourceClass: usage.ProvenanceProvider, Scope: "provider_quota_window", Aggregation: "none"},
				Value:  91, CapturedAt: now.Add(-time.Minute), ObservedAt: now.Add(-time.Minute), WindowStart: &windowStart, WindowEnd: &windowEnd,
				Source: usage.SourceCodexAppServer, SourceVersion: "2.7.0", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable,
			}},
			Availability: []usage.MetricAvailability{{MetricKey: "codex.primary.used_percent", State: usage.AvailabilityAvailable, CheckedAt: now.Add(-time.Minute), Provenance: usage.ProvenanceProvider}},
		},
	}

	conditions := Evaluate(evidence, []Threshold{{ProfileID: "profile-1", MetricKey: "codex.primary.used_percent", WarningPercent: 20, CriticalPercent: 10}}, now)
	assertKinds(t, conditions, KindCapacityCritical, KindResetApproaching)
	if conditions[0].RemainingPercent == nil || *conditions[0].RemainingPercent != 9 {
		t.Fatalf("remaining = %#v, want 9", conditions[0].RemainingPercent)
	}
	for _, condition := range conditions {
		if condition.Scope != "provider_quota_window" || condition.Freshness != usage.FreshnessFresh {
			t.Fatalf("normalized evidence = %#v", condition)
		}
	}
}

func TestEvaluateSuppressesCapacityForMissingOrContradictoryEvidence(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	for _, state := range []string{usage.AvailabilityTemporarilyUnavailable, usage.AvailabilityContradictory, usage.AvailabilityPartial} {
		t.Run(state, func(t *testing.T) {
			conditions := Evaluate(Evidence{ProfileID: "profile-1", Alias: "Work", ProfileStatus: "ready", Snapshot: usage.Snapshot{
				CapturedAt: now, Availability: []usage.MetricAvailability{{MetricKey: "codex.primary.used_percent", State: state, CheckedAt: now}},
			}}, nil, now)
			for _, condition := range conditions {
				if condition.Category == CategoryCapacity {
					t.Fatalf("manufactured capacity condition from %q: %#v", state, condition)
				}
			}
		})
	}
}

func TestEvaluateOperationalConditions(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	conditions := Evaluate(Evidence{
		ProfileID: "profile-1", Alias: "Work", ProfileStatus: "needs_reauthentication",
		Snapshot:              usage.Snapshot{CapturedAt: now.Add(-11 * time.Minute), SourceVersion: "2.7.0"},
		PreviousSourceVersion: "2.6.0", ConsecutiveFailures: 3,
	}, nil, now)
	assertKinds(t, conditions, KindReauthentication, KindStaleEvidence, KindRepeatedCollectionFailure, KindCompatibilityChanged)
}

func TestEvaluateCreditExpiryOnlyWhenProviderReportsIt(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(20 * time.Minute)
	evidence := Evidence{
		ProfileID: "profile-1", Alias: "Work", ProfileStatus: "ready",
		Snapshot: usage.Snapshot{CapturedAt: now}, CreditExpiresAt: &expiresAt,
	}

	conditions := Evaluate(evidence, nil, now)
	assertKinds(t, conditions, KindCreditExpiryApproaching)

	evidence.CreditExpiresAt = nil
	conditions = Evaluate(evidence, nil, now)
	for _, condition := range conditions {
		if condition.Kind == KindCreditExpiryApproaching {
			t.Fatalf("manufactured credit expiry without provider evidence: %#v", conditions)
		}
	}
}

func TestEvaluateUsesDefaultThresholdsPerWindow(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-time.Hour)
	windowEnd := now.Add(time.Hour)
	evidence := Evidence{ProfileID: "profile-1", Alias: "Work", ProfileStatus: "ready", Snapshot: usage.Snapshot{
		CapturedAt: now, Observations: []usage.Observation{
			{Metric: usage.Metric{Key: "codex.primary.used_percent", Unit: "percent", ValueKind: "percentage"}, Value: 79, CapturedAt: now, WindowStart: &windowStart, WindowEnd: &windowEnd, Availability: usage.AvailabilityAvailable, Provenance: usage.ProvenanceProvider},
			{Metric: usage.Metric{Key: "codex.secondary.used_percent", Unit: "percent", ValueKind: "percentage"}, Value: 80, CapturedAt: now, WindowStart: &windowStart, WindowEnd: &windowEnd, Availability: usage.AvailabilityAvailable, Provenance: usage.ProvenanceProvider},
		}, Availability: []usage.MetricAvailability{
			{MetricKey: "codex.primary.used_percent", State: usage.AvailabilityAvailable},
			{MetricKey: "codex.secondary.used_percent", State: usage.AvailabilityAvailable},
		},
	}}
	conditions := Evaluate(evidence, nil, now)
	if len(conditions) != 1 || conditions[0].Kind != KindCapacityWarning || conditions[0].MetricKey != "codex.secondary.used_percent" {
		t.Fatalf("conditions = %#v", conditions)
	}
}

func assertKinds(t *testing.T, conditions []Condition, wants ...string) {
	t.Helper()
	got := make(map[string]bool, len(conditions))
	for _, condition := range conditions {
		got[condition.Kind] = true
	}
	for _, want := range wants {
		if !got[want] {
			t.Fatalf("conditions = %#v, missing %q", conditions, want)
		}
	}
}
