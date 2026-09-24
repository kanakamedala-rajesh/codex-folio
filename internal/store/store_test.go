package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestOpenInitializesAllowlistedFoundationSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "codex-folio.sqlite3")

	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if got := foundation.SchemaVersion(); got != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion() = %d, want %d", got, CurrentSchemaVersion)
	}

	gotTables := queryObjectNames(t, foundation.db, "table")
	wantTables := []string{
		"alert_thresholds",
		"alerts",
		"browser_trust",
		"checkpoints",
		"cli_aliases",
		"collection_schedule_state",
		"configuration_pack_assignments",
		"configuration_pack_overrides",
		"configuration_pack_versions",
		"configuration_packs",
		"correlation_evidence",
		"dashboard_tls_identity",
		"diagnostic_aggregates",
		"experimental_transactions",
		"identity_homes",
		"identity_profiles",
		"managed_launches",
		"metric_availability",
		"metric_provenance",
		"observed_session_assignments",
		"observed_sessions",
		"pending_profiles",
		"profile_quarantine",
		"profile_setup_stages",
		"project_identities",
		"retention_state",
		"schema_migrations",
		"selected_profile",
		"service_ownership",
		"settings",
		"telemetry_state",
		"update_check_state",
		"usage_aggregates",
		"usage_metrics",
		"usage_observations",
		"usage_snapshots",
	}
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("tables = %v, want allowlisted tables %v", gotTables, wantTables)
	}

	var migrationCount int
	if err := foundation.db.QueryRowContext(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("migration ledger query: %v", err)
	}
	if migrationCount != CurrentSchemaVersion {
		t.Fatalf("migration ledger rows = %d, want %d", migrationCount, CurrentSchemaVersion)
	}

	if err := foundation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() on initialized database error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got := queryObjectNames(t, reopened.db, "table"); !reflect.DeepEqual(got, wantTables) {
		t.Fatalf("reopened tables = %v, want %v", got, wantTables)
	}
}

func TestOpenRejectsFutureSchemaBeforeWriting(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "future.sqlite3")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.ExecContext(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set future user_version: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close future database: %v", err)
	}

	foundation, err := Open(databasePath)
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("Open() returned a store for a future schema")
	}
	if err == nil || !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("Open() error = %v, want ErrFutureSchema", err)
	}
	if got := codeOf(err); got != "CF_STORE_SCHEMA_INCOMPATIBLE" {
		t.Fatalf("Open() error code = %q, want schema incompatibility", got)
	}

	check, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("reopen future database: %v", err)
	}
	defer func() { _ = check.Close() }()
	if names := queryObjectNames(t, check, "table"); len(names) != 0 {
		t.Fatalf("future database tables = %v, want no migration writes", names)
	}
}

func TestOpenRejectsPartialMigrationWithoutCompletingIt(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "partial.sqlite3")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create partial ledger: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close partial database: %v", err)
	}

	foundation, err := Open(databasePath)
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("Open() returned a store for a partial migration")
	}
	if err == nil || !errors.Is(err, ErrPartialMigration) {
		t.Fatalf("Open() error = %v, want ErrPartialMigration", err)
	}
	if got := codeOf(err); got != "CF_STORE_MIGRATION_PARTIAL" {
		t.Fatalf("Open() error code = %q, want partial-migration code", got)
	}

	check, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("reopen partial database: %v", err)
	}
	defer func() { _ = check.Close() }()
	if names := queryObjectNames(t, check, "table"); !reflect.DeepEqual(names, []string{"schema_migrations"}) {
		t.Fatalf("partial database tables = %v, want original ledger only", names)
	}
}

