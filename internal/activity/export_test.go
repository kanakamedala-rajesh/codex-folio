package activity

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

func TestExportPreviewDescribesFilteredDatasets(t *testing.T) {
	usageRecords := []UsageExportRecord{{ProfileID: "profile-1"}}
	availabilityRecords := []AvailabilityExportRecord{{ProfileID: "profile-1"}}
	aggregateRecords := []AggregateExportRecord{{ProfileID: "profile-1"}, {ProfileID: "profile-2"}}
	repository := &exportRepositoryStub{records: ExportRecords{Usage: &usageRecords, Availability: &availabilityRecords, Aggregates: &aggregateRecords}}
	service := NewExportService(repository)
	request := ExportRequest{
		Format: "json", Datasets: []string{"usage", "aggregates"}, Scope: usage.ScopeCombinedIdentity,
		ProfileID: "*", ProjectID: "none", From: "2026-08-01T00:00:00Z", To: "2026-09-01T00:00:00Z",
	}
	result, err := service.Export(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repository.request, request) {
		t.Fatalf("repository request = %#v, want %#v", repository.request, request)
	}
	if result.SchemaVersion != ExportSchemaVersion || !reflect.DeepEqual(result.Filters, request) {
		t.Fatalf("export metadata = %#v", result)
	}
	if got := result.Preview; len(got) != 2 || got[0].Dataset != "usage" || got[0].RecordCount != 1 || got[1].Dataset != "aggregates" || got[1].RecordCount != 2 {
		t.Fatalf("preview = %#v", got)
	}
	for _, dataset := range result.Preview {
		if len(dataset.Fields) == 0 {
			t.Fatalf("dataset fields are empty: %#v", dataset)
		}
		for _, field := range dataset.Fields {
			if field == "canonical_path" {
				t.Fatal("ordinary preview disclosed canonical_path")
			}
		}
	}
	if !slices.Contains(result.Preview[0].Fields, "metric") || slices.Contains(result.Preview[0].Fields, "metric_key") {
		t.Fatalf("JSON fields = %#v", result.Preview[0].Fields)
	}

	request.IncludePaths = true
	result, err = service.Export(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Preview[0].Fields[len(result.Preview[0].Fields)-1:], []string{"canonical_path"}) {
		t.Fatalf("explicit path fields = %#v", result.Preview[0].Fields)
	}
}

func TestExportPreviewIncludesMetricAvailabilityWithoutInventingValues(t *testing.T) {
	records := []AvailabilityExportRecord{{ProfileID: "profile-1", State: usage.AvailabilityUnsupported}}
	service := NewExportService(&exportRepositoryStub{records: ExportRecords{Availability: &records}})
	result, err := service.Export(context.Background(), ExportRequest{Format: "csv", Datasets: []string{"availability"}, Scope: usage.ScopeSelectedProfile, ProfileID: "selected", ProjectID: "none", From: "all", To: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview[0].RecordCount != 1 || result.Preview[0].Dataset != "availability" {
		t.Fatalf("availability preview = %#v", result.Preview)
	}
	if !slices.Contains(result.Preview[0].Fields, "metric_key") || slices.Contains(result.Preview[0].Fields, "metric") {
		t.Fatalf("CSV fields = %#v", result.Preview[0].Fields)
	}
}

func TestCombinedExportPreservesLimitsAndDeduplicatesSharedEvidence(t *testing.T) {
	start, end := "2026-09-07T08:00:00Z", "2026-09-07T09:00:00Z"
	limit := usage.Registry()[0]
	tokens := usage.Registry()[2]
	records := []UsageExportRecord{
		{ObservationID: "limit-1", ProfileID: "profile-1", Metric: limit, Value: 25, WindowStart: start, WindowEnd: end},
		{ObservationID: "limit-2", ProfileID: "profile-2", Metric: limit, Value: 50, WindowStart: start, WindowEnd: end},
		{ObservationID: "tokens-1", ProfileID: "profile-1", LoginIdentity: "login-1", Metric: tokens, Value: 10, WindowStart: start, WindowEnd: end},
		{ObservationID: "tokens-2", ProfileID: "profile-2", LoginIdentity: "login-1", Metric: tokens, Value: 10, WindowStart: start, WindowEnd: end},
	}
	service := NewExportService(&exportRepositoryStub{records: ExportRecords{Usage: &records}})
	result, err := service.Export(context.Background(), ExportRequest{Format: "json", Datasets: []string{"usage"}, Scope: usage.ScopeCombinedIdentity, ProfileID: "*", ProjectID: "*", From: "all", To: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if got := *result.Records.Usage; len(got) != 3 || got[0].ObservationID != "limit-1" || got[1].ObservationID != "limit-2" || got[2].ObservationID != "tokens-1" {
		t.Fatalf("combined records = %#v", got)
	}
}

func TestExportRejectsAmbiguousCSVAndIncompleteFilters(t *testing.T) {
	service := NewExportService(&exportRepositoryStub{})
	valid := ExportRequest{Format: "csv", Datasets: []string{"activity"}, Scope: usage.ScopeSelectedProfile, ProfileID: "selected", ProjectID: "*", From: "all", To: "all"}
	for name, mutate := range map[string]func(*ExportRequest){
		"multiple CSV datasets": func(request *ExportRequest) { request.Datasets = append(request.Datasets, "usage") },
		"combined profile": func(request *ExportRequest) {
			request.Scope, request.ProfileID = usage.ScopeCombinedIdentity, "profile-1"
		},
		"selected wildcard": func(request *ExportRequest) { request.ProfileID = "*" },
		"missing project":   func(request *ExportRequest) { request.ProjectID = "" },
		"reversed dates": func(request *ExportRequest) {
			request.From, request.To = time.Now().UTC().Format(time.RFC3339), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			request.Datasets = append([]string(nil), valid.Datasets...)
			mutate(&request)
			if _, err := service.Export(context.Background(), request); err == nil {
				t.Fatal("Export() accepted invalid request")
			}
		})
	}
}

type exportRepositoryStub struct {
	request ExportRequest
	records ExportRecords
}

func (repository *exportRepositoryStub) ExportAnalytics(_ context.Context, request ExportRequest) (ExportRecords, error) {
	repository.request = request
	return repository.records, nil
}
