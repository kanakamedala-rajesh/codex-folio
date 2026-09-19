package alerts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	StateOpen         = "open"
	StateAcknowledged = "acknowledged"
	StateResolved     = "resolved"

	DeliveryPending     = "pending"
	DeliveryAttempting  = "attempting"
	DeliveryDelivered   = "delivered"
	DeliveryFailed      = "failed"
	DeliveryUnavailable = "unavailable"

	DeliveryErrorCode   = apperrors.AlertNotificationFailed
	DeliveryLimit       = 16
	DeliveryMaxAttempts = 3

	deliveryEvaluationBudget = 9 * time.Second
	deliveryAttemptBudget    = 4 * time.Second
	deliveryRecordReserve    = time.Second
)

var ErrInvalid = errors.New("alert request is invalid")

type Record struct {
	ID string
	Condition
	State                 string
	FirstSeenAt           time.Time
	LastSeenAt            time.Time
	AcknowledgedAt        *time.Time
	ResolvedAt            *time.Time
	OccurrenceCount       int
	DeliveryState         string
	DeliveryAttempts      int
	LastDeliveryAttemptAt *time.Time
	NextDeliveryAttemptAt *time.Time
	DeliveredAt           *time.Time
	DeliveryErrorCode     string
}

type NotificationPreference struct {
	DetailEnabled bool
	UpdatedAt     time.Time
}

type DeliveryOutcome struct {
	State         string
	Attempts      int
	AttemptedAt   time.Time
	NextAttemptAt *time.Time
	DeliveredAt   *time.Time
	ErrorCode     string
}

type Notification struct {
	Title string
	Body  string
}

type AdapterHealth struct {
	Available bool
	Mechanism string
	Detail    string
}

type NotificationAdapter interface {
	Health(context.Context) AdapterHealth
	Deliver(context.Context, Notification) error
}

type DeliveryOptions struct {
	Enabled bool
	Adapter NotificationAdapter
}

type DeliveryHealth struct {
	Dashboard     string
	Native        string
	Mechanism     string
	Detail        string
	DetailEnabled bool
}

type Repository interface {
	AlertEvidence(context.Context, string) ([]Evidence, error)
	AlertThresholds(context.Context) ([]Threshold, error)
	SyncAlerts(context.Context, string, []Condition, time.Time, int) error
	ListAlerts(context.Context, int) ([]Record, error)
	AcknowledgeAlert(context.Context, string, time.Time) error
	SetAlertThreshold(context.Context, Threshold, time.Time) error
	NotificationPreference(context.Context) (NotificationPreference, error)
	SetNotificationDetail(context.Context, bool, time.Time) (NotificationPreference, error)
	ClaimAlertDelivery(context.Context, string, time.Time) (bool, error)
	RecordAlertDelivery(context.Context, string, DeliveryOutcome) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository            Repository
	clock                 Clock
	delivery              DeliveryOptions
	deliveryBudget        time.Duration
	deliveryAttemptBudget time.Duration
	deliveryRecordReserve time.Duration
}

func NewService(repository Repository, clock Clock, delivery ...DeliveryOptions) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, ErrInvalid
	}
	options := DeliveryOptions{}
	if len(delivery) > 0 {
		options = delivery[0]
	}
	if options.Enabled && options.Adapter == nil {
		return nil, ErrInvalid
	}
	return &Service{
		repository:            repository,
		clock:                 clock,
		delivery:              options,
		deliveryBudget:        deliveryEvaluationBudget,
		deliveryAttemptBudget: deliveryAttemptBudget,
		deliveryRecordReserve: deliveryRecordReserve,
	}, nil
}

func (service *Service) Evaluate(ctx context.Context, profileID string) ([]Record, error) {
	if service == nil || service.repository == nil || service.clock == nil {
		return nil, ErrInvalid
	}
	evidence, err := service.repository.AlertEvidence(ctx, profileID)
	if err != nil {
		return nil, err
	}
	thresholds, err := service.repository.AlertThresholds(ctx)
	if err != nil {
		return nil, err
	}
	now := service.clock.Now().UTC()
	conditions := make([]Condition, 0)
	for _, item := range evidence {
		conditions = append(conditions, Evaluate(item, thresholds, now)...)
	}
	if err := service.repository.SyncAlerts(ctx, profileID, conditions, now, HistoryLimit); err != nil {
		return nil, err
	}
	records, err := service.repository.ListAlerts(ctx, HistoryLimit)
	if err != nil {
		return nil, err
	}
	service.deliver(ctx, records, now)
	return service.repository.ListAlerts(ctx, HistoryLimit)
}

func (service *Service) Acknowledge(ctx context.Context, id string) ([]Record, error) {
	if service == nil || service.repository == nil || id == "" {
		return nil, ErrInvalid
	}
	if err := service.repository.AcknowledgeAlert(ctx, id, service.clock.Now().UTC()); err != nil {
		return nil, err
	}
	return service.repository.ListAlerts(ctx, HistoryLimit)
}

func (service *Service) SetThreshold(ctx context.Context, threshold Threshold) ([]Threshold, error) {
	if service == nil || service.repository == nil || !threshold.Valid() {
		return nil, ErrInvalid
	}
	if err := service.repository.SetAlertThreshold(ctx, threshold, service.clock.Now().UTC()); err != nil {
		return nil, err
	}
	return service.repository.AlertThresholds(ctx)
}

func (service *Service) Thresholds(ctx context.Context) ([]Threshold, error) {
	if service == nil || service.repository == nil {
		return nil, ErrInvalid
	}
	return service.repository.AlertThresholds(ctx)
}

