package main

import (
	"context"
	"errors"
	"sync"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

type serviceOperationalComposer func(*store.Store) (httpapi.OperationalServices, error)
type serviceActivator func(httpapi.OperationalServices) error

// lockedServiceLifecycle retains the one owner while a passphrase-backed
// service waits for an explicit per-process unlock. Opening the vault, opening
// or migrating SQLite, composing workflows, and publishing them to HTTP are a
// single serialized transition.
type lockedServiceLifecycle struct {
	mu         sync.Mutex
	paths      platform.Paths
	mode       platform.VaultMode
	openStore  profileStoreOpener
	compose    serviceOperationalComposer
	activate   serviceActivator
	store      *store.Store
	background serviceCloser
	health     httpapi.ServiceHealth
}

func newLockedServiceLifecycle(paths platform.Paths, mode platform.VaultMode, openStore profileStoreOpener, compose serviceOperationalComposer) *lockedServiceLifecycle {
	return &lockedServiceLifecycle{
		paths:     paths,
		mode:      mode,
		openStore: openStore,
		compose:   compose,
		health: httpapi.ServiceHealth{
			ServiceState:     httpapi.ServiceStateLocked,
			VaultState:       httpapi.VaultStateLocked,
			DatabaseState:    httpapi.DatabaseStateNotChecked,
			GuidanceCommands: []string{"codex-folio vault unlock"},
		},
	}
}

func (lifecycle *lockedServiceLifecycle) SetActivator(activate serviceActivator) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.activate = activate
}

func (lifecycle *lockedServiceLifecycle) Health() httpapi.ServiceHealth {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	result := lifecycle.health
	result.GuidanceCommands = append([]string(nil), result.GuidanceCommands...)
	return result
}

func (lifecycle *lockedServiceLifecycle) Unlock(ctx context.Context, passphrase string) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.store != nil && lifecycle.health.ServiceState == httpapi.ServiceStateReady {
		return nil
	}
	if lifecycle.openStore == nil || lifecycle.compose == nil || lifecycle.activate == nil {
		err := apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("locked service composition is unavailable"))
		lifecycle.recordFailure(err)
		return err
	}
	if err := ctx.Err(); err != nil {
		failure := apperrors.New(apperrors.VaultLocked, err)
		lifecycle.recordFailure(failure)
		return failure
	}
	stateStore, err := lifecycle.openStore(lifecycle.paths, lifecycle.mode, passphrase)
	if err != nil {
		lifecycle.recordFailure(err)
		return err
	}
	services, err := lifecycle.compose(stateStore)
	if err == nil {
		lifecycle.background = services.Background
	}
	if err == nil {
		err = lifecycle.activate(services)
	}
	if err != nil {
		if lifecycle.background != nil {
			_ = lifecycle.background.Close()
			lifecycle.background = nil
		}
		_ = stateStore.Close()
		lifecycle.recordFailure(err)
		return err
	}
	lifecycle.store = stateStore
	lifecycle.health = httpapi.ServiceHealth{
		ServiceState:     httpapi.ServiceStateReady,
		VaultState:       httpapi.VaultStateUnlocked,
		DatabaseState:    httpapi.DatabaseStateReady,
		GuidanceCommands: []string{},
	}
	return nil
}

func (lifecycle *lockedServiceLifecycle) Close() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.store == nil {
		return nil
	}
	if lifecycle.background != nil {
		if err := lifecycle.background.Close(); err != nil {
			return err
		}
		lifecycle.background = nil
	}
	err := lifecycle.store.Close()
	lifecycle.store = nil
	return err
}

func (lifecycle *lockedServiceLifecycle) recordFailure(err error) {
	code := apperrors.Code(err)
	if recoveryRequired(code) {
		lifecycle.health = httpapi.ServiceHealth{
			ServiceState:  httpapi.ServiceStateRecoveryRequired,
			VaultState:    httpapi.VaultStateLocked,
			DatabaseState: httpapi.DatabaseStateRecoveryRequired,
			ErrorCode:     code,
			GuidanceCommands: []string{
				"codex-folio service recovery verify",
				"codex-folio service recovery list",
			},
		}
		return
	}
	lifecycle.health = httpapi.ServiceHealth{
		ServiceState:     httpapi.ServiceStateLocked,
		VaultState:       httpapi.VaultStateLocked,
		DatabaseState:    httpapi.DatabaseStateNotChecked,
		ErrorCode:        code,
		GuidanceCommands: []string{"codex-folio vault unlock"},
	}
}

func recoveryRequired(code string) bool {
	switch code {
	case apperrors.StoreIntegrityFailed,
		apperrors.StoreSchemaIncompatible,
		apperrors.StoreMigrationFailed,
		apperrors.StoreMigrationPartial,
		apperrors.StoreBackupFailed,
		apperrors.StoreRecoveryRestoreFailed:
		return true
	default:
		return false
	}
}
