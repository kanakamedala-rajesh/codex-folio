package configbundle

import (
	"bytes"
	"strings"
	"testing"
)

func validBundle() Bundle {
	return Bundle{SchemaVersion: SchemaVersion, Profiles: []Profile{{Alias: "Work", DisplayName: "Work"}}, ConfigurationPacks: []Pack{}, AlertThresholds: []Threshold{{ProfileAlias: "Work", MetricKey: "codex.primary.used_percent", WarningPercent: 20, CriticalPercent: 10}}, OperationalPreferences: Preferences{CollectionActiveSeconds: 300, CollectionIdleSeconds: 1800, Appearance: "system"}}
}

func TestDecodeIsStrictBoundedAndCanonical(t *testing.T) {
	b := validBundle()
	encoded, err := CanonicalJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := CanonicalJSON(decoded)
	if !bytes.Equal(encoded, again) {
		t.Fatalf("canonical encoding changed\n%s\n%s", encoded, again)
	}
	if _, err = Decode(append(encoded, []byte(" true")...)); err == nil {
		t.Fatal("accepted trailing value")
	}
	if _, err = Decode([]byte(`{"schema_version":1,"profiles":[],"configuration_packs":[],"alert_thresholds":[],"operational_preferences":{"collection_active_seconds":300,"collection_idle_seconds":1800,"appearance":"system"},"credential":"secret"}`)); err == nil {
		t.Fatal("accepted unknown sensitive field")
	}
	b.SchemaVersion = 2
	if Validate(b) == nil {
		t.Fatal("accepted unsupported schema")
	}
}

func TestValidateRequiresThresholdAliasAndSafeIntervals(t *testing.T) {
	b := validBundle()
	b.AlertThresholds[0].ProfileAlias = "Missing"
	if Validate(b) == nil {
		t.Fatal("accepted unresolved alias")
	}
	b = validBundle()
	b.OperationalPreferences.CollectionActiveSeconds = 1
	if Validate(b) == nil {
		t.Fatal("accepted unsafe interval")
	}
}

func TestValidateRejectsProfileDisplayAndProjectAliasesOutsideDomainLimits(t *testing.T) {
	b := validBundle()
	b.Profiles[0].DisplayName = string(bytes.Repeat([]byte("界"), 129))
	if Validate(b) == nil {
		t.Fatal("accepted profile display name over 128 runes")
	}
	b = validBundle()
	b.ProjectAliases = []ProjectAlias{{RepositoryBasename: "folio", Alias: string(bytes.Repeat([]byte("a"), 121))}}
	if Validate(b) == nil {
		t.Fatal("accepted Project Alias over 120 bytes")
	}
	b.ProjectAliases[0].Alias = "unsafe\nlabel"
	if Validate(b) == nil {
		t.Fatal("accepted control character in Project Alias")
	}
}

func TestFieldsReportsOnlyIncludedOptionalProjectAliases(t *testing.T) {
	b := validBundle()
	if strings.Contains(strings.Join(Fields(b), ","), "project_aliases") {
		t.Fatalf("empty optional field reported as included: %#v", Fields(b))
	}
	b.ProjectAliases = []ProjectAlias{{RepositoryBasename: "folio", Alias: "Folio"}}
	if !strings.Contains(strings.Join(Fields(b), ","), "project_aliases") {
		t.Fatalf("included optional field omitted: %#v", Fields(b))
	}
}