func TestOpenRejectsIntegrityFailureBeforeAdmittingWrites(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "integrity.sqlite3")
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("initial Open() error = %v", err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatalf("initial Close() error = %v", err)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := database.ExecContext(context.Background(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys for fixture: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO metric_availability (
		metric_availability_id, profile_id, metric_key, state, checked_at
	) VALUES ('availability-1', 'missing-profile', 'missing-metric', 'available', '2026-08-31T12:00:00Z')`); err != nil {
		t.Fatalf("create integrity failure fixture: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close integrity fixture: %v", err)
	}

	foundation, err = Open(databasePath)
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("Open() returned a store for an integrity failure")
	}
	if err == nil || !errors.Is(err, ErrIntegrityCheck) {
		t.Fatalf("Open() error = %v, want ErrIntegrityCheck", err)
	}
	if got := codeOf(err); got != "CF_STORE_INTEGRITY_FAILED" {
		t.Fatalf("Open() error code = %q, want integrity-failure code", got)
	}
}

func TestOpenClassifiesMalformedDatabaseAsIntegrityFailure(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "malformed.sqlite3")
	if err := os.WriteFile(databasePath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write malformed database: %v", err)
	}

	foundation, err := Open(databasePath)
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("Open() returned a store for a malformed database")
	}
	if err == nil || !errors.Is(err, ErrIntegrityCheck) {
		t.Fatalf("Open() error = %v, want ErrIntegrityCheck", err)
	}
	if got := codeOf(err); got != "CF_STORE_INTEGRITY_FAILED" {
		t.Fatalf("Open() error code = %q, want integrity-failure code", got)
	}
}

func TestMigrationFailureRollsBackTheCandidateSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "failed-migration.sqlite3")
	injected := errors.New("injected migration failure")
	clock := fixedStoreClock{now: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)}

	foundation, err := OpenWithOptions(Options{
		Path:  databasePath,
		Clock: clock,
		MigrationHook: MigrationHooks{
			After: func(version int) error {
				if version == 1 {
					return injected
				}
				return nil
			},
		},
	})
	if foundation != nil {
		_ = foundation.Close()
		t.Fatal("OpenWithOptions() returned a store after injected migration failure")
	}
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("OpenWithOptions() error = %v, want injected failure", err)
	}
	if got := codeOf(err); got != "CF_STORE_MIGRATION_FAILED" {
		t.Fatalf("OpenWithOptions() error code = %q, want migration-failure code", got)
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("reopen failed migration database: %v", err)
	}
	defer func() { _ = database.Close() }()
	var version int
	if err := database.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read rolled-back user_version: %v", err)
	}
	if version != 0 {
		t.Fatalf("rolled-back user_version = %d, want 0", version)
	}
	if names := queryObjectNames(t, database, "table"); len(names) != 0 {
		t.Fatalf("rolled-back tables = %v, want empty database", names)
	}
}

func TestSensitiveStorageColumnsRequireCiphertextAndRemainUnindexed(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "sensitive-columns.sqlite3")
	foundation, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = foundation.Close() }()

	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "identity_homes", name: "location_ciphertext"},
		{table: "project_identities", name: "canonical_path_ciphertext"},
		{table: "checkpoints", name: "goal_ciphertext"},
		{table: "checkpoints", name: "recovery_metadata_ciphertext"},
	} {
		var columnType string
		if err := foundation.db.QueryRowContext(context.Background(), "SELECT type FROM pragma_table_info(?) WHERE name = ?", column.table, column.name).Scan(&columnType); err != nil {
			t.Fatalf("column %s.%s: %v", column.table, column.name, err)
		}
		if columnType != "BLOB" {
			t.Fatalf("column %s.%s type = %q, want BLOB", column.table, column.name, columnType)
		}

	}
	indexRows, err := foundation.db.QueryContext(context.Background(), "SELECT sql FROM sqlite_master WHERE type = 'index' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatalf("query explicit indexes: %v", err)
	}
	defer func() { _ = indexRows.Close() }()
	for indexRows.Next() {
		var definition string
		if err := indexRows.Scan(&definition); err != nil {
			t.Fatalf("scan explicit index: %v", err)
		}
		for _, column := range []string{"location_ciphertext", "canonical_path_ciphertext", "goal_ciphertext", "recovery_metadata_ciphertext"} {
			if strings.Contains(strings.ToLower(definition), column) {
				t.Fatalf("sensitive column %s appears in index %q", column, definition)
			}
		}
	}
	if err := indexRows.Err(); err != nil {
		t.Fatalf("iterate explicit indexes: %v", err)
	}

	if _, err := foundation.db.ExecContext(context.Background(), `INSERT INTO project_identities (
		project_identity_id, project_alias, canonical_path_ciphertext, created_at, updated_at
	) VALUES ('project-1', 'example', 'plaintext', '2026-08-31T12:00:00Z', '2026-08-31T12:00:00Z')`); err == nil {
		t.Fatal("project identity accepted plaintext in ciphertext column")
	}
	if _, err := foundation.db.ExecContext(context.Background(), `INSERT INTO checkpoints (
		checkpoint_id, status, goal_ciphertext, created_at
	) VALUES ('checkpoint-1', 'draft', 'plaintext', '2026-08-31T12:00:00Z')`); err == nil {
		t.Fatal("checkpoint accepted plaintext in ciphertext column")
	}
}

func TestOpenReportsInjectedDatabaseFailureThroughTheStoreSeam(t *testing.T) {
	injected := errors.New("injected database open failure")
	foundation, err := OpenWithOptions(Options{
		Path: filepath.Join(t.TempDir(), "injected.sqlite3"),
		OpenDatabase: func(path string) (*sql.DB, error) {
			return nil, injected
		},
	})
	if foundation != nil {
		t.Fatal("OpenWithOptions() returned a store after injected open failure")
	}
	if err == nil || !errors.Is(err, injected) {
		t.Fatalf("OpenWithOptions() error = %v, want injected open failure", err)
	}
	if got := codeOf(err); got != "CF_STORE_OPEN_FAILED" {
		t.Fatalf("OpenWithOptions() error code = %q, want open-failure code", got)
	}
}

type fixedStoreClock struct {
	now time.Time
}

func (clock fixedStoreClock) Now() time.Time {
	return clock.now
}

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	return apperrors.Code(err)
}

func queryObjectNames(t *testing.T, database *sql.DB, objectType string) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite_%' ORDER BY name", objectType)
	if err != nil {
		t.Fatalf("query %s names: %v", objectType, err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan %s name: %v", objectType, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s names: %v", objectType, err)
	}
	sort.Strings(names)
	return names
}