func (service *Service) SetNotificationDetail(ctx context.Context, enabled bool) (NotificationPreference, error) {
	if service == nil || service.repository == nil || service.clock == nil {
		return NotificationPreference{}, ErrInvalid
	}
	return service.repository.SetNotificationDetail(ctx, enabled, service.clock.Now().UTC())
}

func (service *Service) DeliveryHealth(ctx context.Context) (DeliveryHealth, error) {
	if service == nil || service.repository == nil {
		return DeliveryHealth{}, ErrInvalid
	}
	preference, err := service.repository.NotificationPreference(ctx)
	if err != nil {
		return DeliveryHealth{}, err
	}
	health := DeliveryHealth{
		Dashboard:     "available",
		Native:        "not_enrolled",
		Detail:        "Alerts remain available in CodexFolio. Native delivery requires explicit service enrollment.",
		DetailEnabled: preference.DetailEnabled,
	}
	if !service.delivery.Enabled {
		return health, nil
	}
	adapter := service.delivery.Adapter.Health(ctx)
	health.Mechanism = adapter.Mechanism
	if !adapter.Available {
		health.Native = DeliveryUnavailable
		health.Detail = adapter.Detail
		return health, nil
	}
	health.Native = "available"
	health.Detail = adapter.Detail
	records, err := service.repository.ListAlerts(ctx, HistoryLimit)
	if err != nil {
		return DeliveryHealth{}, err
	}
	for _, record := range records {
		if record.State == StateResolved {
			continue
		}
		if record.DeliveryState == DeliveryFailed || record.DeliveryState == DeliveryAttempting {
			health.Native = DeliveryFailed
			health.Detail = "Native notification delivery failed. Alerts remain available here and delivery retries stay bounded."
			break
		}
	}
	return health, nil
}

func (service *Service) deliver(ctx context.Context, records []Record, now time.Time) {
	if !service.delivery.Enabled || service.delivery.Adapter == nil {
		return
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, service.deliveryBudget)
	defer cancel()
	preference, err := service.repository.NotificationPreference(deliveryCtx)
	if err != nil {
		return
	}
	adapterHealth := service.delivery.Adapter.Health(deliveryCtx)
	processed := 0
	for _, record := range records {
		if processed >= DeliveryLimit || record.State != StateOpen || record.DeliveryAttempts >= DeliveryMaxAttempts || !deliveryDue(record, now) {
			continue
		}
		deadline, hasDeadline := deliveryCtx.Deadline()
		if deliveryCtx.Err() != nil || (hasDeadline && time.Until(deadline) <= service.deliveryAttemptBudget+service.deliveryRecordReserve) {
			break
		}
		if !adapterHealth.Available {
			next := now.Add(30 * time.Minute)
			_ = service.repository.RecordAlertDelivery(deliveryCtx, record.ID, DeliveryOutcome{State: DeliveryUnavailable, Attempts: record.DeliveryAttempts, AttemptedAt: now, NextAttemptAt: &next, ErrorCode: DeliveryErrorCode})
			processed++
			continue
		}
		claimed, claimErr := service.repository.ClaimAlertDelivery(deliveryCtx, record.ID, now)
		if claimErr != nil || !claimed {
			continue
		}
		attempts := record.DeliveryAttempts + 1
		notification := notificationFor(record, preference.DetailEnabled)
		attemptCtx, attemptCancel := context.WithTimeout(deliveryCtx, service.deliveryAttemptBudget)
		err := service.delivery.Adapter.Deliver(attemptCtx, notification)
		attemptCancel()
		outcome := DeliveryOutcome{State: DeliveryDelivered, Attempts: attempts, AttemptedAt: now, DeliveredAt: &now}
		if err != nil {
			outcome.State = DeliveryFailed
			outcome.DeliveredAt = nil
			outcome.ErrorCode = DeliveryErrorCode
			if attempts < DeliveryMaxAttempts {
				next := now.Add(deliveryBackoff(attempts))
				outcome.NextAttemptAt = &next
			}
		}
		_ = service.repository.RecordAlertDelivery(deliveryCtx, record.ID, outcome)
		processed++
	}
}

func deliveryDue(record Record, now time.Time) bool {
	if record.DeliveryState == DeliveryDelivered || record.DeliveryState == DeliveryAttempting {
		return false
	}
	return record.NextDeliveryAttemptAt == nil || !record.NextDeliveryAttemptAt.After(now)
}

func deliveryBackoff(attempts int) time.Duration {
	if attempts <= 1 {
		return time.Minute
	}
	return 5 * time.Minute
}

func notificationFor(record Record, detailed bool) Notification {
	if !detailed {
		return Notification{Title: "CodexFolio operational alert", Body: "Open CodexFolio to review an operational alert."}
	}
	parts := make([]string, 0, 3)
	if record.ProfileAlias != "" {
		parts = append(parts, record.ProfileAlias)
	}
	if record.RemainingPercent != nil {
		parts = append(parts, fmt.Sprintf("%.1f%% remaining", *record.RemainingPercent))
	}
	if strings.TrimSpace(record.Guidance) != "" {
		parts = append(parts, strings.TrimSpace(record.Guidance))
	}
	body := strings.Join(parts, " · ")
	if body == "" {
		body = "Open CodexFolio for details."
	}
	runes := []rune(body)
	if len(runes) > 240 {
		body = string(runes[:237]) + "..."
	}
	return Notification{Title: record.Title, Body: body}
}
