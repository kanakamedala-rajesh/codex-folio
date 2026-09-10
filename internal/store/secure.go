package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	projectCanonicalPathField = "canonical-path"
	checkpointGoalField       = "goal"
	checkpointCompletedField  = "completed-work"
	checkpointPendingField    = "pending-work"
	checkpointValidationField = "validation"
	checkpointRisksField      = "risks"
	checkpointNextActionField = "next-action"
	checkpointRecoveryField   = "recovery-metadata"
)

// ProjectIdentity is the storage boundary for an app-local repository
// association. CanonicalPath is never written to SQLite as plaintext.
type ProjectIdentity struct {
	ProjectIdentityID  string
	ProjectAlias       string
	RepositoryBasename string
	CanonicalPath      string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Checkpoint contains sanitized continuation fields. The sensitive fields
// are encrypted independently so a field cannot be moved to another purpose
// without failing authenticated decryption.
type Checkpoint struct {
	CheckpointID      string
	ProjectIdentityID string
	Status            string
	Goal              *string
	CompletedWork     *string
	PendingWork       *string
	Validation        *string
	Risks             *string
	NextAction        *string
	RecoveryMetadata  *string
	CreatedAt         time.Time
	ExpiresAt         *time.Time
}

// OpenWithVault opens a verified store with the supplied key-opaque vault.
// The vault is required only when a sensitive record is read or written, so
// foundation startup remains possible while secure storage is unavailable.
func OpenWithVault(path string, secureVault Vault) (*Store, error) {
	return OpenWithOptions(Options{Path: path, Vault: secureVault})
}

// PutProjectIdentity inserts or replaces one Project Identity. Encryption is
// completed before the database write, so an unavailable vault cannot cause a
// plaintext or partially encrypted row to be stored.
func (store *Store) PutProjectIdentity(ctx context.Context, identity ProjectIdentity) error {
	secureVault, err := store.requireVault()
	if err != nil {
		return err
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	ctx = contextOrBackground(ctx)
	ciphertext, err := encryptField(ctx, secureVault, []byte(identity.CanonicalPath), projectIdentityAAD(identity.ProjectIdentityID))
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_identities (
		project_identity_id, project_alias, repository_basename, canonical_path_ciphertext, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_identity_id) DO UPDATE SET
		project_alias = excluded.project_alias,
		repository_basename = excluded.repository_basename,
		canonical_path_ciphertext = excluded.canonical_path_ciphertext,
		created_at = excluded.created_at,
		updated_at = excluded.updated_at`,
		identity.ProjectIdentityID,
		identity.ProjectAlias,
		identity.RepositoryBasename,
		ciphertext,
		formatStoredTime(identity.CreatedAt),
		formatStoredTime(identity.UpdatedAt),
	)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrSensitiveWrite, err))
	}
	return nil
}

// GetProjectIdentity returns one Project Identity after authenticating and
// decrypting its canonical path through the configured vault.
func (store *Store) GetProjectIdentity(ctx context.Context, projectIdentityID string) (ProjectIdentity, error) {
	secureVault, err := store.requireVault()
	if err != nil {
		return ProjectIdentity{}, err
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	ctx = contextOrBackground(ctx)
	var identity ProjectIdentity
	var ciphertext []byte
	var createdAt, updatedAt string
	if err := store.db.QueryRowContext(ctx, `SELECT project_identity_id, project_alias, repository_basename, canonical_path_ciphertext, created_at, updated_at
		FROM project_identities WHERE project_identity_id = ?`, projectIdentityID).Scan(
		&identity.ProjectIdentityID,
		&identity.ProjectAlias,
		&identity.RepositoryBasename,
		&ciphertext,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectIdentity{}, err
		}
		return ProjectIdentity{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	identity.CanonicalPath, err = decryptField(ctx, secureVault, ciphertext, projectIdentityAAD(identity.ProjectIdentityID))
	if err != nil {
		return ProjectIdentity{}, err
	}
	identity.CreatedAt, err = parseStoredTime(createdAt)
	if err != nil {
		return ProjectIdentity{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	identity.UpdatedAt, err = parseStoredTime(updatedAt)
	if err != nil {
		return ProjectIdentity{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	return identity, nil
}

// listProjectIdentities returns private records to the activity workflow after
// authenticating every encrypted canonical path.
func (store *Store) listProjectIdentities(ctx context.Context) ([]ProjectIdentity, error) {
	secureVault, err := store.requireVault()
	if err != nil {
		return nil, err
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	ctx = contextOrBackground(ctx)
	rows, err := store.db.QueryContext(ctx, `SELECT project_identity_id, project_alias, repository_basename, canonical_path_ciphertext, created_at, updated_at
		FROM project_identities ORDER BY created_at, project_identity_id`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	defer rows.Close()
	identities := []ProjectIdentity{}
	for rows.Next() {
		var identity ProjectIdentity
		var ciphertext []byte
		var createdAt, updatedAt string
		if err := rows.Scan(&identity.ProjectIdentityID, &identity.ProjectAlias, &identity.RepositoryBasename, &ciphertext, &createdAt, &updatedAt); err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		identity.CanonicalPath, err = decryptField(ctx, secureVault, ciphertext, projectIdentityAAD(identity.ProjectIdentityID))
		if err != nil {
			return nil, err
		}
		identity.CreatedAt, err = parseStoredTime(createdAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		identity.UpdatedAt, err = parseStoredTime(updatedAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	return identities, nil
}

func (store *Store) SaveProjectRecord(ctx context.Context, record activity.ProjectRecord) error {
	return store.PutProjectIdentity(ctx, ProjectIdentity{
		ProjectIdentityID:  record.ID,
		ProjectAlias:       record.Alias,
		RepositoryBasename: record.Basename,
		CanonicalPath:      record.CanonicalPath,
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
	})
}

func (store *Store) ListProjectRecords(ctx context.Context) ([]activity.ProjectRecord, error) {
	identities, err := store.listProjectIdentities(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]activity.ProjectRecord, 0, len(identities))
	for _, identity := range identities {
		records = append(records, activity.ProjectRecord{
			ID: identity.ProjectIdentityID, Alias: identity.ProjectAlias, Basename: identity.RepositoryBasename, CanonicalPath: identity.CanonicalPath,
			CreatedAt: identity.CreatedAt, UpdatedAt: identity.UpdatedAt,
		})
	}
	return records, nil
}

// ListProjectIdentities returns only the safe projection and never opens the
// encrypted canonical-path field.
func (store *Store) ListProjectIdentities(ctx context.Context) ([]activity.ProjectIdentity, error) {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	rows, err := store.db.QueryContext(contextOrBackground(ctx), `SELECT project_identity_id, project_alias, repository_basename, created_at, updated_at
		FROM project_identities ORDER BY created_at, project_identity_id`)
	if err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	defer rows.Close()
	projects := []activity.ProjectIdentity{}
	for rows.Next() {
		var project activity.ProjectIdentity
		var createdAt, updatedAt string
		if err := rows.Scan(&project.ID, &project.Alias, &project.Basename, &createdAt, &updatedAt); err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		project.CreatedAt, err = parseStoredTime(createdAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		project.UpdatedAt, err = parseStoredTime(updatedAt)
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}
	return projects, nil
}

func (store *Store) UpdateProjectAlias(ctx context.Context, id, alias string, updatedAt time.Time) error {
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	result, err := store.db.ExecContext(contextOrBackground(ctx), `UPDATE project_identities SET project_alias = ?, updated_at = ? WHERE project_identity_id = ?`, alias, formatStoredTime(updatedAt), id)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return coded(apperrors.StoreWriteFailed, err)
	}
	if changed == 0 {
		return apperrors.New(apperrors.ProjectIdentityNotFound, activity.ErrProjectNotFound)
	}
	return nil
}

// PutCheckpoint inserts or replaces one checkpoint. Each sensitive field is
// encrypted with field-specific associated data before any row mutation.
func (store *Store) PutCheckpoint(ctx context.Context, checkpoint Checkpoint) error {
	return store.putCheckpoint(ctx, checkpoint, "", "")
}

func (store *Store) putCheckpoint(ctx context.Context, checkpoint Checkpoint, expectedStatus, expectedRevision string) error {
	secureVault, err := store.requireVault()
	if err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	fields := []struct {
		value *string
		name  string
	}{
		{checkpoint.Goal, checkpointGoalField},
		{checkpoint.CompletedWork, checkpointCompletedField},
		{checkpoint.PendingWork, checkpointPendingField},
		{checkpoint.Validation, checkpointValidationField},
		{checkpoint.Risks, checkpointRisksField},
		{checkpoint.NextAction, checkpointNextActionField},
		{checkpoint.RecoveryMetadata, checkpointRecoveryField},
	}
	ciphertexts := make([][]byte, len(fields))
	for index, field := range fields {
		if field.value == nil {
			continue
		}
		ciphertexts[index], err = encryptField(ctx, secureVault, []byte(*field.value), checkpointAAD(checkpoint.CheckpointID, field.name))
		if err != nil {
			return err
		}
	}

	var expiresAt any
	if checkpoint.ExpiresAt != nil {
		expiresAt = formatStoredTime(*checkpoint.ExpiresAt)
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrSensitiveWrite, err))
	}
	rollback := func() { _ = tx.Rollback() }
	if expectedStatus != "" || expectedRevision != "" {
		if expectedStatus == "" || expectedRevision == "" {
			rollback()
			return continuation.ErrCheckpointInvalid
		}
		var currentStatus string
		var metadataCiphertext []byte
		if err := tx.QueryRowContext(ctx, `SELECT status, recovery_metadata_ciphertext FROM checkpoints WHERE checkpoint_id = ?`, checkpoint.CheckpointID).Scan(&currentStatus, &metadataCiphertext); err != nil {
			rollback()
			if errors.Is(err, sql.ErrNoRows) {
				return continuation.ErrCheckpointNotFound
			}
			return coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		if currentStatus != expectedStatus {
			rollback()
			return continuation.ErrHandoffNotReady
		}
		metadata, err := decryptField(ctx, secureVault, metadataCiphertext, checkpointAAD(checkpoint.CheckpointID, checkpointRecoveryField))
		if err != nil {
			rollback()
			return err
		}
		var current struct {
			Revision string `json:"revision"`
		}
		if json.Unmarshal([]byte(metadata), &current) != nil || current.Revision != expectedRevision {
			rollback()
			return continuation.ErrCheckpointRevisionChanged
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO checkpoints (
		checkpoint_id, project_identity_id, status,
		goal_ciphertext, completed_work_ciphertext, pending_work_ciphertext,
		validation_ciphertext, risks_ciphertext, next_action_ciphertext,
		recovery_metadata_ciphertext, created_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(checkpoint_id) DO UPDATE SET
		project_identity_id = excluded.project_identity_id,
		status = excluded.status,
		goal_ciphertext = excluded.goal_ciphertext,
		completed_work_ciphertext = excluded.completed_work_ciphertext,
		pending_work_ciphertext = excluded.pending_work_ciphertext,
		validation_ciphertext = excluded.validation_ciphertext,
		risks_ciphertext = excluded.risks_ciphertext,
		next_action_ciphertext = excluded.next_action_ciphertext,
		recovery_metadata_ciphertext = excluded.recovery_metadata_ciphertext,
		created_at = excluded.created_at,
		expires_at = excluded.expires_at`,
		checkpoint.CheckpointID,
		nullableString(checkpoint.ProjectIdentityID),
		checkpoint.Status,
		nullableBytes(ciphertexts[0]),
		nullableBytes(ciphertexts[1]),
		nullableBytes(ciphertexts[2]),
		nullableBytes(ciphertexts[3]),
		nullableBytes(ciphertexts[4]),
		nullableBytes(ciphertexts[5]),
		nullableBytes(ciphertexts[6]),
		formatStoredTime(checkpoint.CreatedAt),
		expiresAt,
	)
	if err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrSensitiveWrite, err))
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreWriteFailed, errors.Join(ErrSensitiveWrite, err))
	}
	return nil
}

