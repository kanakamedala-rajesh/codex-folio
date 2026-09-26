package store

import (
	"context"
	"database/sql"
	"errors"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

// vaultProtectedColumn describes one allowlisted encrypted SQLite field and
// the stable associated-data identity used by its existing read/write path.
// Keeping this list next to the migration makes adding a protected field an
// explicit migration obligation instead of silently leaving mixed generations.
type vaultProtectedColumn struct {
	table      string
	rowID      string
	aadID      string
	column     string
	associated func(string) []byte
}

var vaultProtectedColumns = []vaultProtectedColumn{
	{table: "dashboard_tls_identity", rowID: "identity_id", aadID: "identity_id", column: "server_key_ciphertext", associated: dashboardTLSKeyAAD},
	{table: "identity_homes", rowID: "identity_home_id", aadID: "identity_home_id", column: "location_ciphertext", associated: identityHomeAAD},
	{table: "identity_homes", rowID: "identity_home_id", aadID: "profile_id", column: "documented_login_identity_ciphertext", associated: func(id string) []byte { return documentedMetadataAAD(id, "login-identity") }},
	{table: "identity_homes", rowID: "identity_home_id", aadID: "profile_id", column: "documented_workspace_ciphertext", associated: func(id string) []byte { return documentedMetadataAAD(id, "workspace") }},
	{table: "project_identities", rowID: "project_identity_id", aadID: "project_identity_id", column: "canonical_path_ciphertext", associated: projectIdentityAAD},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "goal_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointGoalField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "completed_work_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointCompletedField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "pending_work_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointPendingField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "validation_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointValidationField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "risks_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointRisksField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "next_action_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointNextActionField) }},
	{table: "checkpoints", rowID: "checkpoint_id", aadID: "checkpoint_id", column: "recovery_metadata_ciphertext", associated: func(id string) []byte { return checkpointAAD(id, checkpointRecoveryField) }},
	{table: "usage_snapshots", rowID: "snapshot_id", aadID: "snapshot_id", column: "login_identity_ciphertext", associated: func(id string) []byte { return usageScopeAAD(id, "login-identity") }},
	{table: "usage_snapshots", rowID: "snapshot_id", aadID: "snapshot_id", column: "workspace_ciphertext", associated: func(id string) []byte { return usageScopeAAD(id, "workspace") }},
	{table: "usage_aggregates", rowID: "aggregate_id", aadID: "aggregate_id", column: "source_scope_ciphertext", associated: aggregateScopeAAD},
}

// CreateVaultMigrationBackup records the required pre-transition recovery
// point while the current store still opens through its original vault.
func (store *Store) CreateVaultMigrationBackup(ctx context.Context) (RecoveryCandidate, error) {
	if store == nil || store.db == nil {
		return RecoveryCandidate{}, coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	if store.recovery == nil {
		recovery, err := NewRecovery(RecoveryOptions{DatabasePath: store.path, Clock: store.clock})
		if err != nil {
			return RecoveryCandidate{}, err
		}
		store.recovery = recovery
	}
	return store.recovery.CreateBackup(ctx, BackupReasonMigration)
}

// ReprotectVaultState atomically decrypts every allowlisted protected field
// with the current vault and encrypts it through destination. A failure rolls
// back the SQLite transaction, leaving the original generation readable.
func (store *Store) ReprotectVaultState(ctx context.Context, destination Vault) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	if destination == nil {
		return apperrors.New(apperrors.VaultUnavailable, vault.ErrUnavailable)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	source, err := store.requireVaultLocked()
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
	}
	rollback := func() { _ = tx.Rollback() }
	for _, protected := range vaultProtectedColumns {
		if err := reprotectColumn(ctx, tx, source, destination, protected); err != nil {
			rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		rollback()
		return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
	}
	if lockable, ok := source.(interface{ Lock() }); ok && source != destination {
		lockable.Lock()
	}
	store.vault = destination
	return nil
}

// VerifyProtectedState authenticates every retained protected value through
// the store's current vault without returning plaintext to the caller.
func (store *Store) VerifyProtectedState(ctx context.Context) error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	secureVault, err := store.requireVaultLocked()
	if err != nil {
		return err
	}
	for _, protected := range vaultProtectedColumns {
		query := "SELECT " + protected.aadID + ", " + protected.column + " FROM " + protected.table + " WHERE " + protected.column + " IS NOT NULL"
		rows, err := store.db.QueryContext(ctx, query)
		if err != nil {
			return coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		for rows.Next() {
			var aadID string
			var ciphertext []byte
			if err := rows.Scan(&aadID, &ciphertext); err != nil {
				_ = rows.Close()
				return coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
			}
			plaintext, err := secureVault.Decrypt(ctx, ciphertext, protected.associated(aadID))
			clear(plaintext)
			if err != nil {
				_ = rows.Close()
				return normalizeVaultMigrationError(err)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
		if err := rows.Close(); err != nil {
			return coded(apperrors.StoreReadFailed, errors.Join(ErrSensitiveRead, err))
		}
	}
	return nil
}

type vaultMigrationValue struct {
	rowID      string
	ciphertext []byte
}

func reprotectColumn(ctx context.Context, tx *sql.Tx, source, destination Vault, protected vaultProtectedColumn) error {
	query := "SELECT " + protected.rowID + ", " + protected.aadID + ", " + protected.column + " FROM " + protected.table + " WHERE " + protected.column + " IS NOT NULL"
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
	}
	values := make([]vaultMigrationValue, 0)
	for rows.Next() {
		var rowID, aadID string
		var ciphertext []byte
		if err := rows.Scan(&rowID, &aadID, &ciphertext); err != nil {
			_ = rows.Close()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		associatedData := protected.associated(aadID)
		plaintext, err := source.Decrypt(ctx, ciphertext, associatedData)
		if err != nil {
			_ = rows.Close()
			return normalizeVaultMigrationError(err)
		}
		reprotected, err := destination.Encrypt(ctx, plaintext, associatedData)
		clear(plaintext)
		if err != nil {
			_ = rows.Close()
			return normalizeVaultMigrationError(err)
		}
		values = append(values, vaultMigrationValue{rowID: rowID, ciphertext: reprotected})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
	}
	if err := rows.Close(); err != nil {
		return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
	}
	update := "UPDATE " + protected.table + " SET " + protected.column + " = ? WHERE " + protected.rowID + " = ?"
	for _, value := range values {
		if _, err := tx.ExecContext(ctx, update, value.ciphertext, value.rowID); err != nil {
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
	}
	return nil
}

func (store *Store) requireVaultLocked() (Vault, error) {
	if store.vault == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, vault.ErrUnavailable)
	}
	return store.vault, nil
}

func normalizeVaultMigrationError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, err))
}
