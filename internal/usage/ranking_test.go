package usage

import (
	"reflect"
	"testing"
	"time"
)

func TestRankRequiresUniqueFreshProviderCapacity(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	profiles := []ProfileTarget{{ID: "work", Alias: "Work", Eligible: true}, {ID: "personal", Alias: "Personal", Eligible: true}}
	snapshots := []Snapshot{rankingSnapshot("work", now, 10, 20), rankingSnapshot("personal", now, 30, 40)}
	candidates, recommended := Rank(profiles, snapshots, now, true)
	if recommended != "work" || len(candidates) != 2 || candidates[0].ProfileID != "work" || candidates[0].CapacityState != FreshnessFresh {
		t.Fatalf("ranking = %#v, recommended = %q", candidates, recommended)
	}

	snapshots[0] = rankingSnapshot("work", now.Add(-10*time.Minute), 10, 20)
	if _, recommended := Rank(profiles, snapshots, now, true); recommended != "work" {
		t.Fatalf("exact ten-minute boundary recommended = %q", recommended)
	}
	snapshots[0] = rankingSnapshot("work", now.Add(-10*time.Minute-time.Nanosecond), 10, 20)
	candidates, recommended = Rank(profiles, snapshots, now, true)
	if recommended != "personal" || candidates[1].ProfileID != "work" || candidates[1].CapacityState != FreshnessStale || !candidates[1].Eligible {
		t.Fatalf("stale candidate = %#v, recommended = %q", candidates, recommended)
	}
}

func TestRankWithholdsFalseWinners(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"tie", func(snapshot *Snapshot) { snapshot.Observations[0].Value, snapshot.Observations[1].Value = 30, 40 }},
		{"crossed windows", func(snapshot *Snapshot) { snapshot.Observations[1].Value = 50 }},
		{"missing capacity", func(snapshot *Snapshot) { snapshot.Observations = nil }},
		{"partial capacity", func(snapshot *Snapshot) { snapshot.Observations = snapshot.Observations[:1] }},
		{"unavailable metric", func(snapshot *Snapshot) { snapshot.Availability[0].State = AvailabilityTemporarilyUnavailable }},
		{"partial metric", func(snapshot *Snapshot) { snapshot.Availability[0].State = AvailabilityPartial }},
		{"unsupported metric", func(snapshot *Snapshot) { snapshot.Availability[0].State = AvailabilityUnsupported }},
		{"contradictory metric", func(snapshot *Snapshot) { snapshot.Availability[0].State = AvailabilityContradictory }},
		{"contradictory observation", func(snapshot *Snapshot) { snapshot.Observations[0].Availability = AvailabilityContradictory }},
		{"duplicate observations", func(snapshot *Snapshot) {
			snapshot.Observations = append(snapshot.Observations, snapshot.Observations[0])
		}},
		{"local evidence", func(snapshot *Snapshot) { snapshot.Observations[0].Provenance = ProvenanceLocal }},
		{"estimated evidence", func(snapshot *Snapshot) { snapshot.Observations[0].Provenance = ProvenanceEstimated }},
		{"session evidence", func(snapshot *Snapshot) { snapshot.Observations[0].Provenance = ProvenanceObserved }},
		{"incompatible unit", func(snapshot *Snapshot) { snapshot.Observations[0].Metric.Unit = "credits" }},
		{"incompatible scope", func(snapshot *Snapshot) { snapshot.Observations[0].Metric.Scope = "workspace" }},
		{"incompatible source", func(snapshot *Snapshot) { snapshot.Observations[0].Source = SourceLocalMetadata }},
		{"incompatible source version", func(snapshot *Snapshot) { snapshot.Observations[0].SourceVersion = "0.153.3" }},
		{"unknown window", func(snapshot *Snapshot) { snapshot.Observations[0].WindowStart = nil }},
		{"different window duration", func(snapshot *Snapshot) {
			start := snapshot.Observations[0].WindowStart.Add(time.Hour)
			snapshot.Observations[0].WindowStart = &start
		}},
		{"expired window", func(snapshot *Snapshot) { end := now; snapshot.Observations[0].WindowEnd = &end }},
		{"future capture", func(snapshot *Snapshot) { snapshot.Observations[0].CapturedAt = now.Add(time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			profiles := []ProfileTarget{{ID: "work", Alias: "Work", Eligible: true}, {ID: "personal", Alias: "Personal", Eligible: true}}
			snapshots := []Snapshot{rankingSnapshot("work", now, 10, 20), rankingSnapshot("personal", now, 30, 40)}
			test.change(&snapshots[0])
			candidates, recommended := Rank(profiles, snapshots, now, true)
			if recommended != "" || len(candidates) != 2 {
				t.Fatalf("ranking = %#v, false winner = %q", candidates, recommended)
			}
			reversed, otherWinner := Rank([]ProfileTarget{profiles[1], profiles[0]}, []Snapshot{snapshots[1], snapshots[0]}, now, true)
			if !reflect.DeepEqual(candidates, reversed) || otherWinner != recommended {
				t.Fatalf("input order changed policy: %#v/%q versus %#v/%q", candidates, recommended, reversed, otherWinner)
			}
		})
	}
}

