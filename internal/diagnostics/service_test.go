package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type serviceRepository struct {
	settings   Settings
	aggregates []Aggregate
}

func (repository *serviceRepository) DiagnosticSettings(context.Context) (Settings, error) {
	return repository.settings, nil
}

func (repository *serviceRepository) SetDiagnosticSettings(_ context.Context, settings Settings) (Settings, error) {
	repository.settings = settings
	return settings, nil
}

func (repository *serviceRepository) ListDiagnosticAggregates(context.Context) ([]Aggregate, error) {
	return append([]Aggregate(nil), repository.aggregates...), nil
}

func testBundleEnvironment() BundleEnvironment {
	return BundleEnvironment{
		ApplicationVersion:    "0.5.0-test",
		DatabaseSchemaVersion: 22,
		OSFamily:              OSLinux,
		Architecture:          ArchitectureAMD64,
		Features:              FeatureStates{Service: true},
		Health:                Health{Service: HealthHealthy, Database: HealthHealthy, Vault: HealthLocked, ErrorCode: apperrors.VaultLocked},
	}
}

func TestServicePreviewsAndExportsTheExactVersionedBundle(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	repository := &serviceRepository{
		settings: DefaultSettings(),
		aggregates: []Aggregate{{
			ID: AggregateID(Event{Component: ComponentStore, ErrorCode: apperrors.StoreReadFailed, Severity: SeverityError}), Component: ComponentStore, ErrorCode: apperrors.StoreReadFailed,
			Severity: SeverityError, OccurrenceCount: 2, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now,
		}},
	}
	environmentCalls := 0
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Clock:      &fixedClock{now: now},
		Environment: func(context.Context) (BundleEnvironment, error) {
			environmentCalls++
			result := testBundleEnvironment()
			result.Features.DetailedAlerts = environmentCalls == 2
			return result, nil
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	preview, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if preview.Bundle.SchemaVersion != BundleSchemaVersion || preview.Bundle.Settings != DefaultSettings() || preview.DiagnosticCount != 1 || preview.EncodedBytes <= 0 || preview.EncodedBytes > MaxBundleEncodedBytes || len(preview.ConfirmationDigest) != 64 {
		t.Fatalf("Preview() = %#v", preview)
	}
	if !reflect.DeepEqual(preview.Fields, []string{
		"schema_version", "generated_at", "settings", "environment.application_version", "environment.database_schema_version",
		"environment.os_family", "environment.architecture", "environment.features", "environment.health", "diagnostics",
	}) {
		t.Fatalf("Preview().Fields = %#v", preview.Fields)
	}
	exported, err := service.Export(context.Background(), preview.ConfirmationDigest)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if !reflect.DeepEqual(exported, preview.Bundle) {
		t.Fatalf("Export() = %#v, want exact preview %#v", exported, preview.Bundle)
	}

	second, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("second Preview() error = %v", err)
	}
	if !second.Bundle.Environment.Features.DetailedAlerts || preview.Bundle.Environment.Features.DetailedAlerts {
		t.Fatal("Preview() did not evaluate current environment state")
	}
}

func TestServiceRequiresCurrentConfirmationAndInvalidatesItOnSettingsChange(t *testing.T) {
	repository := &serviceRepository{settings: DefaultSettings()}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Clock:      &fixedClock{now: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)},
		Environment: func(context.Context) (BundleEnvironment, error) {
			return testBundleEnvironment(), nil
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	preview, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	for _, confirmation := range []string{"", "stale"} {
		if _, err := service.Export(context.Background(), confirmation); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfirmationInvalid {
			t.Fatalf("Export(%q) error = %v, want confirmation-invalid", confirmation, err)
		}
	}
	updated := Settings{Enabled: false, MinimumLevel: LevelError, RetentionDays: 7}
	if got, err := service.UpdateSettings(context.Background(), updated); err != nil || got != updated {
		t.Fatalf("UpdateSettings() = %#v, %v", got, err)
	}
	if _, err := service.Export(context.Background(), preview.ConfirmationDigest); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfirmationInvalid {
		t.Fatalf("Export(previous confirmation) error = %v, want confirmation-invalid", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Export(canceled, preview.ConfirmationDigest); err == nil || apperrors.Code(err) != apperrors.DiagnosticsExportFailed {
		t.Fatalf("Export(canceled) error = %v, want export-failed", err)
	}
}

func TestBundleShapeExcludesUnrestrictedOrPrivateContent(t *testing.T) {
	repository := &serviceRepository{settings: DefaultSettings()}
	service, err := NewService(ServiceOptions{
		Repository: repository,
		Clock:      &fixedClock{now: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)},
		Environment: func(context.Context) (BundleEnvironment, error) {
			return testBundleEnvironment(), nil
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	preview, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	encoded, err := json.Marshal(preview.Bundle)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{
		"identity", "workspace", "project", "canonical", "home_path", "usage", "session", "codex_content",
		"repository_content", "credential", "command_args", "message", "cause", "analytics", "checkpoint", "configuration",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("bundle %s contains forbidden field %q", encoded, forbidden)
		}
	}
}

func TestSettingsAndEnvironmentRejectValuesOutsideFixedVocabularies(t *testing.T) {
	for _, settings := range []Settings{
		{Enabled: true, MinimumLevel: "debug", RetentionDays: 14},
		{Enabled: true, MinimumLevel: LevelInfo, RetentionDays: 0},
		{Enabled: true, MinimumLevel: LevelInfo, RetentionDays: MaximumRetentionDays + 1},
	} {
		if err := settings.Validate(); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfigurationInvalid {
			t.Fatalf("Settings.Validate(%#v) error = %v", settings, err)
		}
	}
	environment := testBundleEnvironment()
	environment.ApplicationVersion = "version with unrestricted text"
	if err := environment.Validate(); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfigurationInvalid {
		t.Fatalf("BundleEnvironment.Validate() error = %v", err)
	}
	environment = testBundleEnvironment()
	environment.Health.ErrorCode = "UNREGISTERED"
	if err := environment.Validate(); err == nil {
		t.Fatal("BundleEnvironment.Validate() accepted an unregistered error code")
	}
}

func TestServicePropagatesEnvironmentFailureWithoutCachingAPreview(t *testing.T) {
	sentinel := errors.New("safe environment unavailable")
	service, err := NewService(ServiceOptions{
		Repository: &serviceRepository{settings: DefaultSettings()},
		Environment: func(context.Context) (BundleEnvironment, error) {
			return BundleEnvironment{}, sentinel
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Preview(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Preview() error = %v, want sentinel", err)
	}
	if _, err := service.Export(context.Background(), "confirmation"); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfirmationInvalid {
		t.Fatalf("Export() error = %v, want confirmation-invalid", err)
	}
}
