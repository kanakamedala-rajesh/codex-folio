package main

import "testing"

func TestParseCollectionSettingsOptionsRequiresBoundedIntervals(t *testing.T) {
	options, err := parseCollectionSettingsOptions([]string{"collection", "--active-minutes", "5", "--idle-minutes", "45", "--json"})
	if err != nil {
		t.Fatalf("parseCollectionSettingsOptions() error = %v", err)
	}
	if options.activeMinutes == nil || *options.activeMinutes != 5 || options.idleMinutes == nil || *options.idleMinutes != 45 || !options.json {
		t.Fatalf("options = %#v", options)
	}

	for _, args := range [][]string{
		{},
		{"unknown"},
		{"collection", "--active-minutes", "4"},
		{"collection", "--idle-minutes", "1441"},
		{"collection", "--active-minutes", "5", "--active-minutes", "10"},
	} {
		if _, err := parseCollectionSettingsOptions(args); err == nil {
			t.Fatalf("parseCollectionSettingsOptions(%q) accepted invalid input", args)
		}
	}
}
