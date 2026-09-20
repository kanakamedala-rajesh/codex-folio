package telemetry

import (
	"encoding/json"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func validEvent() EventV1 {
	return EventV1{
		SchemaVersion: SchemaVersion, AppVersion: "1.2.3", OSFamily: OSLinux,
		Architecture: ArchitectureAMD64, Feature: FeatureDashboard, Outcome: OutcomeSucceeded,
		DurationBucket: DurationUnderOneSecond, InstallationID: "00112233445566778899aabbccddeeff",
	}
}

func TestEventV1IsAnExactAllowlist(t *testing.T) {
	event := validEvent()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	want := Schema().Fields
	for _, field := range want {
		if field == "error_code" {
			continue
		}
		if _, ok := fields[field]; !ok {
			t.Fatalf("missing public field %q in %s", field, payload)
		}
	}
	if len(fields) != len(want)-1 {
		t.Fatalf("unexpected fields in public payload: %s", payload)
	}
	for _, forbidden := range []string{"identity", "workspace", "project", "path", "usage", "session", "content", "credential", "argument", "command", "raw", "payload"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("forbidden field %q entered payload: %s", forbidden, payload)
		}
	}
}

func TestPublicSchemaDescribesConsentAndRetentionContracts(t *testing.T) {
	schema := Schema()
	if schema.Version != SchemaVersion || schema.ConsentVersion != ConsentSchemaVersion ||
		schema.EventRetentionDays != 30 || schema.AggregateRetentionMonths != 13 {
		t.Fatalf("incomplete public schema: %+v", schema)
	}
}

func TestEventV1ValidationRejectsValuesOutsideSchema(t *testing.T) {
	tests := []struct {
		name string
		edit func(*EventV1)
	}{
		{"schema", func(event *EventV1) { event.SchemaVersion++ }},
		{"version", func(event *EventV1) { event.AppVersion = strings.Repeat("a", MaxAppVersionLength+1) }},
		{"os", func(event *EventV1) { event.OSFamily = "plan9" }},
		{"architecture", func(event *EventV1) { event.Architecture = "386" }},
		{"feature", func(event *EventV1) { event.Feature = "project-123" }},
		{"outcome", func(event *EventV1) { event.Outcome = "maybe" }},
		{"duration", func(event *EventV1) { event.DurationBucket = "123ms" }},
		{"installation ID", func(event *EventV1) { event.InstallationID = "user@example.com" }},
		{"error code format", func(event *EventV1) { event.Outcome, event.ErrorCode = OutcomeFailed, "raw error text" }},
		{"unregistered error namespace", func(event *EventV1) { event.Outcome, event.ErrorCode = OutcomeFailed, "NETWORK_FAILED" }},
		{"unregistered stable-looking error", func(event *EventV1) { event.Outcome, event.ErrorCode = OutcomeFailed, "CF_STABLE_ERROR" }},
		{"error on success", func(event *EventV1) { event.ErrorCode = "CF_ERROR" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			test.edit(&event)
			if event.Validate() == nil {
				t.Fatal("expected invalid event")
			}
		})
	}

	failed := validEvent()
	failed.Outcome = OutcomeFailed
	failed.ErrorCode = apperrors.CLIInternal
	if err := failed.Validate(); err != nil {
		t.Fatalf("valid failed outcome rejected: %v", err)
	}
}
