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
	records []activity.TimelineRecord
	filters activity.Filters
}

func (service *activityTestService) Refresh(context.Context, string) ([]activity.TimelineRecord, error) {
	return append([]activity.TimelineRecord(nil), service.records...), nil
}

func (service *activityTestService) List(_ context.Context, filters activity.Filters) ([]activity.TimelineRecord, error) {
	service.filters = filters
	return append([]activity.TimelineRecord(nil), service.records...), nil
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

	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
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
}
