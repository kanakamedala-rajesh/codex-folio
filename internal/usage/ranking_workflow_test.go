package usage

import (
	"context"
	"testing"
	"time"
)

func TestViewUsesOneRankingAcrossScopesAndPreservesCaptureAge(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	state := &recordingStore{profiles: []ProfileTarget{
		{ID: "personal", Alias: "Personal", Eligible: true, Selected: true},
		{ID: "work", Alias: "Work", Eligible: true},
	}, snapshots: map[string]Snapshot{
		"personal": rankingSnapshot("personal", now.Add(-601*time.Second), 0, 0),
		"work":     rankingSnapshot("work", now, 30, 40),
	}}
	service, err := NewService(state, &recordingCollector{}, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	combined, err := service.View(context.Background(), ScopeCombinedIdentity, true)
	if err != nil || combined.RecommendedProfileID != "work" || combined.Profiles[0].ProfileID != "work" || combined.Profiles[1].Observations[0].CaptureAgeSeconds != 601 {
		t.Fatalf("combined view = %#v/%v", combined, err)
	}
	selected, err := service.View(context.Background(), "", true)
	if err != nil || selected.RecommendedProfileID != "" || len(selected.Candidates) != 1 || selected.Candidates[0].CapacityState != FreshnessStale || selected.EligibleProfileCount != 2 || selected.Profiles[0].Observations[0].CaptureAgeSeconds != 601 {
		t.Fatalf("selected view = %#v/%v", selected, err)
	}
	combined, err = service.View(context.Background(), ScopeCombinedIdentity, false)
	if err != nil || combined.RecommendedProfileID != "" || combined.EligibleProfileCount != 0 || len(combined.Profiles) != 2 {
		t.Fatalf("incompatible capability view = %#v/%v", combined, err)
	}
}
