package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/usage"
)

type UsageCollector struct {
	appServer appServerRunner
}

func NewUsageCollector() *UsageCollector {
	return &UsageCollector{appServer: runAppServer}
}

func NewUsageCollectorWithCommandRunner(run CommandRunner) *UsageCollector {
	if run == nil {
		run = runCommand
	}
	return &UsageCollector{appServer: appServerWithCommandRunner(run)}
}

func (collector *UsageCollector) Collect(ctx context.Context, request usage.CollectionRequest) (usage.Snapshot, error) {
	if collector == nil || collector.appServer == nil || !filepath.IsAbs(request.Executable) || !filepath.IsAbs(request.IdentityHome) || strings.TrimSpace(request.SourceVersion) == "" || request.CapturedAt.IsZero() {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageRequestInvalid, usage.ErrInvalid)
	}
	environment := codexEnvironment(request.IdentityHome)
	accountOutput, err := collector.appServer(contextOrBackground(ctx), request.Executable, environment, `{"method":"account/read","id":2,"params":{"refreshToken":false}}`, 2)
	if err != nil {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	authenticated, err := parseUsageAccount(accountOutput)
	if err != nil {
		return usage.Snapshot{}, err
	}
	if !authenticated {
		return usage.NewUnavailableSnapshot(request.SourceVersion, request.CapturedAt.UTC(), usage.AvailabilityReauthenticationRequired), nil
	}
	rateOutput, err := collector.appServer(contextOrBackground(ctx), request.Executable, environment, `{"method":"account/rateLimits/read","id":3}`, 3)
	if err != nil {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	return normalizeRateLimits(rateOutput, request.SourceVersion, request.CapturedAt)
}

func parseUsageAccount(input []byte) (bool, error) {
	var response accountReadResponse
	if len(input) > 64*1024 || json.Unmarshal(bytes.TrimSpace(input), &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("2")) {
		return false, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	if len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
		return false, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	return response.Result != nil && len(response.Result.Account) > 0 && !bytes.Equal(bytes.TrimSpace(response.Result.Account), []byte("null")), nil
}

type rateLimitsResponse struct {
	ID     json.RawMessage `json:"id"`
	Result *struct {
		RateLimits struct {
			Primary   *rateLimitWindow `json:"primary"`
			Secondary *rateLimitWindow `json:"secondary"`
		} `json:"rateLimits"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

type rateLimitWindow struct {
	UsedPercent        *int   `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

func normalizeRateLimits(input []byte, sourceVersion string, capturedAt time.Time) (usage.Snapshot, error) {
	var response rateLimitsResponse
	if len(input) > 64*1024 || json.Unmarshal(bytes.TrimSpace(input), &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("3")) || response.Result == nil || sourceVersion == "" || capturedAt.IsZero() {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	if len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	snapshot := usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt.UTC(), Observations: []usage.Observation{}, Availability: []usage.MetricAvailability{}}
	windows := []*rateLimitWindow{response.Result.RateLimits.Primary, response.Result.RateLimits.Secondary}
	for index, metric := range usage.Registry() {
		availability := usage.MetricAvailability{MetricKey: metric.Key, State: usage.AvailabilityUnsupported, CheckedAt: snapshot.CapturedAt, Provenance: usage.ProvenanceProvider}
		window := windows[index]
		if window != nil {
			if window.UsedPercent == nil {
				availability.State = usage.AvailabilityTemporarilyUnavailable
				snapshot.Availability = append(snapshot.Availability, availability)
				continue
			}
			observation, err := normalizeWindow(metric, window, snapshot.CapturedAt)
			if err != nil {
				return usage.Snapshot{}, err
			}
			availability.State = usage.AvailabilityAvailable
			snapshot.Observations = append(snapshot.Observations, observation)
		}
		snapshot.Availability = append(snapshot.Availability, availability)
	}
	return snapshot, nil
}

func normalizeWindow(metric usage.Metric, window *rateLimitWindow, observedAt time.Time) (usage.Observation, error) {
	if window.UsedPercent == nil || *window.UsedPercent < 0 || *window.UsedPercent > 100 {
		return usage.Observation{}, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	observation := usage.Observation{Metric: metric, Value: float64(*window.UsedPercent), ObservedAt: observedAt, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}
	if window.WindowDurationMins == nil && window.ResetsAt == nil {
		return observation, nil
	}
	if window.WindowDurationMins == nil || window.ResetsAt == nil || *window.WindowDurationMins <= 0 || *window.WindowDurationMins > 10*365*24*60 || *window.ResetsAt <= 0 {
		return usage.Observation{}, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	windowEnd := time.Unix(*window.ResetsAt, 0).UTC()
	windowStart := windowEnd.Add(-time.Duration(*window.WindowDurationMins) * time.Minute)
	observation.WindowStart = &windowStart
	observation.WindowEnd = &windowEnd
	return observation, nil
}

var _ usage.Collector = (*UsageCollector)(nil)
