package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	// RecoveryBackupCount is the bounded number of rotating database backups.
	RecoveryBackupCount = 3

	// RecoveryDirectoryName is the app-local directory containing recovery
	// candidates and preserved database copies.
	RecoveryDirectoryName = "recovery"

	recoveryMetadataVersion = 1
)

var (
	ErrBackup             = errors.New("database backup failed")
	ErrRecoveryCandidate  = errors.New("recovery candidate is invalid")
	ErrRecoveryNotFound   = errors.New("recovery candidate was not found")
	ErrRecoveryRestore    = errors.New("database restore failed")
	ErrRecoveryDatabase   = errors.New("active database is missing")
	ErrRecoveryPathUnsafe = errors.New("recovery path is unsafe")
)

// BackupReason identifies an approved point at which a database snapshot is
// created. The reason is stored only in local recovery metadata.
type BackupReason string

const (
	BackupReasonMigration          BackupReason = "migration"
	BackupReasonRecoveryCheckpoint BackupReason = "recovery-checkpoint"
)

// RecoveryHooks inject failures at the real recovery boundaries. Production
// composition leaves these callbacks nil; tests use them to exercise partial
// backup, migration, and restore outcomes without changing recovery policy.
type RecoveryHooks struct {
	BeforeBackup                   func(BackupReason) error
	AfterBackupCopy                func(BackupReason) error
	AfterMigrationBeforeActivation func(int) error
	BeforeRestoreActivation        func(string) error
	AfterRestoreActivation         func(string) error
}

// RecoveryOptions configures the app-local recovery manager.
type RecoveryOptions struct {
	DatabasePath    string
	BackupDirectory string
	Clock           Clock
	FileSystem      FileSystem
	Hooks           RecoveryHooks
}

// RecoveryCandidate is a safe projection of one app-local backup. It never
// contains a filesystem path, database row, encrypted value, or vault detail.
type RecoveryCandidate struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	SchemaVersion  int       `json:"schema_version"`
	Valid          bool      `json:"valid"`
	ValidationCode string    `json:"validation_code,omitempty"`
}

