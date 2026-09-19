package alerts

import (
	"context"
	"errors"
	"time"
)

const (
	StateOpen         = "open"
	StateAcknowledged = "acknowledged"
	StateResolved     = "resolved"
)

var ErrInvalid = errors.New("alert request is invalid")

type Record struct {
	ID string
	Condition
	State           string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	AcknowledgedAt  *time.Time
	ResolvedAt      *time.Time
	OccurrenceCount int
}

type Repository interface {
	AlertEvidence(context.Context, string) ([]Evidence, error)
	AlertThresholds(context.Context) ([]Threshold, error)
	SyncAlerts(context.Context, string, []Condition, time.Time, int) error
	ListAlerts(context.Context, int) ([]Record, error)
	AcknowledgeAlert(context.Context, string, time.Time) error
	SetAlertThreshold(context.Context, Threshold, time.Time) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	clock      Clock
}

func NewService(repository Repository, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository, clock: clock}, nil
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
