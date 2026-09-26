package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
)

type activityTestService struct {
	records  []activity.TimelineRecord
	filters  activity.Filters
	assigned activity.Assignment
}

func TestActivityResponsePreservesAssignedLegacyAttribution(t *testing.T) {
	response := ActivityResponseFor([]activity.TimelineRecord{{
		RecordType: activity.RecordTypeObservedSession, ProfileID: "personal", AttributionProvenance: "user_assigned",
		OriginalProfileID: "work", OriginalAttributionProvenance: "legacy_profile_observation",
	}})
	if len(response.Records) != 1 || response.Records[0].OriginalProfileId == nil || *response.Records[0].OriginalProfileId != "work" || response.Records[0].OriginalAttributionProvenance == nil || *response.Records[0].OriginalAttributionProvenance != "legacy_profile_observation" {
		t.Fatalf("assigned legacy response = %#v", response)
	}
}

func (service *activityTestService) Refresh(context.Context, string) ([]activity.TimelineRecord, error) {
	return append([]activity.TimelineRecord(nil), service.records...), nil
}

func (service *activityTestService) List(_ context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	service.filters = filters
	return append([]activity.TimelineRecord(nil), service.records...), nil
}

func (service *activityTestService) ReviewSources(context.Context) ([]activity.SourceReview, error) {
	return []activity.SourceReview{{SourceID: "home-1", Label: "Work", Status: activity.SourceStatusSupported, SessionCount: 1}}, nil
}

func (service *activityTestService) ImportSource(_ context.Context, sourceID string, consent bool) (activity.ImportResult, error) {
	if !consent || sourceID != "home-1" {
		return activity.ImportResult{}, activity.ErrActivityInvalid
	}
	return activity.ImportResult{ImportedCount: 1}, nil
}

func (service *activityTestService) Assign(_ context.Context, assignment activity.Assignment) error {
	service.assigned = assignment
	return nil
}

func TestActivityAPIsExposeSafeConsistentTimeline(t *testing.T) {
	tokens := int64(42)
	service := &activityTestService{records: []activity.TimelineRecord{{
		RecordType: activity.RecordTypeObservedSession, ID: "observed-1", SourceSessionID: "018f4f70-6f77-7c3f-9b77-93aa087dfc4d",
		ProfileID: "profile-1", ProfileAlias: "Work", ProjectID: "project-1", ProjectAlias: "Folio", ProjectBasename: "codex-folio",
		Source: activity.SourceLocalMetadata, SourceVersion: "0.150.1", Provenance: activity.ProvenanceObservedSession,
		StartedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), LastObservedAt: time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC),
		Model: "gpt-5", TokensUsed: &tokens, Correlation: activity.Correlation{State: activity.CorrelationUncorrelated},
	}}}
	server, _, _ := startTestServer(t, Options{Activities: service, CommandToken: "activity-token"})
	command := NewCommandClient(server.Origin(), "activity-token", nil)
	commandResult, err := command.Activity(context.Background(), CommandActivityRequest{Action: "list", ProfileAlias: "Work", ProjectID: "project-1"})
	if err != nil || len(commandResult.Records) != 1 {
		t.Fatalf("command Activity() = %#v, %v", commandResult, err)
	}
	review, err := command.Activity(context.Background(), CommandActivityRequest{Action: "review_sources"})
	if err != nil || len(review.Sources) != 1 || review.Sources[0].SourceID != "home-1" {
		t.Fatalf("command source review = %#v, %v", review, err)
	}
	if _, err := command.Activity(context.Background(), CommandActivityRequest{Action: "import_source", SourceID: "home-1"}); err == nil {
		t.Fatal("command import accepted without separate consent")
	}
	imported, err := command.Activity(context.Background(), CommandActivityRequest{Action: "import_source", SourceID: "home-1", Consent: true})
	if err != nil || imported.Import == nil || imported.Import.ImportedCount != 1 {
		t.Fatalf("command source import = %#v, %v", imported, err)
	}

	client := testClient(t)
	origin := server.Origin()
	unauthorized, err := doRequest(client, http.MethodPost, origin+ActivityAssignmentsPath, server.Address(), origin, []byte(`{"session_ids":["observed-1"],"profile_id":"profile-1"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized assignment = %d", unauthorized.StatusCode)
	}
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()
	result, response, err := NewClient(origin, client).GetActivity(context.Background(), "Work", "project-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	encoded, _ := json.Marshal(result)
	if len(result.Records) != 1 || result.Records[0].RecordType != activity.RecordTypeObservedSession || result.Records[0].TokensUsed != "42" {
		t.Fatalf("generated activity = %s", encoded)
	}
	if service.filters != (activity.Filters{ProfileAlias: "Work", ProjectID: "project-1"}) {
		t.Fatalf("filters = %#v", service.filters)
	}
	for _, forbidden := range []string{"/private/", "prompt sentinel", "response sentinel", "credential sentinel"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("generated activity contains prohibited value %q: %s", forbidden, encoded)
		}
	}
	sources, sourceResponse, err := NewClient(origin, client).GetActivitySources(context.Background())
	if err != nil || len(sources.Sources) != 1 || sources.Sources[0].Status != activity.SourceStatusSupported {
		t.Fatalf("sources = %#v, %v", sources, err)
	}
	_ = sourceResponse.Body.Close()
	for _, attempt := range []struct {
		body, csrf string
		status     int
	}{
		{`{"session_ids":["observed-1"],"profile_id":"profile-1"}`, "", http.StatusForbidden},
		{`{"session_ids":["observed-1"],"profile_id":"profile-1","extra":true}`, bootstrap.CSRFToken, http.StatusBadRequest},
		{`{"session_ids":["observed-1"],"profile_id":"profile-1"}`, bootstrap.CSRFToken, http.StatusOK},
	} {
		response, err := doRequest(client, http.MethodPost, origin+ActivityAssignmentsPath, server.Address(), origin, []byte(attempt.body), attempt.csrf)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != attempt.status {
			t.Fatalf("assignment response = %d, want %d", response.StatusCode, attempt.status)
		}
	}
	if len(service.assigned.SessionIDs) != 1 || service.assigned.ProfileID != "profile-1" {
		t.Fatalf("assignment = %#v", service.assigned)
	}
	for _, attempt := range []struct {
		body, csrf string
		status     int
	}{
		{`{"source_id":"home-1","consent":true}`, "", http.StatusForbidden},
		{`{"source_id":"home-1","consent":false}`, bootstrap.CSRFToken, http.StatusBadRequest},
		{`{"source_id":"home-1","consent":true}`, bootstrap.CSRFToken, http.StatusOK},
	} {
		result, err := doRequest(client, http.MethodPost, origin+ActivitySourcesPath, server.Address(), origin, []byte(attempt.body), attempt.csrf)
		if err != nil {
			t.Fatal(err)
		}
		_ = result.Body.Close()
		if result.StatusCode != attempt.status {
			t.Fatalf("import response status = %d, want %d", result.StatusCode, attempt.status)
		}
	}
}