// RecoveryVerification is the redacted result of active-database inspection.
type RecoveryVerification struct {
	Valid         bool   `json:"valid"`
	SchemaVersion int    `json:"schema_version,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
}

// RecoveryResult reports an explicit restore without returning paths or
// database contents. Changes after KnownLossWindowStart may not be present in
// the selected candidate.
type RecoveryResult struct {
	CandidateID          string    `json:"candidate_id"`
	RestoredAt           time.Time `json:"restored_at"`
	KnownLossWindowStart time.Time `json:"known_loss_window_start"`
	KnownLossWindowEnd   time.Time `json:"known_loss_window_end"`
	PreservedDatabaseID  string    `json:"preserved_database_id,omitempty"`
}

// File is the small file-handle surface required for atomic recovery copies.
// It is deliberately local to the store adapter so fault-injection tests use
// the same boundary as production filesystem operations.
type File interface {
	io.Reader
	io.Writer
	io.Closer
	Stat() (os.FileInfo, error)
	Chmod(os.FileMode) error
	Sync() error
}

// FileSystem is the filesystem seam used by backup and restore. SQLite
// validation still uses the repository's real SQLite driver against the named
// candidate path; this seam controls file safety, copying, and activation.
type FileSystem interface {
	Lstat(string) (os.FileInfo, error)
	MkdirAll(string, os.FileMode) error
	Chmod(string, os.FileMode) error
	OpenFile(string, int, os.FileMode) (File, error)
	Open(string) (File, error)
	Remove(string) error
	Rename(string, string) error
}

type recoveryFileSystem struct{}

func (recoveryFileSystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (recoveryFileSystem) MkdirAll(path string, permission os.FileMode) error {
	return os.MkdirAll(path, permission)
}

func (recoveryFileSystem) Chmod(path string, permission os.FileMode) error {
	return os.Chmod(path, permission)
}

func (recoveryFileSystem) OpenFile(path string, flags int, permission os.FileMode) (File, error) {
	return os.OpenFile(path, flags, permission)
}

func (recoveryFileSystem) Open(path string) (File, error) {
	return os.Open(path)
}

func (recoveryFileSystem) Remove(path string) error {
	return os.Remove(path)
}

func (recoveryFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

// Recovery owns the bounded backup rotation and explicit restore workflow.
// The service must hold the platform state-owner lock before constructing or
// using it; the mutex additionally serializes calls within one process.
type Recovery struct {
	databasePath    string
	backupDirectory string
	clock           Clock
	filesystem      FileSystem
	hooks           RecoveryHooks

	mu sync.Mutex
}

// NewRecovery creates a recovery manager without opening or modifying the
// active database.
func NewRecovery(options RecoveryOptions) (*Recovery, error) {
	databasePath := strings.TrimSpace(options.DatabasePath)
	if databasePath == "" || !filepath.IsAbs(databasePath) {
		return nil, coded(apperrors.StoreOpenFailed, ErrInvalidDatabasePath)
	}
	databasePath = filepath.Clean(databasePath)

	backupDirectory := strings.TrimSpace(options.BackupDirectory)
	if backupDirectory == "" {
		backupDirectory = filepath.Join(filepath.Dir(databasePath), RecoveryDirectoryName)
	}
	if !filepath.IsAbs(backupDirectory) {
		return nil, coded(apperrors.StoreOpenFailed, ErrInvalidDatabasePath)
	}

	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	filesystem := options.FileSystem
	if filesystem == nil {
		filesystem = recoveryFileSystem{}
	}
	return &Recovery{
		databasePath:    databasePath,
		backupDirectory: filepath.Clean(backupDirectory),
		clock:           clock,
		filesystem:      filesystem,
		hooks:           options.Hooks,
	}, nil
}

// CreateCheckpoint verifies the active state and records a new recovery
// checkpoint. A failed candidate is discarded before any old candidate moves.
func (recovery *Recovery) CreateCheckpoint(ctx context.Context) (RecoveryCandidate, error) {
	return recovery.CreateBackup(ctx, BackupReasonRecoveryCheckpoint)
}

// CreateRecoveryCheckpoint snapshots a verified, open Store while serializing
// against the store's known write operations. Restore remains a Recovery-only
// operation because replacing an open database would be unsafe.
func (store *Store) CreateRecoveryCheckpoint(ctx context.Context) (RecoveryCandidate, error) {
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
	return store.recovery.CreateCheckpoint(ctx)
}

// ListRecoveryCandidates exposes the same redacted candidate projection as a
// standalone Recovery manager while serializing against known Store writes.
func (store *Store) ListRecoveryCandidates(ctx context.Context) ([]RecoveryCandidate, error) {
	if store == nil || store.db == nil {
		return nil, coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	if store.recovery == nil {
		return nil, coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	return store.recovery.ListCandidates(ctx)
}

// CreateBackup creates a validated backup for an approved recovery reason. It
// is used by Store before a risky migration and by explicit checkpoints.
func (recovery *Recovery) CreateBackup(ctx context.Context, reason BackupReason) (RecoveryCandidate, error) {
	if recovery == nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, ErrBackup)
	}
	if reason != BackupReasonMigration && reason != BackupReasonRecoveryCheckpoint {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, errors.New("unsupported backup reason")))
	}
	ctx = contextOrBackground(ctx)
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	return recovery.createBackupLocked(ctx, recovery.databasePath, reason)
}

// VerifyActive performs a read-only integrity and schema check. It never
// repairs, replaces, or resets the active database.
func (recovery *Recovery) VerifyActive(ctx context.Context) (RecoveryVerification, error) {
	if recovery == nil {
		return RecoveryVerification{}, coded(apperrors.StoreIntegrityFailed, ErrRecoveryDatabase)
	}
	ctx = contextOrBackground(ctx)
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	inspection, err := recovery.inspect(ctx, recovery.databasePath)
	result := RecoveryVerification{Valid: err == nil, SchemaVersion: inspection.schemaVersion}
	if err != nil {
		result.ErrorCode = safeErrorCode(err, apperrors.StoreIntegrityFailed)
	}
	return result, err
}

// ListCandidates returns only the fixed, app-local backup slots. Corrupt or
// otherwise unsafe candidates remain visible as invalid entries so recovery
// never hides evidence or treats an unvalidated file as restorable.
func (recovery *Recovery) ListCandidates(ctx context.Context) ([]RecoveryCandidate, error) {
	if recovery == nil {
		return nil, coded(apperrors.StoreReadFailed, ErrRecoveryDatabase)
	}
	ctx = contextOrBackground(ctx)
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	if err := recovery.ensureDirectory(); err != nil {
		return nil, coded(apperrors.StoreReadFailed, err)
	}

	candidates := make([]RecoveryCandidate, 0, RecoveryBackupCount)
	for index := 1; index <= RecoveryBackupCount; index++ {
		id := recoveryCandidateID(index)
		path := recovery.backupPath(id)
		info, err := recovery.filesystem.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, coded(apperrors.StoreReadFailed, err)
		}

		candidate := RecoveryCandidate{ID: id, CreatedAt: info.ModTime().UTC()}
		if metadata, metadataErr := recovery.readMetadata(id); metadataErr == nil {
			candidate.CreatedAt = metadata.CreatedAt
			candidate.SchemaVersion = metadata.SchemaVersion
		}
		inspection, validationErr := recovery.inspect(ctx, path)
		if validationErr != nil {
			candidate.Valid = false
			candidate.ValidationCode = apperrors.StoreRecoveryCandidateInvalid
		} else {
			candidate.Valid = true
			candidate.SchemaVersion = inspection.schemaVersion
			candidate.ValidationCode = ""
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// Restore validates the selected fixed-slot candidate, preserves the current
// active bytes, and atomically activates the candidate. A failed activation
// attempts to restore the displaced bytes and always leaves the candidate in
// the recovery set.
func (recovery *Recovery) Restore(ctx context.Context, candidateID string) (RecoveryResult, error) {
	if recovery == nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, ErrRecoveryRestore)
	}
	index, ok := recoveryCandidateIndex(candidateID)
	if !ok {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryCandidateNotFound, ErrRecoveryNotFound)
	}
	ctx = contextOrBackground(ctx)
	recovery.mu.Lock()
	defer recovery.mu.Unlock()
	if err := recovery.ensureDirectory(); err != nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
	}

	candidatePath := recovery.backupPath(recoveryCandidateID(index))
	candidate, err := recovery.validCandidateLocked(ctx, recoveryCandidateID(index), candidatePath)
	if err != nil {
		return RecoveryResult{}, err
	}

	now := recovery.clock.Now().UTC()
	if now.IsZero() {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, errors.New("recovery clock returned zero")))
	}
	if candidate.CreatedAt.IsZero() || candidate.CreatedAt.After(now) {
		candidate.CreatedAt = now
	}

	restoreTemporary := recovery.databasePath + ".restore-" + strconv.Itoa(index)
	if err := recovery.removePathIfPresent(restoreTemporary); err != nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
	}
	cleanupRestore := true
	defer func() {
		if cleanupRestore {
			_ = recovery.removePathIfPresent(restoreTemporary)
		}
	}()
	if err := copyRecoveryFile(recovery.filesystem, candidatePath, restoreTemporary); err != nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
	}
	if _, err := recovery.inspect(ctx, restoreTemporary); err != nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
	}

	// Secure the selected candidate as the newest checkpoint before changing
	// active state. If rotation fails, the active database remains untouched.
	if _, err := recovery.createBackupLocked(ctx, restoreTemporary, BackupReasonRecoveryCheckpoint); err != nil {
		return RecoveryResult{}, err
	}

	preservedID, preservedPath, activeExisted, err := recovery.preserveActiveLocked(ctx, now)
	if err != nil {
		return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
	}
	if recovery.hooks.BeforeRestoreActivation != nil {
		if err := recovery.hooks.BeforeRestoreActivation(candidate.ID); err != nil {
			return RecoveryResult{}, coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, err))
		}
	}

	if err := replaceRecoveryPath(recovery.filesystem, restoreTemporary, recovery.databasePath); err != nil {
		rollbackErr := recovery.rollbackActiveLocked(preservedPath, activeExisted)
		return RecoveryResult{}, recovery.restoreFailure(err, rollbackErr)
	}
	cleanupRestore = false

	if recovery.hooks.AfterRestoreActivation != nil {
		if err := recovery.hooks.AfterRestoreActivation(candidate.ID); err != nil {
			rollbackErr := recovery.rollbackActiveLocked(preservedPath, activeExisted)
			return RecoveryResult{}, recovery.restoreFailure(err, rollbackErr)
		}
	}
	if _, err := recovery.inspect(ctx, recovery.databasePath); err != nil {
		rollbackErr := recovery.rollbackActiveLocked(preservedPath, activeExisted)
		return RecoveryResult{}, recovery.restoreFailure(err, rollbackErr)
	}

	return RecoveryResult{
		CandidateID:          candidate.ID,
		RestoredAt:           now,
		KnownLossWindowStart: candidate.CreatedAt,
		KnownLossWindowEnd:   now,
		PreservedDatabaseID:  preservedID,
	}, nil
}

type databaseInspection struct {
	schemaVersion int
}

type recoveryMetadata struct {
	Version       int          `json:"version"`
	ID            string       `json:"id"`
	CreatedAt     time.Time    `json:"created_at"`
	SchemaVersion int          `json:"schema_version"`
	Reason        BackupReason `json:"reason"`
}

func (recovery *Recovery) createBackupLocked(ctx context.Context, sourcePath string, reason BackupReason) (RecoveryCandidate, error) {
	if err := recovery.ensureDirectory(); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	inspection, err := recovery.inspect(ctx, sourcePath)
	if err != nil {
		return RecoveryCandidate{}, err
	}
	if recovery.hooks.BeforeBackup != nil {
		if err := recovery.hooks.BeforeBackup(reason); err != nil {
			return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
		}
	}

	stagedData := recovery.backupPath("staged")
	stagedMetadata := recovery.metadataPath("staged")
	if err := recovery.removePathIfPresent(stagedData); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	if err := recovery.removePathIfPresent(stagedMetadata); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = recovery.removePathIfPresent(stagedData)
			_ = recovery.removePathIfPresent(stagedMetadata)
		}
	}()

	if err := copyRecoveryFile(recovery.filesystem, sourcePath, stagedData); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	if recovery.hooks.AfterBackupCopy != nil {
		if err := recovery.hooks.AfterBackupCopy(reason); err != nil {
			return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
		}
	}
	if _, err := recovery.inspect(ctx, stagedData); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, errors.Join(ErrRecoveryCandidate, err)))
	}

	createdAt := recovery.clock.Now().UTC()
	if createdAt.IsZero() {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, errors.New("backup clock returned zero")))
	}
	metadata := recoveryMetadata{
		Version:       recoveryMetadataVersion,
		ID:            recoveryCandidateID(1),
		CreatedAt:     createdAt,
		SchemaVersion: inspection.schemaVersion,
		Reason:        reason,
	}
	if err := recovery.writeMetadata(stagedMetadata, metadata); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	if err := recovery.rotate(stagedData, stagedMetadata); err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreBackupFailed, errors.Join(ErrBackup, err))
	}
	cleanup = false
	return RecoveryCandidate{
		ID:            recoveryCandidateID(1),
		CreatedAt:     createdAt,
		SchemaVersion: inspection.schemaVersion,
		Valid:         true,
	}, nil
}

func (recovery *Recovery) validCandidateLocked(ctx context.Context, id, path string) (RecoveryCandidate, error) {
	info, err := recovery.filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return RecoveryCandidate{}, coded(apperrors.StoreRecoveryCandidateNotFound, ErrRecoveryNotFound)
	}
	if err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreRecoveryCandidateInvalid, errors.Join(ErrRecoveryCandidate, err))
	}
	metadata, metadataErr := recovery.readMetadata(id)
	if metadataErr != nil {
		metadata = recoveryMetadata{ID: id, CreatedAt: info.ModTime().UTC()}
	}
	inspection, err := recovery.inspect(ctx, path)
	if err != nil {
		return RecoveryCandidate{}, coded(apperrors.StoreRecoveryCandidateInvalid, errors.Join(ErrRecoveryCandidate, err))
	}
	return RecoveryCandidate{
		ID:            id,
		CreatedAt:     metadata.CreatedAt,
		SchemaVersion: inspection.schemaVersion,
		Valid:         true,
	}, nil
}

func (recovery *Recovery) inspect(ctx context.Context, path string) (databaseInspection, error) {
	info, err := recovery.filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, ErrRecoveryDatabase)
	}
	if err != nil {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, ErrRecoveryPathUnsafe)
	}
	if !isPrivateRecoveryFile(info.Mode()) {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.New("database permissions are not user-scoped"))
	}
	if info.Size() == 0 {
		return databaseInspection{schemaVersion: 0}, nil
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer func() { _ = database.Close() }()
	if err := database.PingContext(ctx); err != nil {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	if err := verifyIntegrity(ctx, database); err != nil {
		return databaseInspection{}, err
	}
	version, err := readUserVersion(ctx, database)
	if err != nil {
		return databaseInspection{}, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	if version < 0 || version > CurrentSchemaVersion {
		return databaseInspection{}, coded(apperrors.StoreSchemaIncompatible, ErrFutureSchema)
	}
	if version == 0 {
		if err := ensureFreshDatabase(ctx, database); err != nil {
			return databaseInspection{}, err
		}
	} else if err := validateMigrationLedger(ctx, database, version); err != nil {
		return databaseInspection{}, err
	}
	if version == CurrentSchemaVersion {
		if err := validateSchema(ctx, database); err != nil {
			return databaseInspection{}, err
		}
	}
	return databaseInspection{schemaVersion: version}, nil
}

func (recovery *Recovery) ensureDirectory() error {
	info, err := recovery.filesystem.Lstat(recovery.backupDirectory)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if err := recovery.filesystem.MkdirAll(recovery.backupDirectory, 0o700); err != nil {
			return err
		}
		info, err = recovery.filesystem.Lstat(recovery.backupDirectory)
		created = true
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrRecoveryPathUnsafe
	}
	if created && !isWindowsRecoveryPath() {
		if err := recovery.filesystem.Chmod(recovery.backupDirectory, 0o700); err != nil {
			return err
		}
		info, err = recovery.filesystem.Lstat(recovery.backupDirectory)
		if err != nil {
			return err
		}
	}
	if !isPrivateRecoveryDirectory(info.Mode()) {
		return errors.New("recovery directory permissions are not user-scoped")
	}
	return nil
}

func (recovery *Recovery) rotate(stagedData, stagedMetadata string) error {
	for index := RecoveryBackupCount; index >= 2; index-- {
		previousID := recoveryCandidateID(index - 1)
		currentID := recoveryCandidateID(index)
		previousData := recovery.backupPath(previousID)
		currentData := recovery.backupPath(currentID)
		previousMetadata := recovery.metadataPath(previousID)
		currentMetadata := recovery.metadataPath(currentID)

		if exists, err := recovery.pathExists(previousData); err != nil {
			return err
		} else if exists {
			if err := replaceRecoveryPath(recovery.filesystem, previousData, currentData); err != nil {
				return err
			}
			if metadataExists, metadataErr := recovery.pathExists(previousMetadata); metadataErr != nil {
				return metadataErr
			} else if metadataExists {
				if err := replaceRecoveryPath(recovery.filesystem, previousMetadata, currentMetadata); err != nil {
					return err
				}
			} else if err := recovery.removePathIfPresent(currentMetadata); err != nil {
				return err
			}
		} else {
			// Keep a sparse slot intact. Removing a newer slot when its
			// predecessor is absent could erase the only known-good point if
			// a later rotation step fails.
		}
	}
	if err := replaceRecoveryPath(recovery.filesystem, stagedData, recovery.backupPath(recoveryCandidateID(1))); err != nil {
		return err
	}
	return replaceRecoveryPath(recovery.filesystem, stagedMetadata, recovery.metadataPath(recoveryCandidateID(1)))
}

func (recovery *Recovery) preserveActiveLocked(ctx context.Context, now time.Time) (string, string, bool, error) {
	info, err := recovery.filesystem.Lstat(recovery.databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", "", false, ErrRecoveryPathUnsafe
	}
	prefix := "displaced"
	if _, verifyErr := recovery.inspect(ctx, recovery.databasePath); verifyErr != nil {
		prefix = "damaged"
	}
	for suffix := 1; ; suffix++ {
		id := fmt.Sprintf("%s-%d-%d", prefix, now.UnixNano(), suffix)
		path := filepath.Join(recovery.backupDirectory, "codex-folio.sqlite3."+id)
		exists, existsErr := recovery.pathExists(path)
		if existsErr != nil {
			return "", "", false, existsErr
		}
		if exists {
			continue
		}
		if err := copyRecoveryFile(recovery.filesystem, recovery.databasePath, path); err != nil {
			return "", "", false, err
		}
		return id, path, true, nil
	}
}

func (recovery *Recovery) rollbackActiveLocked(preservedPath string, activeExisted bool) error {
	if preservedPath == "" {
		if activeExisted {
			return ErrRecoveryRestore
		}
		return recovery.removePathIfPresent(recovery.databasePath)
	}
	rollbackPath := recovery.databasePath + ".rollback"
	if err := recovery.removePathIfPresent(rollbackPath); err != nil {
		return err
	}
	if err := copyRecoveryFile(recovery.filesystem, preservedPath, rollbackPath); err != nil {
		return err
	}
	if err := replaceRecoveryPath(recovery.filesystem, rollbackPath, recovery.databasePath); err != nil {
		return err
	}
	return nil
}

func (recovery *Recovery) restoreFailure(operationErr, rollbackErr error) error {
	if rollbackErr != nil {
		return coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, operationErr, rollbackErr))
	}
	return coded(apperrors.StoreRecoveryRestoreFailed, errors.Join(ErrRecoveryRestore, operationErr))
}

func (recovery *Recovery) writeMetadata(path string, metadata recoveryMetadata) error {
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	file, err := recovery.filesystem.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = recovery.removePathIfPresent(path)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if !isWindowsRecoveryPath() {
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (recovery *Recovery) readMetadata(id string) (recoveryMetadata, error) {
	info, err := recovery.filesystem.Lstat(recovery.metadataPath(id))
	if err != nil {
		return recoveryMetadata{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !isPrivateRecoveryFile(info.Mode()) {
		return recoveryMetadata{}, ErrRecoveryPathUnsafe
	}
	file, err := recovery.filesystem.Open(recovery.metadataPath(id))
	if err != nil {
		return recoveryMetadata{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var metadata recoveryMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return recoveryMetadata{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return recoveryMetadata{}, errors.New("recovery metadata has trailing data")
	}
	if metadata.Version != recoveryMetadataVersion || metadata.ID != id || metadata.CreatedAt.IsZero() || metadata.SchemaVersion < 0 || metadata.SchemaVersion > CurrentSchemaVersion || (metadata.Reason != BackupReasonMigration && metadata.Reason != BackupReasonRecoveryCheckpoint) {
		return recoveryMetadata{}, errors.New("recovery metadata is invalid")
	}
	return metadata, nil
}

func (recovery *Recovery) pathExists(path string) (bool, error) {
	_, err := recovery.filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (recovery *Recovery) removePathIfPresent(path string) error {
	info, err := recovery.filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrRecoveryPathUnsafe
	}
	return recovery.filesystem.Remove(path)
}

func (recovery *Recovery) backupPath(id string) string {
	return filepath.Join(recovery.backupDirectory, "codex-folio.sqlite3."+id)
}

func (recovery *Recovery) metadataPath(id string) string {
	return recovery.backupPath(id) + ".json"
}

func copyRecoveryFile(filesystem FileSystem, sourcePath, destinationPath string) error {
	sourceInfo, err := filesystem.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return ErrRecoveryPathUnsafe
	}
	source, err := filesystem.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := filesystem.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = removeRecoveryPath(filesystem, destinationPath)
		}
	}()
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		return err
	}
	if err := destination.Sync(); err != nil {
		_ = destination.Close()
		return err
	}
	if !isWindowsRecoveryPath() {
		if err := destination.Chmod(0o600); err != nil {
			_ = destination.Close()
			return err
		}
	}
	if err := destination.Close(); err != nil {
		return err
	}
	info, err := filesystem.Lstat(destinationPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !isPrivateRecoveryFile(info.Mode()) {
		return errors.New("backup permissions are not user-scoped")
	}
	cleanup = false
	return nil
}

func replaceRecoveryPath(filesystem FileSystem, sourcePath, destinationPath string) error {
	err := filesystem.Rename(sourcePath, destinationPath)
	if err == nil {
		return nil
	}
	if !isWindowsRecoveryPath() {
		// Unix rename is atomic and replaces a regular destination. Do not
		// remove the destination after a failed rename: it may be the last
		// known-good recovery point.
		return err
	}
	if removeErr := removeRecoveryPath(filesystem, destinationPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, removeErr)
	}
	if retryErr := filesystem.Rename(sourcePath, destinationPath); retryErr != nil {
		return errors.Join(err, retryErr)
	}
	return nil
}

func removeRecoveryPath(filesystem FileSystem, path string) error {
	info, err := filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrRecoveryPathUnsafe
	}
	return filesystem.Remove(path)
}

func recoveryCandidateID(index int) string {
	return "backup-" + strconv.Itoa(index)
}

func recoveryCandidateIndex(id string) (int, bool) {
	for index := 1; index <= RecoveryBackupCount; index++ {
		if id == recoveryCandidateID(index) {
			return index, true
		}
	}
	return 0, false
}

func safeErrorCode(err error, fallback string) string {
	if code := apperrors.Code(err); code != "" {
		if code == apperrors.StoreOpenFailed || code == apperrors.StoreIntegrityFailed || code == apperrors.StoreSchemaIncompatible || code == apperrors.StoreMigrationPartial {
			return code
		}
	}
	return fallback
}

func isWindowsRecoveryPath() bool {
	return os.PathSeparator == '\\'
}

func isPrivateRecoveryDirectory(mode os.FileMode) bool {
	return isWindowsRecoveryPath() || mode.Perm() == 0o700
}

func isPrivateRecoveryFile(mode os.FileMode) bool {
	return isWindowsRecoveryPath() || mode.Perm() == 0o600
}
