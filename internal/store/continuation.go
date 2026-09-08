package store

import (
	"context"
	"database/sql"
	"errors"

	"venkatasudha.com/codex-folio/internal/continuation"
)

func (store *Store) SaveCheckpoint(ctx context.Context, record continuation.CheckpointRecord) error {
	return store.PutCheckpoint(ctx, Checkpoint{
		CheckpointID: record.ID, ProjectIdentityID: record.ProjectIdentityID, Status: record.Status,
		Goal: record.Goal, CompletedWork: record.CompletedWork, PendingWork: record.PendingWork,
		Validation: record.Validation, Risks: record.Risks, NextAction: record.NextAction,
		RecoveryMetadata: &record.Metadata, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	})
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
