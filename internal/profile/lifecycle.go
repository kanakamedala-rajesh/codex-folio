package profile

import (
	"context"
	"errors"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type LifecycleRepository interface {
	BeginProfileRemoval(context.Context, string, string) (RemovalRecord, error)
	ListQuarantinedProfiles(context.Context) ([]RemovalRecord, error)
	CompleteProfileQuarantine(context.Context, string) error
	CancelProfileRemoval(context.Context, string) error
	GetProfile(context.Context, string) (IdentityProfile, error)
	GetQuarantinedProfile(context.Context, string) (RemovalRecord, error)
	RestoreProfile(context.Context, string) error
	PurgeProfile(context.Context, string) error
}

func (lifecycle *Lifecycle) ListQuarantined(ctx context.Context) ([]RemovalRecord, error) {
	return lifecycle.repository.ListQuarantinedProfiles(contextOrBackground(ctx))
}

func (lifecycle *Lifecycle) PreviewRemoval(ctx context.Context, alias string) (RemovalRecord, error) {
	item, err := lifecycle.repository.GetProfile(contextOrBackground(ctx), alias)
	if errors.Is(err, ErrNotFound) {
		return lifecycle.repository.GetQuarantinedProfile(contextOrBackground(ctx), alias)
	}
	return RemovalRecord{Profile: item}, err
}

func (lifecycle *Lifecycle) PreviewQuarantined(ctx context.Context, alias string) (RemovalRecord, error) {
	return lifecycle.repository.GetQuarantinedProfile(contextOrBackground(ctx), alias)
}

type HomeLifecycle interface {
	Quarantine(context.Context, string, string) error
	Restore(context.Context, string, string) error
	Purge(context.Context, string) error
}

type Lifecycle struct {
	repository LifecycleRepository
	homes      HomeLifecycle
	now        func() time.Time
}

func NewLifecycle(repository LifecycleRepository, homes HomeLifecycle) (*Lifecycle, error) {
	if repository == nil || homes == nil {
		return nil, apperrors.New(apperrors.ProfileQuarantineInvalid, ErrQuarantineInvalid)
	}
	return &Lifecycle{repository: repository, homes: homes, now: time.Now}, nil
}

func (lifecycle *Lifecycle) Remove(ctx context.Context, alias, replacement string) (RemovalRecord, error) {
	record, err := lifecycle.repository.BeginProfileRemoval(contextOrBackground(ctx), alias, replacement)
	if err != nil || record.Action == RemovalDeregistered {
		return record, err
	}
	if err := lifecycle.homes.Quarantine(ctx, record.Profile.ID, record.Profile.IdentityHomePath); err != nil {
		cancelErr := lifecycle.repository.CancelProfileRemoval(contextOrBackground(ctx), record.Profile.ID)
		return RemovalRecord{}, errors.Join(err, cancelErr)
	}
	if err := lifecycle.repository.CompleteProfileQuarantine(contextOrBackground(ctx), record.Profile.ID); err != nil {
		return RemovalRecord{}, err
	}
	record.State = QuarantineReady
	return record, nil
}

func (lifecycle *Lifecycle) Restore(ctx context.Context, alias string) (RemovalRecord, error) {
	record, err := lifecycle.repository.GetQuarantinedProfile(contextOrBackground(ctx), alias)
	if err != nil {
		return RemovalRecord{}, err
	}
	if record.State != QuarantineReady {
		return RemovalRecord{}, apperrors.New(apperrors.ProfileQuarantineInvalid, ErrQuarantineInvalid)
	}
	if !lifecycle.now().UTC().Before(record.PurgeAfter) {
		return RemovalRecord{}, apperrors.New(apperrors.ProfileQuarantineExpired, ErrQuarantineExpired)
	}
	if err := lifecycle.homes.Restore(ctx, record.Profile.ID, record.Profile.IdentityHomePath); err != nil {
		return RemovalRecord{}, err
	}
	if err := lifecycle.repository.RestoreProfile(contextOrBackground(ctx), record.Profile.ID); err != nil {
		return RemovalRecord{}, err
	}
	record.Action = RemovalRestored
	record.State = ""
	return record, nil
}

func (lifecycle *Lifecycle) Purge(ctx context.Context, alias string) (RemovalRecord, error) {
	record, err := lifecycle.repository.GetQuarantinedProfile(contextOrBackground(ctx), alias)
	if err != nil {
		return RemovalRecord{}, err
	}
	if record.State != QuarantineReady {
		return RemovalRecord{}, apperrors.New(apperrors.ProfileQuarantineInvalid, ErrQuarantineInvalid)
	}
	if err := lifecycle.homes.Purge(ctx, record.Profile.ID); err != nil {
		return RemovalRecord{}, err
	}
	if err := lifecycle.repository.PurgeProfile(contextOrBackground(ctx), record.Profile.ID); err != nil {
		return RemovalRecord{}, err
	}
	record.Action = RemovalPurged
	record.State = ""
	return record, nil
}
