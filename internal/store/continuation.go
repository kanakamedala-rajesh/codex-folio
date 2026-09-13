package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/launch"
)

const unresolvedSourceLaunchSQL = `SELECT profile_id, state FROM managed_launches
	WHERE project_identity_id = ? AND state <> 'exited'
	AND NOT (state = 'abandoned' AND process_id IS NULL)
	ORDER BY rowid DESC LIMIT 1`

const definitiveSourceLaunchSQL = `SELECT profile_id, state FROM managed_launches candidate
	WHERE project_identity_id = ?
	AND NOT (state = 'abandoned' AND process_id IS NULL)
	AND NOT EXISTS (
		SELECT 1 FROM managed_launches unresolved
		WHERE unresolved.project_identity_id = candidate.project_identity_id
		AND NOT (unresolved.state = 'abandoned' AND unresolved.process_id IS NULL)
		AND unresolved.state <> 'exited'
	)
	ORDER BY ended_at DESC, rowid DESC LIMIT 1`

const latestExitedSourceLaunchSQL = `SELECT profile_id, state FROM managed_launches
	WHERE project_identity_id = ? AND state = 'exited'
	ORDER BY ended_at DESC, rowid DESC LIMIT 1`

func (store *Store) SaveCheckpoint(ctx context.Context, record continuation.CheckpointRecord) error {
	checkpoint := Checkpoint{
		CheckpointID: record.ID, ProjectIdentityID: record.ProjectIdentityID, Status: record.Status,
		Goal: record.Goal, CompletedWork: record.CompletedWork, PendingWork: record.PendingWork,
		Validation: record.Validation, Risks: record.Risks, NextAction: record.NextAction,
		RecoveryMetadata: &record.Metadata, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}
	return store.putCheckpoint(ctx, checkpoint, record.ExpectedStatus, record.ExpectedRevision)
}

func (store *Store) LoadCheckpoint(ctx context.Context, id string) (continuation.CheckpointRecord, error) {
	record, err := store.GetCheckpoint(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return continuation.CheckpointRecord{}, continuation.ErrCheckpointNotFound
	}
	if err != nil {
		return continuation.CheckpointRecord{}, err
	}
	metadata := ""
	if record.RecoveryMetadata != nil {
		metadata = *record.RecoveryMetadata
	}
	return continuation.CheckpointRecord{
		ID: record.CheckpointID, ProjectIdentityID: record.ProjectIdentityID, Status: record.Status,
		Goal: record.Goal, CompletedWork: record.CompletedWork, PendingWork: record.PendingWork,
		Validation: record.Validation, Risks: record.Risks, NextAction: record.NextAction,
		Metadata: metadata, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}, nil
}

func (store *Store) LatestSourceLaunch(ctx context.Context, projectID string) (continuation.SourceLaunch, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var profileID, state string
	err := store.db.QueryRowContext(contextOrBackground(ctx), unresolvedSourceLaunchSQL, projectID).Scan(&profileID, &state)
	if err == nil {
		result := continuation.SourceLaunch{ProfileID: profileID, State: continuation.SourceUncertain}
		if state == string(launch.StateRunning) {
			result.State = continuation.SourceRunning
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return continuation.SourceLaunch{}, coded(apperrors.StoreReadFailed, err)
	}
	err = store.db.QueryRowContext(contextOrBackground(ctx), latestExitedSourceLaunchSQL, projectID).Scan(&profileID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return continuation.SourceLaunch{State: continuation.SourceUncertain}, nil
	}
	if err != nil {
		return continuation.SourceLaunch{}, coded(apperrors.StoreReadFailed, err)
	}
	result := continuation.SourceLaunch{ProfileID: profileID, State: continuation.SourceUncertain}
	if state == string(launch.StateExited) {
		result.State = continuation.SourceExited
	} else if state == string(launch.StateRunning) {
		result.State = continuation.SourceRunning
	}
	return result, nil
}

func (store *Store) SourceIdentityHome(ctx context.Context, profileID string) (string, error) {
	if store == nil || store.db == nil || strings.TrimSpace(profileID) == "" {
		return "", continuation.ErrHistoryUnavailable
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	var homeID string
	var ciphertext []byte
	err := store.db.QueryRowContext(ctx, `SELECT h.identity_home_id, h.location_ciphertext
		FROM identity_profiles p
		JOIN identity_homes h ON h.identity_home_id = p.identity_home_id
		WHERE p.profile_id = ?
		AND NOT EXISTS (SELECT 1 FROM profile_quarantine q WHERE q.profile_id = p.profile_id)`, profileID).Scan(&homeID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", continuation.ErrHistoryUnavailable
	}
	if err != nil {
		return "", coded(apperrors.StoreReadFailed, err)
	}
	secureVault, err := store.requireVault()
	if err != nil {
		return "", err
	}
	home, err := decryptField(ctx, secureVault, ciphertext, identityHomeAAD(homeID))
	if err != nil {
		return "", err
	}
	home = filepath.Clean(home)
	if !filepath.IsAbs(home) {
		return "", continuation.ErrHistoryUnavailable
	}
	return home, nil
}
