// Package store owns the service's versioned, allowlisted SQLite state.
//
// The service opens this adapter only after acquiring the user-scoped state
// owner. Store callers receive a fully initialized and verified database or an
// error; a partially initialized store is never returned.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	// DatabaseFileName is the service-owned SQLite filename below the
	// app-local state root.
	DatabaseFileName = "codex-folio.sqlite3"

	// CurrentSchemaVersion is independent of the product and browser API
	// versions. Every durable shape change must add an ordered migration.
	CurrentSchemaVersion = 29
)

var (
	ErrInvalidDatabasePath = errors.New("database path is invalid")
	ErrDatabaseOpen        = errors.New("database could not be opened")
	ErrIntegrityCheck      = errors.New("database integrity check failed")
	ErrFutureSchema        = errors.New("database schema version is newer than this build")
	ErrPartialMigration    = errors.New("database migration is incomplete")
	ErrSchemaDefinition    = errors.New("database schema does not match the reviewed definition")
	ErrMigration           = errors.New("database migration failed")
)

// Clock supplies migration timestamps. It is an external seam so migration
// ledger tests can be deterministic without changing schema behavior.
type Clock interface {
	Now() time.Time
}

// Vault is the key-opaque encryption boundary used for sensitive fields.
// Store aliases the shared contract so composition roots can inject a
// platform adapter without exposing key material through storage APIs.
type Vault = vault.Vault

// MigrationHooks are test seams for failure injection around a transaction.
// Production composition leaves both callbacks nil. A hook failure rolls back
// the candidate migration and prevents OpenWithOptions from returning a Store.
type MigrationHooks struct {
	Before func(version int) error
	After  func(version int) error
}

// ProfileHooks are test seams for interruption at durable onboarding boundaries.
type ProfileHooks struct {
	BeforeSelection func(string) error
}

// Options configures a store open. OpenDatabase is used by isolated tests to
// inject an opening failure; production uses the pure-Go SQLite adapter.
type Options struct {
	Path          string
	Clock         Clock
	Vault         Vault
	FileSystem    FileSystem
	OpenDatabase  func(path string) (*sql.DB, error)
	MigrationHook MigrationHooks
	ProfileHooks  ProfileHooks
	RecoveryHooks RecoveryHooks
}

// Store is a verified service-owned SQLite database. The raw database handle
// intentionally remains private so callers cannot create an unreviewed write
// path around the schema and state-owner protocol.
type Store struct {
	db           *sql.DB
	path         string
	version      int
	clock        Clock
	vault        Vault
	recovery     *Recovery
	profileHooks ProfileHooks
	operationMu  sync.RWMutex

	closeOnce sync.Once
	closeErr  error
}

// Open opens and verifies a service-owned SQLite database using the default
// pure-Go adapter.
func Open(path string) (*Store, error) {
	return OpenWithOptions(Options{Path: path})
}

// OpenWithOptions opens a database, verifies existing state before writes,
// applies any remaining ordered migrations in transactions, and verifies the
// resulting schema before admitting the Store to callers.
func OpenWithOptions(options Options) (*Store, error) {
	databasePath := strings.TrimSpace(options.Path)
	if databasePath == "" || !filepath.IsAbs(databasePath) {
		return nil, coded(apperrors.StoreOpenFailed, ErrInvalidDatabasePath)
	}

	opener := options.OpenDatabase
	malformedCandidate := false
	if opener == nil {
		malformedCandidate = databaseHasBytes(databasePath)
		if err := prepareDatabaseFile(databasePath); err != nil {
			return nil, coded(apperrors.StoreOpenFailed, err)
		}
		opener = openSQLite
	}

	database, err := opener(databasePath)
	if err != nil || database == nil {
		if err == nil {
			err = ErrDatabaseOpen
		}
		return nil, coded(apperrors.StoreOpenFailed, errors.Join(ErrDatabaseOpen, err))
	}

	return openVerifiedDatabase(database, databasePath, options, malformedCandidate)
}

