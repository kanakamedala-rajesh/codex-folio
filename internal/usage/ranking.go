package usage

import (
	"sort"
	"strings"
	"time"
)

type Candidate struct {
	ProfileID     string `json:"profile_id"`
	Alias         string `json:"alias"`
	Eligible      bool   `json:"eligible"`
	CapacityState string `json:"capacity_state"`
}

// Rank compares only the registered provider quota windows. A winner must have
// at least as much remaining capacity in every window, and more in one.
// Presentation order never breaks a capacity tie or changes Selected Profile.
func Rank(profiles []ProfileTarget, snapshots []Snapshot, now time.Time, capabilityCompatible bool) ([]Candidate, string) {
	byProfile := make(map[string]Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byProfile[snapshot.ProfileID] = snapshot
	}
	candidates := make([]Candidate, 0, len(profiles))
	evidence := make([][]Observation, len(profiles))
	dominated := make([]int, len(profiles))
	uncertain := false
	for index, profile := range profiles {
		candidate := Candidate{ProfileID: profile.ID, Alias: profile.Alias, Eligible: profile.Eligible && capabilityCompatible && profile.ID != "", CapacityState: "ineligible"}
		if candidate.Eligible {
			evidence[index], candidate.CapacityState = providerCapacity(byProfile[profile.ID], now)
			if candidate.CapacityState != FreshnessFresh && candidate.CapacityState != FreshnessStale {
				uncertain = true
			}
		}
		candidates = append(candidates, candidate)
	}
	// ponytail: quadratic comparison for a local profile list; index comparable
	// source/window groups if profile inventories become large.
	for left := range profiles {
		if len(evidence[left]) == 0 {
			continue
		}
		for right := left + 1; right < len(profiles); right++ {
			if len(evidence[right]) == 0 {
				continue
			}
			leftBetter, rightBetter, compatible, contradictory := false, false, true, false
			leftScope, rightScope := byProfile[profiles[left].ID], byProfile[profiles[right].ID]
			shared := leftScope.LoginIdentity != "" && leftScope.Workspace != "" && leftScope.LoginIdentity == rightScope.LoginIdentity && leftScope.Workspace == rightScope.Workspace
			for index, a := range evidence[left] {
				b := evidence[right][index]
				if a.Metric != b.Metric || a.Source != b.Source || a.SourceVersion != b.SourceVersion || a.WindowTimezone != b.WindowTimezone || a.WindowEnd.Sub(*a.WindowStart) != b.WindowEnd.Sub(*b.WindowStart) {
					compatible = false
				}
				if shared && a.Source == b.Source && windowsOverlap(a, b) && a.Value != b.Value {
					contradictory = true
				}
				leftBetter = leftBetter || a.Value < b.Value
				rightBetter = rightBetter || b.Value < a.Value
			}
			if contradictory || !compatible {
				state := "incompatible"
				if contradictory {
					state = AvailabilityContradictory
				}
				for _, index := range []int{left, right} {
					if candidates[index].CapacityState != AvailabilityContradictory {
						candidates[index].CapacityState = state
					}
				}
				uncertain = true
			} else if leftBetter && !rightBetter {
				dominated[right]++
			} else if rightBetter && !leftBetter {
				dominated[left]++
			}
		}
	}
	recommended, winners := "", 0
	for index, candidate := range candidates {
		if candidate.CapacityState == FreshnessFresh && dominated[index] == 0 {
			winners++
			recommended = candidate.ProfileID
			for _, observation := range evidence[index] {
				if observation.Value == 100 {
					uncertain = true
				}
			}
		}
	}
	if uncertain || winners != 1 {
		recommended = ""
	}
	order := make(map[string]int, len(candidates))
	for index, candidate := range candidates {
		order[candidate.ProfileID] = dominated[index]
	}
	sort.Slice(candidates, func(left, right int) bool {
		a, b := candidates[left], candidates[right]
		group := func(candidate Candidate) int {
			if !candidate.Eligible {
				return 3
			}
			if candidate.CapacityState == FreshnessFresh {
				return 0
			}
			if candidate.CapacityState == FreshnessStale {
				return 1
			}
			return 2
		}
		if group(a) != group(b) {
			return group(a) < group(b)
		}
		if a.CapacityState == FreshnessFresh && b.CapacityState == FreshnessFresh && order[a.ProfileID] != order[b.ProfileID] {
			return order[a.ProfileID] < order[b.ProfileID]
		}
		if strings.ToLower(a.Alias) != strings.ToLower(b.Alias) {
			return strings.ToLower(a.Alias) < strings.ToLower(b.Alias)
		}
		return a.ProfileID < b.ProfileID
	})
	return candidates, recommended
}

func providerCapacity(snapshot Snapshot, now time.Time) ([]Observation, string) {
	capacity := make([]Observation, 0, 2)
	stale := false
	for _, metric := range Registry() {
		if metric.SourceClass != ProvenanceProvider {
			continue
		}
		state := ""
		for _, availability := range snapshot.Availability {
			if availability.MetricKey == metric.Key {
				if availability.State == AvailabilityContradictory {
					return nil, AvailabilityContradictory
				}
				state = availability.State
			}
		}
		if state != AvailabilityAvailable && state != AvailabilityStale {
			return nil, AvailabilityPartial
		}
		var found *Observation
		for _, observation := range snapshot.Observations {
			if observation.Metric.Key != metric.Key {
				continue
			}
			if observation.Availability == AvailabilityContradictory {
				return nil, AvailabilityContradictory
			}
			if observation.Provenance != ProvenanceProvider {
				continue
			}
			if found != nil {
				return nil, AvailabilityContradictory
			}
			if observation.Metric != metric || observation.Source != SourceCodexAppServer || observation.SourceVersion == "" || observation.WindowStart == nil || observation.WindowEnd == nil || !observation.WindowStart.Before(*observation.WindowEnd) {
				return nil, "incompatible"
			}
			if observation.CapturedAt.IsZero() || now.Before(observation.CapturedAt) || !(observation.Value >= 0 && observation.Value <= 100) || (observation.Availability != AvailabilityAvailable && observation.Availability != AvailabilityStale) {
				return nil, AvailabilityPartial
			}
			stale = stale || now.Sub(observation.CapturedAt) > freshnessLimit || state == AvailabilityStale || observation.Availability == AvailabilityStale
			found = &observation
		}
		if found == nil {
			return nil, AvailabilityPartial
		}
		capacity = append(capacity, *found)
	}
	if stale {
		return nil, FreshnessStale
	}
	for _, observation := range capacity {
		if now.Before(*observation.WindowStart) || !now.Before(*observation.WindowEnd) {
			return nil, "incompatible"
		}
	}
	return capacity, FreshnessFresh
}