// GetCheckpoint returns one checkpoint with all sensitive values authenticated
// and decrypted through the configured vault.
func (store *Store) GetCheckpoint(ctx context.Context, checkpointID string) (Checkpoint, error) {
	secureVault, err := store.requireVault()
	if err != nil {
		return Checkpoint{}, err
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	ctx = contextOrBackground(ctx)
	var checkpoint Checkpoint
	var projectIdentityID sql.NullString
	var ciphertexts [7][]byte
	var createdAt string
	var expiresAt sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT checkpoint_id, project_identity_id, status,
		goal_ciphertext, completed_work_ciphertext, pending_work_ciphertext,
		validation_ciphertext, risks_ciphertext, next_action_ciphertext,
		recovery_metadata_ciphertext, created_at, expires_at
		FROM checkpoints WHERE checkpoint_id = ?`, checkpointID).Scan(
		&checkpoint.CheckpointID,
		&projectIdentityID,
		&checkpoint.Status,
		&ciphertexts[0],
		&ciphertexts[1],
		&ciphertexts[2],
		&ciphertexts[3],
		&ciphertexts[4],
		&ciphertexts[5],
		&ciphertexts[6],
		&createdAt,
		&expiresAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Checkpoint{}, err
		}
		return Checkpoint{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	if projectIdentityID.Valid {
		checkpoint.ProjectIdentityID = projectIdentityID.String
	}
	fieldNames := []string{
		checkpointGoalField,
		checkpointCompletedField,
		checkpointPendingField,
		checkpointValidationField,
		checkpointRisksField,
		checkpointNextActionField,
		checkpointRecoveryField,
	}
	values := make([]*string, len(ciphertexts))
	for index, ciphertext := range ciphertexts {
		if ciphertext == nil {
			continue
		}
		value, decryptErr := decryptField(ctx, secureVault, ciphertext, checkpointAAD(checkpoint.CheckpointID, fieldNames[index]))
		if decryptErr != nil {
			return Checkpoint{}, decryptErr
		}
		values[index] = &value
	}
	checkpoint.Goal = values[0]
	checkpoint.CompletedWork = values[1]
	checkpoint.PendingWork = values[2]
	checkpoint.Validation = values[3]
	checkpoint.Risks = values[4]
	checkpoint.NextAction = values[5]
	checkpoint.RecoveryMetadata = values[6]
	checkpoint.CreatedAt, err = parseStoredTime(createdAt)
	if err != nil {
		return Checkpoint{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
	}
	if expiresAt.Valid {
		parsed, parseErr := parseStoredTime(expiresAt.String)
		if parseErr != nil {
			return Checkpoint{}, coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, parseErr))
		}
		checkpoint.ExpiresAt = &parsed
	}
	return checkpoint, nil
}

var (
	ErrSensitiveRead  = errors.New("sensitive record could not be read")
	ErrSensitiveWrite = errors.New("sensitive record could not be written")
)

func (store *Store) requireVault() (Vault, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	if store.vault == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, vault.ErrUnavailable)
	}
	return store.vault, nil
}

func encryptField(ctx context.Context, secureVault Vault, plaintext, associatedData []byte) ([]byte, error) {
	ciphertext, err := secureVault.Encrypt(ctx, plaintext, associatedData)
	if err != nil {
		if apperrors.Code(err) != "" {
			return nil, err
		}
		return nil, apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, err))
	}
	return ciphertext, nil
}

func decryptField(ctx context.Context, secureVault Vault, ciphertext, associatedData []byte) (string, error) {
	plaintext, err := secureVault.Decrypt(ctx, ciphertext, associatedData)
	if err != nil {
		if apperrors.Code(err) != "" {
			return "", err
		}
		return "", apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, err))
	}
	return string(plaintext), nil
}

func projectIdentityAAD(projectIdentityID string) []byte {
	return []byte("codex-folio/project-identities/" + projectIdentityID + "/" + projectCanonicalPathField)
}

func checkpointAAD(checkpointID, field string) []byte {
	return []byte("codex-folio/checkpoints/" + checkpointID + "/" + field)
}

func nullableBytes(value []byte) any {
	if value == nil {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func formatStoredTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseStoredTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored timestamp is invalid: %w", err)
	}
	return parsed, nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