func openVerifiedDatabase(database *sql.DB, databasePath string, options Options, malformedCandidate bool) (*Store, error) {
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	opened := &Store{db: database, path: databasePath, clock: options.Clock, vault: options.Vault, profileHooks: options.ProfileHooks}
	if opened.clock == nil {
		opened.clock = systemClock{}
	}
	recovery, err := NewRecovery(RecoveryOptions{
		DatabasePath: databasePath,
		Clock:        opened.clock,
		FileSystem:   options.FileSystem,
		Hooks:        options.RecoveryHooks,
	})
	if err != nil {
		return nil, err
	}
	opened.recovery = recovery
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = opened.Close()
		}
	}()

	ctx := context.Background()
	if err := database.PingContext(ctx); err != nil {
		if malformedCandidate {
			return nil, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
		}
		return nil, coded(apperrors.StoreOpenFailed, errors.Join(ErrDatabaseOpen, err))
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		if malformedCandidate {
			return nil, coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
		}
		return nil, coded(apperrors.StoreOpenFailed, errors.Join(ErrDatabaseOpen, err))
	}
	if err := verifyIntegrity(ctx, database); err != nil {
		return nil, err
	}

	version, err := readUserVersion(ctx, database)
	if err != nil {
		return nil, coded(apperrors.StoreOpenFailed, err)
	}
	if version > CurrentSchemaVersion {
		return nil, coded(apperrors.StoreSchemaIncompatible, ErrFutureSchema)
	}
	if version < 0 {
		return nil, coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
	}

	if version == 0 {
		if err := ensureFreshDatabase(ctx, database); err != nil {
			return nil, err
		}
	} else if err := validateMigrationLedger(ctx, database, version); err != nil {
		return nil, err
	}

	if version < CurrentSchemaVersion {
		if _, err := recovery.CreateBackup(ctx, BackupReasonMigration); err != nil {
			return nil, err
		}
	}
	if err := applyMigrations(ctx, database, version, opened.clock, options.MigrationHook); err != nil {
		return nil, err
	}
	if options.RecoveryHooks.AfterMigrationBeforeActivation != nil {
		if err := options.RecoveryHooks.AfterMigrationBeforeActivation(CurrentSchemaVersion); err != nil {
			return nil, coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
	}
	if err := validateMigrationLedger(ctx, database, CurrentSchemaVersion); err != nil {
		return nil, err
	}
	if err := validateSchema(ctx, database); err != nil {
		return nil, err
	}
	if err := verifyIntegrity(ctx, database); err != nil {
		return nil, err
	}

	opened.version = CurrentSchemaVersion
	keepOpen = true
	return opened, nil
}

// Path reports the app-local database path without exposing the SQL adapter.
func (store *Store) Path() string {
	return store.path
}

// SchemaVersion reports the verified persisted schema version.
func (store *Store) SchemaVersion() int {
	return store.version
}

// VerifyIntegrity repeats the read-only integrity checks for an already-open
// store. It does not repair, reset, or replace damaged state.
func (store *Store) VerifyIntegrity() error {
	if store == nil || store.db == nil {
		return coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	store.operationMu.RLock()
	defer store.operationMu.RUnlock()
	return verifyIntegrity(context.Background(), store.db)
}

// Close releases the SQLite handle. It is safe to call more than once.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.closeOnce.Do(func() {
		store.operationMu.Lock()
		defer store.operationMu.Unlock()
		if store.db != nil {
			store.closeErr = store.db.Close()
		}
	})
	if store.closeErr != nil {
		return coded(apperrors.StoreOpenFailed, store.closeErr)
	}
	return nil
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func openSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

func prepareDatabaseFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			if !errors.Is(createErr, os.ErrExist) {
				return createErr
			}
		} else {
			if syncErr := file.Sync(); syncErr != nil {
				_ = file.Close()
				return syncErr
			}
			if closeErr := file.Close(); closeErr != nil {
				return closeErr
			}
			return enforceDatabasePermissions(path)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrInvalidDatabasePath
	}
	return enforceDatabasePermissions(path)
}

func databaseHasBytes(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func enforceDatabasePermissions(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrInvalidDatabasePath
	}
	if os.PathSeparator != '\\' && info.Mode().Perm() != 0o600 {
		return fmt.Errorf("database permissions are not user-scoped")
	}
	return nil
}

