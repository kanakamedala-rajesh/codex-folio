package codex

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

func TestUsageCollectorReadsDocumentedAccountAndRateLimitsThroughIdentityHome(t *testing.T) {
	t.Parallel()
	var requests []string
	var environments [][]string
	runner := func(_ context.Context, _ string, args, environment []string, stdin io.Reader, stdout, _ io.Writer) error {
		if len(args) != 2 || args[0] != "app-server" || args[1] != "--stdio" {
			t.Fatalf("args = %v", args)
		}
		input, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		requests = append(requests, string(input))
		environments = append(environments, environment)
		if bytes.Contains(input, []byte(`"method":"account/read"`)) {
			_, err = io.WriteString(stdout, `{"id":2,"result":{"account":{"type":"chatgpt"},"requiresOpenaiAuth":true}}`)
			return err
		}
		_, err = stdout.Write(readUsageFixture(t, "supported.json"))
		return err
	}
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	home := filepath.Join(t.TempDir(), "identity-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(home, localStateDatabase))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, created_at_ms INTEGER NOT NULL, updated_at_ms INTEGER NOT NULL, model TEXT, cwd TEXT NOT NULL, tokens_used INTEGER NOT NULL);
		INSERT INTO threads VALUES ('018f4f70-6f77-7c3f-9b77-93aa087dfc4d', 1788696000000, 1788696300000, 'gpt-5', ?, 42)`, home); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := NewUsageCollectorWithCommandRunner(runner).Collect(context.Background(), usage.CollectionRequest{
		Executable: filepath.Join(t.TempDir(), "codex"), IdentityHome: home, SourceVersion: "0.153.4", CapturedAt: capturedAt,
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(requests) != 2 || !strings.Contains(requests[0], `"method":"account/read"`) || !strings.Contains(requests[1], `"method":"account/rateLimits/read"`) {
		t.Fatalf("requests = %q", requests)
	}
	for _, environment := range environments {
		if !containsEnvironment(environment, "CODEX_HOME="+home) {
			t.Fatalf("environment does not contain intended Identity Home: %v", environment)
		}
	}
	if len(result.Observations) != len(usage.Registry()) || result.Observations[2].Metric.Key != "codex.local.tokens_used" || result.Observations[2].Value != 42 || result.Observations[3].Metric.Key != "codex.local.session_duration" || result.Observations[3].Value != 300 {
		t.Fatalf("observations = %#v", result.Observations)
	}
}

func TestNormalizeRateLimitsAcceptsOnlyRegisteredMetrics(t *testing.T) {
	t.Parallel()
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

	for _, fixture := range []string{"supported.json", "future-unknown.json"} {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			result, err := normalizeRateLimits(readUsageFixture(t, fixture), "0.153.4", capturedAt)
			if err != nil {
				t.Fatalf("normalizeRateLimits() error = %v", err)
			}
			if len(result.Observations) == 0 || len(result.Observations) > len(usage.Registry()) {
				t.Fatalf("observations = %d, want 1..%d", len(result.Observations), len(usage.Registry()))
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal normalized result: %v", err)
			}
			for _, prohibited := range []string{"must-not-survive", "futurePromptField", "newProviderField", "newBucket", "rawResponse", "transcript", "cookie"} {
				if strings.Contains(string(encoded), prohibited) {
					t.Fatalf("normalized result retained unknown field %q: %s", prohibited, encoded)
				}
			}
		})
	}
}

func TestNormalizeRateLimitsPreservesZeroWindowsAndAvailability(t *testing.T) {
	t.Parallel()
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	result, err := normalizeRateLimits(readUsageFixture(t, "future-unknown.json"), "0.153.4", capturedAt)
	if err != nil {
		t.Fatalf("normalizeRateLimits() error = %v", err)
	}
	if len(result.Observations) != 1 || result.Observations[0].Value != 0 {
		t.Fatalf("observations = %#v, want one supported zero", result.Observations)
	}
	observation := result.Observations[0]
	if observation.WindowStart == nil || observation.WindowEnd == nil || observation.WindowEnd.Sub(*observation.WindowStart) != 5*time.Hour {
		t.Fatalf("window = %v..%v, want provider five-hour window", observation.WindowStart, observation.WindowEnd)
	}
	if len(result.Availability) != len(usage.Registry()) || result.Availability[0].State != usage.AvailabilityAvailable {
		t.Fatalf("availability = %#v", result.Availability)
	}
}

func TestNormalizeRateLimitsMarksMissingMetricsUnsupported(t *testing.T) {
	t.Parallel()
	result, err := normalizeRateLimits(readUsageFixture(t, "missing.json"), "0.153.4", time.Now().UTC())
	if err != nil {
		t.Fatalf("normalizeRateLimits() error = %v", err)
	}
	if len(result.Observations) != 0 || len(result.Availability) != len(usage.Registry()) {
		t.Fatalf("result = %#v", result)
	}
	for _, availability := range result.Availability {
		if availability.State != usage.AvailabilityUnsupported {
			t.Fatalf("availability = %#v, want unsupported", availability)
		}
	}
}

func TestNormalizeRateLimitsRejectsMalformedRegisteredMetric(t *testing.T) {
	t.Parallel()
	if _, err := normalizeRateLimits(readUsageFixture(t, "malformed.json"), "0.153.4", time.Now().UTC()); err == nil {
		t.Fatal("normalizeRateLimits() accepted malformed registered metric")
	}
}

func TestNormalizeRateLimitsAcceptsFractionalPercentagesAndClassifiesRPCError(t *testing.T) {
	capturedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	result, err := normalizeRateLimits([]byte(`{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":25.5}}}}`), "0.153.4", capturedAt)
	if err != nil || len(result.Observations) != 1 || result.Observations[0].Value != 25.5 {
		t.Fatalf("fractional percentage = %#v/%v", result, err)
	}
	_, err = normalizeRateLimits([]byte(`{"id":3,"error":{"code":-32000,"message":"unavailable"}}`), "0.153.4", capturedAt)
	if !errors.Is(err, usage.ErrCollectionFailed) || apperrors.Code(err) != apperrors.UsageCollectionFailed {
		t.Fatalf("JSON-RPC error = %v", err)
	}
}

func TestNormalizeRateLimitsPreservesValidSiblingWhenPercentageMissingOrNull(t *testing.T) {
	t.Parallel()
	missing := readUsageFixture(t, "missing-null-percentages.json")
	for name, input := range map[string][]byte{
		"missing": missing,
		"null":    bytes.Replace(missing, []byte(`"primary":{}`), []byte(`"primary":{"usedPercent":null}`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := normalizeRateLimits(input, "0.153.4", time.Now().UTC())
			if err != nil {
				t.Fatalf("normalizeRateLimits() error = %v", err)
			}
			if len(result.Observations) != 1 || result.Observations[0].Metric.Key != usage.Registry()[1].Key || result.Observations[0].Value != 0 {
				t.Fatalf("observations = %#v, want valid zero-valued sibling", result.Observations)
			}
			if result.Availability[0].State != usage.AvailabilityTemporarilyUnavailable || result.Availability[1].State != usage.AvailabilityAvailable {
				t.Fatalf("availability = %#v", result.Availability)
			}
		})
	}
}

func readUsageFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "app-server", "v2", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return content
}
