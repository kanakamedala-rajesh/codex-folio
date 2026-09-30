package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
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
		return usage.NewUnavailableSnapshot(request.SourceVersion, request.CapturedAt.UTC(), usage.AvailabilityReauthenticationRequired, usage.ReasonReauthentication), nil
	}
	rateOutput, err := collector.appServer(contextOrBackground(ctx), request.Executable, environment, `{"method":"account/rateLimits/read","id":3}`, 3)
	if err != nil {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	snapshot, err := normalizeRateLimits(rateOutput, request.SourceVersion, request.CapturedAt)
	if err != nil {
		return usage.Snapshot{}, err
	}
	collector.addLocalUsage(ctx, request, &snapshot)
	return snapshot, nil
}

func (*UsageCollector) addLocalUsage(ctx context.Context, request usage.CollectionRequest, snapshot *usage.Snapshot) {
	inspection, err := NewLocalActivityReader().Probe(contextOrBackground(ctx), request.IdentityHome)
	for index := range snapshot.Availability {
		availability := &snapshot.Availability[index]
		if availability.MetricKey != "codex.local.tokens_used" && availability.MetricKey != "codex.local.session_duration" {
			continue
		}
		availability.Provenance = usage.ProvenanceLocal
		if availability.MetricKey == "codex.local.session_duration" {
			// Thread timestamps bound coverage; they do not measure active use.
			availability.State, availability.Reason = usage.AvailabilityUnsupported, usage.ReasonUnsupported
			continue
		}
		if err != nil || inspection.Status == activity.SourceStatusUnavailable {
			availability.State, availability.Reason = usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed
			continue
		}
		switch inspection.Status {
		case activity.SourceStatusMissing:
			availability.State, availability.Reason = usage.AvailabilityTemporarilyUnavailable, usage.ReasonHistoryAbsent
		case activity.SourceStatusUnsupported:
			availability.State, availability.Reason = usage.AvailabilityUnsupported, usage.ReasonUnsupported
		case activity.SourceStatusSchemaInvalid:
			availability.State, availability.Reason = usage.AvailabilityTemporarilyUnavailable, usage.ReasonMalformedSource
		case activity.SourceStatusSupported:
			if inspection.SessionCount == 0 {
				availability.State, availability.Reason = usage.AvailabilityNoActivity, usage.ReasonNoActivity
			} else {
				// Source threads can belong to other profiles or Unassigned History.
				availability.State, availability.Reason = usage.AvailabilityTemporarilyUnavailable, usage.ReasonAttributionUnknown
			}
		}
	}
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
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

func normalizeRateLimits(input []byte, sourceVersion string, capturedAt time.Time) (usage.Snapshot, error) {
	var response rateLimitsResponse
	if len(input) > 64*1024 || json.Unmarshal(bytes.TrimSpace(input), &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("3")) || sourceVersion == "" || capturedAt.IsZero() {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	if len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageCollectionFailed, usage.ErrCollectionFailed)
	}
	if response.Result == nil {
		return usage.Snapshot{}, apperrors.New(apperrors.UsageSourceInvalid, usage.ErrSourceInvalid)
	}
	snapshot := usage.Snapshot{Source: usage.SourceCodexAppServer, SourceVersion: sourceVersion, CapturedAt: capturedAt.UTC(), Observations: []usage.Observation{}, Availability: []usage.MetricAvailability{}}
	windows := []*rateLimitWindow{response.Result.RateLimits.Primary, response.Result.RateLimits.Secondary}
	for index, metric := range usage.Registry() {
		availability := usage.MetricAvailability{MetricKey: metric.Key, State: usage.AvailabilityUnsupported, Reason: usage.ReasonUnsupported, CheckedAt: snapshot.CapturedAt, Provenance: metric.SourceClass}
		if index >= len(windows) {
			snapshot.Availability = append(snapshot.Availability, availability)
			continue
		}
		window := windows[index]
		if window != nil {
			if window.UsedPercent == nil {
				availability.State = usage.AvailabilityTemporarilyUnavailable
				availability.Reason = usage.ReasonCollectionFailed
				snapshot.Availability = append(snapshot.Availability, availability)
				continue
			}
			observation, err := normalizeWindow(metric, window, snapshot.CapturedAt)
			if err != nil {
				return usage.Snapshot{}, err
			}
			availability.State = usage.AvailabilityAvailable
			availability.Reason = ""
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
	observation := usage.Observation{Metric: metric, Value: *window.UsedPercent, ObservedAt: observedAt, CapturedAt: observedAt, Source: usage.SourceCodexAppServer, Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}
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
	observation.WindowTimezone = "UTC"
	return observation, nil
}

var _ usage.Collector = (*UsageCollector)(nil)