func verifyIntegrity(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	defer func() { _ = rows.Close() }()

	var result string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			return coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
		}
		if result == "" {
			result = message
		}
	}
	if err := rows.Err(); err != nil {
		return coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	if result != "ok" {
		return coded(apperrors.StoreIntegrityFailed, fmt.Errorf("%w: %s", ErrIntegrityCheck, result))
	}

	foreignKeys, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	defer func() { _ = foreignKeys.Close() }()
	if foreignKeys.Next() {
		return coded(apperrors.StoreIntegrityFailed, ErrIntegrityCheck)
	}
	if err := foreignKeys.Err(); err != nil {
		return coded(apperrors.StoreIntegrityFailed, errors.Join(ErrIntegrityCheck, err))
	}
	return nil
}

func readUserVersion(ctx context.Context, database *sql.DB) (int, error) {
	var version int64
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	if version > int64(^uint(0)>>1) {
		return 0, ErrSchemaDefinition
	}
	return int(version), nil
}

func ensureFreshDatabase(ctx context.Context, database *sql.DB) error {
	objects, err := queryObjects(ctx, database)
	if err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	if len(objects) != 0 {
		return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
	}
	return nil
}

func applyMigrations(ctx context.Context, database *sql.DB, current int, clock Clock, hooks MigrationHooks) error {
	for _, migration := range migrations() {
		if migration.version <= current {
			continue
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		committed := false
		rollback := func() {
			if !committed {
				_ = tx.Rollback()
			}
		}

		if hooks.Before != nil {
			if err := hooks.Before(migration.version); err != nil {
				rollback()
				return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
			}
		}
		if err := migration.apply(ctx, tx); err != nil {
			rollback()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		if hooks.After != nil {
			if err := hooks.After(migration.version); err != nil {
				rollback()
				return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
			}
		}

		appliedAt := clock.Now().UTC()
		if appliedAt.IsZero() {
			rollback()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, errors.New("migration clock returned zero")))
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)", migration.version, migration.name, appliedAt.Format(time.RFC3339Nano)); err != nil {
			rollback()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); err != nil {
			rollback()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		if err := tx.Commit(); err != nil {
			rollback()
			return coded(apperrors.StoreMigrationFailed, errors.Join(ErrMigration, err))
		}
		committed = true

		if err := verifyIntegrity(ctx, database); err != nil {
			return err
		}
	}
	return nil
}

func validateMigrationLedger(ctx context.Context, database *sql.DB, version int) error {
	objects, err := queryObjects(ctx, database)
	if err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	if !containsObject(objects, "table", "schema_migrations") {
		return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
	}

	rows, err := database.QueryContext(ctx, "SELECT version, name, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	defer func() { _ = rows.Close() }()

	wanted := migrations()
	rowIndex := 0
	for rows.Next() {
		var rowVersion int
		var name, appliedAt string
		if err := rows.Scan(&rowVersion, &name, &appliedAt); err != nil {
			return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
		}
		if rowIndex >= len(wanted) || rowVersion != wanted[rowIndex].version || rowVersion > version || name != wanted[rowIndex].name || strings.TrimSpace(appliedAt) == "" {
			return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
		}
		if _, err := time.Parse(time.RFC3339Nano, appliedAt); err != nil {
			return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
		}
		rowIndex++
	}
	if err := rows.Err(); err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	if rowIndex != version {
		return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
	}
	return nil
}

func queryObjects(ctx context.Context, database *sql.DB) ([]schemaObject, error) {
	rows, err := database.QueryContext(ctx, "SELECT type, name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var objects []schemaObject
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.kind, &object.name); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return objects, nil
}

type schemaObject struct {
	kind string
	name string
}

func containsObject(objects []schemaObject, kind, name string) bool {
	for _, object := range objects {
		if object.kind == kind && object.name == name {
			return true
		}
	}
	return false
}

func coded(code string, cause error) error {
	if cause == nil {
		cause = errors.New(code)
	}
	return apperrors.New(code, cause)
}
