package usage

import (
	"testing"
	"time"
)

func TestAnalyticsRetentionCutoffs(t *testing.T) {
	now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct{ setting, cutoff string }{
		{"13-months", "2025-02-28T12:00:00Z"},
		{"30", "2026-03-01T12:00:00Z"},
		{"365", "2025-03-31T12:00:00Z"},
		{"unlimited", ""},
	} {
		policy, err := ParseRetention(test.setting)
		if err != nil {
			t.Fatal(err)
		}
		cutoff := policy.Cutoff(now)
		if test.cutoff == "" {
			if cutoff != nil {
				t.Fatalf("unlimited cutoff = %v", cutoff)
			}
		} else if cutoff == nil || cutoff.Format(time.RFC3339) != test.cutoff {
			t.Fatalf("%s cutoff = %v, want %s", test.setting, cutoff, test.cutoff)
		}
	}
	for _, invalid := range []string{"", "29", "0", "-1", "1.5", "30000000000000000"} {
		if _, err := ParseRetention(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestHistoryBucketsAndExplicitScope(t *testing.T) {
	at := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	kind, start, end, zone, err := HistoryBucket(Observation{ObservedAt: at, WindowTimezone: "America/New_York"})
	if err != nil || kind != "calendar_day" || zone != "America/New_York" || start.Format(time.RFC3339) != "2026-03-08T05:00:00Z" || end.Format(time.RFC3339) != "2026-03-09T04:00:00Z" {
		t.Fatalf("DST calendar bucket: %s/%s/%s/%s/%v", kind, start, end, zone, err)
	}
	quotaStart, quotaEnd := at.Add(-3*time.Hour), at.Add(2*time.Hour)
	kind, start, end, zone, err = HistoryBucket(Observation{ObservedAt: at, WindowStart: &quotaStart, WindowEnd: &quotaEnd, WindowTimezone: "UTC"})
	if err != nil || kind != "source_window" || !start.Equal(quotaStart) || !end.Equal(quotaEnd) || zone != "UTC" {
		t.Fatal("provider window was rewritten")
	}
	valid := HistoryScope{ProfileID: "*", ProjectID: "*", From: "all", To: "all", Classes: []string{"usage", "aggregates"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HistoryScope){
		func(s *HistoryScope) { s.ProfileID = "" }, func(s *HistoryScope) { s.ProjectID = "" },
		func(s *HistoryScope) { s.From = "" }, func(s *HistoryScope) { s.To = "yesterday" },
		func(s *HistoryScope) { s.From = "2026-03-02T00:00:00Z"; s.To = "2026-03-01T00:00:00Z" },
		func(s *HistoryScope) { s.Classes = []string{"profiles"} }, func(s *HistoryScope) { s.Classes = []string{"usage", "usage"} },
		func(s *HistoryScope) { s.ProfileID = "work"; s.Classes = []string{"checkpoints"} },
	} {
		scope := valid
		mutate(&scope)
		if scope.Validate() == nil {
			t.Fatalf("accepted invalid scope: %#v", scope)
		}
	}
	changed := valid
	changed.Classes = []string{"aggregates", "usage"}
	if changed.Confirmation() != valid.Confirmation() {
		t.Fatal("class ordering changed confirmation")
	}
	changed.ProfileID = "work"
	if changed.Confirmation() == valid.Confirmation() {
		t.Fatal("confirmation did not bind profile scope")
	}
}
