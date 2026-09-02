package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type migration struct {
	version int
	name    string
	apply   func(context.Context, *sql.Tx) error
}

func migrations() []migration {
	return []migration{
		{
			version: 1,
			name:    "allowlisted-foundation",
			apply:   applyFoundationMigration,
		},
		{
			version: 2,
			name:    "profile-setup-stages",
			apply:   applyProfileSetupMigration,
		},
		{
			version: 3,
			name:    "documented-profile-metadata",
			apply: func(ctx context.Context, tx *sql.Tx) error {
				for _, column := range []string{"documented_login_identity_ciphertext", "documented_workspace_ciphertext"} {
					if _, err := tx.ExecContext(ctx, "ALTER TABLE identity_homes ADD COLUMN "+column+" BLOB CHECK ("+column+" IS NULL OR typeof("+column+") = 'blob')"); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			version: 4,
			name:    "managed-launch-lifecycle",
			apply: func(ctx context.Context, tx *sql.Tx) error {
				for _, statement := range []string{
					"ALTER TABLE managed_launches ADD COLUMN process_id INTEGER CHECK (process_id IS NULL OR process_id > 0)",
					"ALTER TABLE managed_launches ADD COLUMN exit_status INTEGER CHECK (exit_status IS NULL OR (exit_status >= 0 AND exit_status <= 4294967295))",
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
}

func applyFoundationMigration(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range foundationSchemaStatements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func applyProfileSetupMigration(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE profile_setup_stages (
		profile_id TEXT PRIMARY KEY NOT NULL,
		discovery_completed INTEGER NOT NULL CHECK (discovery_completed IN (0, 1)),
		home_completed INTEGER NOT NULL CHECK (home_completed IN (0, 1)),
		authentication_completed INTEGER NOT NULL CHECK (authentication_completed IN (0, 1)),
		validation_completed INTEGER NOT NULL CHECK (validation_completed IN (0, 1)),
		selection_completed INTEGER NOT NULL CHECK (selection_completed IN (0, 1)),
		updated_at TEXT NOT NULL,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`)
	return err
}

var foundationSchemaStatements = []string{
	`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY CHECK (version > 0),
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`,
	`CREATE TABLE identity_profiles (
		profile_id TEXT PRIMARY KEY NOT NULL,
		display_name TEXT NOT NULL,
		status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'needs_reauthentication', 'unavailable')),
		identity_home_id TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE pending_profiles (
		pending_profile_id TEXT PRIMARY KEY NOT NULL,
		display_name TEXT NOT NULL,
		requested_alias TEXT,
		state TEXT NOT NULL CHECK (state IN ('pending', 'needs_attention')),
		identity_home_id TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE identity_homes (
		identity_home_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT NOT NULL,
		ownership TEXT NOT NULL CHECK (ownership IN ('managed', 'referenced')),
		location_ciphertext BLOB CHECK (location_ciphertext IS NULL OR typeof(location_ciphertext) = 'blob'),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`,
	`CREATE TABLE cli_aliases (
		alias_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT NOT NULL,
		alias TEXT NOT NULL COLLATE NOCASE,
		created_at TEXT NOT NULL,
		UNIQUE (alias COLLATE NOCASE),
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`,
	`CREATE TABLE selected_profile (
		selection_id INTEGER PRIMARY KEY CHECK (selection_id = 1),
		profile_id TEXT,
		updated_at TEXT NOT NULL,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`,
	`CREATE TABLE project_identities (
		project_identity_id TEXT PRIMARY KEY NOT NULL,
		project_alias TEXT NOT NULL,
		canonical_path_ciphertext BLOB NOT NULL CHECK (typeof(canonical_path_ciphertext) = 'blob'),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE usage_metrics (
		metric_key TEXT PRIMARY KEY NOT NULL,
		unit TEXT NOT NULL,
		value_kind TEXT NOT NULL CHECK (value_kind IN ('count', 'number', 'duration', 'percentage')),
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE metric_provenance (
		provenance_id TEXT PRIMARY KEY NOT NULL,
		source TEXT NOT NULL CHECK (source IN ('codex_app_server', 'local_metadata', 'derived')),
		source_version TEXT,
		captured_at TEXT NOT NULL,
		freshness TEXT NOT NULL CHECK (freshness IN ('fresh', 'stale', 'unknown')),
		availability TEXT NOT NULL CHECK (availability IN ('available', 'unsupported', 'temporarily_unavailable', 'reauthentication_required'))
	)`,
	`CREATE TABLE metric_availability (
		metric_availability_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT NOT NULL,
		metric_key TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('available', 'unsupported', 'temporarily_unavailable', 'stale', 'reauthentication_required')),
		checked_at TEXT NOT NULL,
		provenance_id TEXT,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id),
		FOREIGN KEY (metric_key) REFERENCES usage_metrics (metric_key),
		FOREIGN KEY (provenance_id) REFERENCES metric_provenance (provenance_id)
	)`,
	`CREATE TABLE usage_observations (
		observation_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT NOT NULL,
		metric_key TEXT NOT NULL,
		provenance_id TEXT,
		metric_availability_id TEXT,
		value REAL NOT NULL,
		unit TEXT NOT NULL,
		window_start TEXT,
		window_end TEXT,
		observed_at TEXT NOT NULL,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id),
		FOREIGN KEY (metric_key) REFERENCES usage_metrics (metric_key),
		FOREIGN KEY (provenance_id) REFERENCES metric_provenance (provenance_id),
		FOREIGN KEY (metric_availability_id) REFERENCES metric_availability (metric_availability_id)
	)`,
	`CREATE TABLE managed_launches (
		managed_launch_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT NOT NULL,
		lease_id TEXT NOT NULL UNIQUE,
		project_identity_id TEXT,
		state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'exited', 'abandoned')),
		started_at TEXT NOT NULL,
		ended_at TEXT,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id),
		FOREIGN KEY (project_identity_id) REFERENCES project_identities (project_identity_id)
	)`,
	`CREATE TABLE observed_sessions (
		observed_session_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT,
		source TEXT NOT NULL CHECK (source IN ('codex_app_server', 'local_metadata')),
		started_at TEXT NOT NULL,
		ended_at TEXT,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`,
	`CREATE TABLE correlation_evidence (
		correlation_evidence_id TEXT PRIMARY KEY NOT NULL,
		managed_launch_id TEXT NOT NULL,
		observed_session_id TEXT NOT NULL,
		confidence TEXT NOT NULL CHECK (confidence IN ('high', 'medium', 'low', 'none')),
		evidence_type TEXT NOT NULL CHECK (evidence_type IN ('explicit', 'temporal', 'project', 'none')),
		observed_at TEXT NOT NULL,
		FOREIGN KEY (managed_launch_id) REFERENCES managed_launches (managed_launch_id),
		FOREIGN KEY (observed_session_id) REFERENCES observed_sessions (observed_session_id)
	)`,
	`CREATE TABLE checkpoints (
		checkpoint_id TEXT PRIMARY KEY NOT NULL,
		project_identity_id TEXT,
		status TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'expired')),
		goal_ciphertext BLOB CHECK (goal_ciphertext IS NULL OR typeof(goal_ciphertext) = 'blob'),
		completed_work_ciphertext BLOB CHECK (completed_work_ciphertext IS NULL OR typeof(completed_work_ciphertext) = 'blob'),
		pending_work_ciphertext BLOB CHECK (pending_work_ciphertext IS NULL OR typeof(pending_work_ciphertext) = 'blob'),
		validation_ciphertext BLOB CHECK (validation_ciphertext IS NULL OR typeof(validation_ciphertext) = 'blob'),
		risks_ciphertext BLOB CHECK (risks_ciphertext IS NULL OR typeof(risks_ciphertext) = 'blob'),
		next_action_ciphertext BLOB CHECK (next_action_ciphertext IS NULL OR typeof(next_action_ciphertext) = 'blob'),
		recovery_metadata_ciphertext BLOB CHECK (recovery_metadata_ciphertext IS NULL OR typeof(recovery_metadata_ciphertext) = 'blob'),
		created_at TEXT NOT NULL,
		expires_at TEXT,
		FOREIGN KEY (project_identity_id) REFERENCES project_identities (project_identity_id)
	)`,
	`CREATE TABLE alerts (
		alert_id TEXT PRIMARY KEY NOT NULL,
		profile_id TEXT,
		category TEXT NOT NULL CHECK (category IN ('capacity', 'reauthentication', 'stale_data', 'collection_failure', 'compatibility')),
		severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'error')),
		state TEXT NOT NULL CHECK (state IN ('open', 'acknowledged', 'resolved')),
		created_at TEXT NOT NULL,
		acknowledged_at TEXT,
		FOREIGN KEY (profile_id) REFERENCES identity_profiles (profile_id)
	)`,
	`CREATE TABLE settings (
		settings_id INTEGER PRIMARY KEY CHECK (settings_id = 1),
		analytics_retention_days INTEGER CHECK (analytics_retention_days IS NULL OR analytics_retention_days >= 30),
		diagnostics_retention_days INTEGER CHECK (diagnostics_retention_days IS NULL OR diagnostics_retention_days >= 1),
		locale TEXT,
		appearance TEXT CHECK (appearance IS NULL OR appearance IN ('system', 'light', 'dark')),
		service_enabled INTEGER CHECK (service_enabled IS NULL OR service_enabled IN (0, 1)),
		experimental_features_enabled INTEGER CHECK (experimental_features_enabled IS NULL OR experimental_features_enabled IN (0, 1)),
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE configuration_packs (
		configuration_pack_id TEXT PRIMARY KEY NOT NULL,
		pack_version TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('draft', 'approved', 'superseded')),
		content_digest BLOB NOT NULL CHECK (typeof(content_digest) = 'blob'),
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE service_ownership (
		ownership_id TEXT PRIMARY KEY NOT NULL,
		process_id INTEGER NOT NULL CHECK (process_id > 0),
		generation TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('starting', 'running', 'stopping')),
		started_at TEXT NOT NULL,
		last_seen_at TEXT NOT NULL
	)`,
	`CREATE TABLE experimental_transactions (
		experimental_transaction_id TEXT PRIMARY KEY NOT NULL,
		capability TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('prepared', 'committing', 'committed', 'rolled_back', 'failed')),
		started_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE diagnostic_aggregates (
		diagnostic_aggregate_id TEXT PRIMARY KEY NOT NULL,
		component TEXT NOT NULL,
		error_code TEXT NOT NULL,
		severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'error')),
		occurrence_count INTEGER NOT NULL CHECK (occurrence_count > 0),
		first_seen_at TEXT NOT NULL,
		last_seen_at TEXT NOT NULL
	)`,
	`CREATE TABLE retention_state (
		retention_state_id INTEGER PRIMARY KEY CHECK (retention_state_id = 1),
		analytics_retention_days INTEGER NOT NULL CHECK (analytics_retention_days >= 30),
		diagnostics_retention_days INTEGER NOT NULL CHECK (diagnostics_retention_days >= 1),
		last_analytics_purge_at TEXT,
		last_diagnostics_purge_at TEXT,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX idx_identity_profiles_status ON identity_profiles (status)`,
	`CREATE INDEX idx_pending_profiles_state ON pending_profiles (state)`,
	`CREATE INDEX idx_identity_homes_profile ON identity_homes (profile_id)`,
	`CREATE INDEX idx_cli_aliases_profile ON cli_aliases (profile_id)`,
	`CREATE INDEX idx_usage_metrics_kind ON usage_metrics (value_kind)`,
	`CREATE INDEX idx_metric_provenance_captured_at ON metric_provenance (captured_at)`,
	`CREATE INDEX idx_metric_availability_profile ON metric_availability (profile_id)`,
	`CREATE INDEX idx_usage_observations_profile_time ON usage_observations (profile_id, observed_at)`,
	`CREATE INDEX idx_managed_launches_profile_time ON managed_launches (profile_id, started_at)`,
	`CREATE INDEX idx_observed_sessions_profile_time ON observed_sessions (profile_id, started_at)`,
	`CREATE INDEX idx_correlation_evidence_launch ON correlation_evidence (managed_launch_id)`,
	`CREATE INDEX idx_checkpoints_project_time ON checkpoints (project_identity_id, created_at)`,
	`CREATE INDEX idx_alerts_profile_state ON alerts (profile_id, state)`,
	`CREATE INDEX idx_configuration_packs_state ON configuration_packs (state)`,
	`CREATE INDEX idx_service_ownership_state ON service_ownership (state)`,
	`CREATE INDEX idx_experimental_transactions_state ON experimental_transactions (state)`,
	`CREATE INDEX idx_diagnostic_aggregates_code ON diagnostic_aggregates (error_code)`,
}

var expectedTables = map[string][]string{
	"alerts":                    {"alert_id", "profile_id", "category", "severity", "state", "created_at", "acknowledged_at"},
	"checkpoints":               {"checkpoint_id", "project_identity_id", "status", "goal_ciphertext", "completed_work_ciphertext", "pending_work_ciphertext", "validation_ciphertext", "risks_ciphertext", "next_action_ciphertext", "recovery_metadata_ciphertext", "created_at", "expires_at"},
	"cli_aliases":               {"alias_id", "profile_id", "alias", "created_at"},
	"configuration_packs":       {"configuration_pack_id", "pack_version", "state", "content_digest", "created_at"},
	"correlation_evidence":      {"correlation_evidence_id", "managed_launch_id", "observed_session_id", "confidence", "evidence_type", "observed_at"},
	"diagnostic_aggregates":     {"diagnostic_aggregate_id", "component", "error_code", "severity", "occurrence_count", "first_seen_at", "last_seen_at"},
	"experimental_transactions": {"experimental_transaction_id", "capability", "state", "started_at", "updated_at"},
	"identity_homes":            {"identity_home_id", "profile_id", "ownership", "location_ciphertext", "documented_login_identity_ciphertext", "documented_workspace_ciphertext", "created_at", "updated_at"},
	"identity_profiles":         {"profile_id", "display_name", "status", "identity_home_id", "created_at", "updated_at"},
	"managed_launches":          {"managed_launch_id", "profile_id", "lease_id", "project_identity_id", "state", "started_at", "ended_at", "process_id", "exit_status"},
	"metric_availability":       {"metric_availability_id", "profile_id", "metric_key", "state", "checked_at", "provenance_id"},
	"metric_provenance":         {"provenance_id", "source", "source_version", "captured_at", "freshness", "availability"},
	"observed_sessions":         {"observed_session_id", "profile_id", "source", "started_at", "ended_at"},
	"pending_profiles":          {"pending_profile_id", "display_name", "requested_alias", "state", "identity_home_id", "created_at", "updated_at"},
	"profile_setup_stages":      {"profile_id", "discovery_completed", "home_completed", "authentication_completed", "validation_completed", "selection_completed", "updated_at"},
	"project_identities":        {"project_identity_id", "project_alias", "canonical_path_ciphertext", "created_at", "updated_at"},
	"retention_state":           {"retention_state_id", "analytics_retention_days", "diagnostics_retention_days", "last_analytics_purge_at", "last_diagnostics_purge_at", "updated_at"},
	"schema_migrations":         {"version", "name", "applied_at"},
	"selected_profile":          {"selection_id", "profile_id", "updated_at"},
	"service_ownership":         {"ownership_id", "process_id", "generation", "state", "started_at", "last_seen_at"},
	"settings":                  {"settings_id", "analytics_retention_days", "diagnostics_retention_days", "locale", "appearance", "service_enabled", "experimental_features_enabled", "updated_at"},
	"usage_metrics":             {"metric_key", "unit", "value_kind", "created_at"},
	"usage_observations":        {"observation_id", "profile_id", "metric_key", "provenance_id", "metric_availability_id", "value", "unit", "window_start", "window_end", "observed_at"},
}

var expectedIndexes = []string{
	"idx_alerts_profile_state",
	"idx_checkpoints_project_time",
	"idx_cli_aliases_profile",
	"idx_configuration_packs_state",
	"idx_correlation_evidence_launch",
	"idx_diagnostic_aggregates_code",
	"idx_experimental_transactions_state",
	"idx_identity_homes_profile",
	"idx_identity_profiles_status",
	"idx_managed_launches_profile_time",
	"idx_metric_availability_profile",
	"idx_metric_provenance_captured_at",
	"idx_observed_sessions_profile_time",
	"idx_pending_profiles_state",
	"idx_service_ownership_state",
	"idx_usage_metrics_kind",
	"idx_usage_observations_profile_time",
}

var sensitiveColumns = map[string][]string{
	"checkpoints":        {"goal_ciphertext", "completed_work_ciphertext", "pending_work_ciphertext", "validation_ciphertext", "risks_ciphertext", "next_action_ciphertext", "recovery_metadata_ciphertext"},
	"identity_homes":     {"location_ciphertext", "documented_login_identity_ciphertext", "documented_workspace_ciphertext"},
	"project_identities": {"canonical_path_ciphertext"},
}

func validateSchema(ctx context.Context, database *sql.DB) error {
	objects, err := queryObjects(ctx, database)
	if err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}

	wantedTables := make(map[string]struct{}, len(expectedTables))
	for table := range expectedTables {
		wantedTables[table] = struct{}{}
	}
	wantedIndexSet := make(map[string]struct{}, len(expectedIndexes))
	for _, index := range expectedIndexes {
		wantedIndexSet[index] = struct{}{}
	}
	for _, object := range objects {
		switch object.kind {
		case "table":
			if _, ok := wantedTables[object.name]; !ok {
				return coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
			}
		case "index":
			if _, ok := wantedIndexSet[object.name]; !ok {
				return coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
			}
		default:
			return coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
		}
	}
	for table := range wantedTables {
		if !containsObject(objects, "table", table) {
			return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
		}
	}
	for _, index := range expectedIndexes {
		if !containsObject(objects, "index", index) {
			return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
		}
	}

	for table, columns := range expectedTables {
		if err := validateColumns(ctx, database, table, columns); err != nil {
			return err
		}
	}
	for table, columns := range sensitiveColumns {
		if err := validateSensitiveColumns(ctx, database, table, columns); err != nil {
			return err
		}
	}
	return nil
}

func validateColumns(ctx context.Context, database *sql.DB, table string, expected []string) error {
	rows, err := database.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(table)))
	if err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	defer func() { _ = rows.Close() }()

	wanted := make(map[string]struct{}, len(expected))
	for _, column := range expected {
		wanted[column] = struct{}{}
	}
	seen := make(map[string]struct{}, len(expected))
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
		}
		if _, ok := wanted[name]; !ok {
			return coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
		}
		seen[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	if len(seen) != len(wanted) {
		return coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
	}
	return nil
}

func validateSensitiveColumns(ctx context.Context, database *sql.DB, table string, columns []string) error {
	var definition string
	if err := database.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&definition); err != nil {
		return coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	lowerDefinition := strings.ToLower(definition)
	for _, column := range columns {
		columnType, err := columnTypeFor(ctx, database, table, column)
		if err != nil {
			return err
		}
		if strings.ToUpper(columnType) != "BLOB" || !strings.Contains(lowerDefinition, "typeof("+strings.ToLower(column)+")") {
			return coded(apperrors.StoreSchemaIncompatible, ErrSchemaDefinition)
		}
	}
	return nil
}

func columnTypeFor(ctx context.Context, database *sql.DB, table, column string) (string, error) {
	rows, err := database.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(table)))
	if err != nil {
		return "", coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return "", coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
		}
		if name == column {
			return columnType, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", coded(apperrors.StoreMigrationPartial, errors.Join(ErrPartialMigration, err))
	}
	return "", coded(apperrors.StoreMigrationPartial, ErrPartialMigration)
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