func TestRankGatesBeforeCapacityAndKeepsUncertaintyVisible(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	profiles := []ProfileTarget{{ID: "work", Alias: "Work", Eligible: true}, {ID: "personal", Alias: "Personal", Eligible: true}}
	snapshots := []Snapshot{rankingSnapshot("work", now, 0, 0), rankingSnapshot("personal", now, 30, 40)}
	profiles[0].Eligible = false
	if candidates, winner := Rank(profiles, snapshots, now, true); winner != "personal" || candidates[1].Eligible {
		t.Fatalf("ineligible capacity won: %#v/%q", candidates, winner)
	}
	profiles[0].Eligible = true
	if candidates, winner := Rank(profiles, snapshots, now, false); winner != "" || candidates[0].Eligible || candidates[1].Eligible {
		t.Fatalf("incompatible capability won: %#v/%q", candidates, winner)
	}
	if _, winner := Rank(profiles[:1], snapshots[:1], now, true); winner != "work" {
		t.Fatalf("reported zero usage lost capacity: %q", winner)
	}
	if _, winner := Rank(profiles, nil, now, true); winner != "" {
		t.Fatalf("missing snapshots won: %q", winner)
	}
	if candidates, winner := Rank(nil, nil, now, true); len(candidates) != 0 || winner != "" {
		t.Fatalf("empty set = %#v/%q", candidates, winner)
	}
	snapshots[0] = rankingSnapshot("work", now, 100, 20)
	if _, winner := Rank(profiles[:1], snapshots[:1], now, true); winner != "" {
		t.Fatalf("exhausted window won: %q", winner)
	}
	for index := range snapshots {
		snapshots[index].LoginIdentity, snapshots[index].Workspace = "shared-login", "shared-workspace"
	}
	if candidates, winner := Rank(profiles, snapshots, now, true); winner != "" || candidates[0].CapacityState != AvailabilityContradictory || candidates[1].CapacityState != AvailabilityContradictory {
		t.Fatalf("conflicting shared scope = %#v/%q", candidates, winner)
	}
	for index := range snapshots {
		snapshots[index] = rankingSnapshot(profiles[index].ID, now.Add(-11*time.Minute), 0, 0)
	}
	if candidates, winner := Rank(profiles, snapshots, now, true); winner != "" || candidates[0].CapacityState != FreshnessStale || !candidates[0].Eligible {
		t.Fatalf("stale-only set = %#v/%q", candidates, winner)
	}
	snapshots[0] = rankingSnapshot("work", now.Add(-11*time.Minute), 0, 0)
	end := now.Add(-time.Minute)
	snapshots[0].Observations[0].WindowEnd = &end
	snapshots[1] = rankingSnapshot("personal", now, 30, 40)
	if candidates, winner := Rank(profiles, snapshots, now, true); winner != "personal" || candidates[1].CapacityState != FreshnessStale {
		t.Fatalf("stale expired window blocked fresh capacity: %#v/%q", candidates, winner)
	}
}

func rankingSnapshot(id string, capturedAt time.Time, primary, secondary float64) Snapshot {
	snapshot := Snapshot{ProfileID: id, CapturedAt: capturedAt, Source: SourceCodexAppServer, SourceVersion: "0.153.4"}
	for index, value := range []float64{primary, secondary} {
		start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
		end := start.Add(time.Duration(index+1) * 24 * time.Hour)
		metric := Registry()[index]
		snapshot.Observations = append(snapshot.Observations, Observation{
			Metric: metric, Value: value, CapturedAt: capturedAt, ObservedAt: capturedAt,
			WindowStart: &start, WindowEnd: &end, WindowTimezone: "UTC",
			Source: SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: ProvenanceProvider,
			Availability: AvailabilityAvailable, Freshness: FreshnessFresh,
		})
		snapshot.Availability = append(snapshot.Availability, MetricAvailability{MetricKey: metric.Key, State: AvailabilityAvailable, Provenance: ProvenanceProvider, CheckedAt: capturedAt})
	}
	return snapshot
}
