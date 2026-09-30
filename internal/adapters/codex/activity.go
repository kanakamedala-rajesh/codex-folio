package codex

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"venkatasudha.com/codex-folio/internal/activity"
)

const (
	localStateDatabase         = "state_5.sqlite"
	LocalActivitySourceVersion = "state_5"
)

type LocalActivityReader struct{}

func NewLocalActivityReader() *LocalActivityReader { return &LocalActivityReader{} }

func (reader *LocalActivityReader) Probe(ctx context.Context, identityHome string) (activity.SourceInspection, error) {
	if !filepath.IsAbs(identityHome) {
		return activity.SourceInspection{}, activity.ErrActivityInvalid
	}
	info, err := os.Stat(filepath.Join(identityHome, localStateDatabase))
	if errors.Is(err, os.ErrNotExist) {
		return activity.SourceInspection{Status: activity.SourceStatusMissing}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return activity.SourceInspection{Status: activity.SourceStatusUnavailable}, nil
	}
	sessions, err := reader.Read(ctx, activity.ReadRequest{IdentityHome: identityHome, SourceVersion: LocalActivitySourceVersion})
	switch {
	case errors.Is(err, activity.ErrActivityUnsupportedSource):
		return activity.SourceInspection{Status: activity.SourceStatusUnsupported}, nil
	case errors.Is(err, activity.ErrActivityInvalidSchema):
		return activity.SourceInspection{Status: activity.SourceStatusSchemaInvalid}, nil
	case errors.Is(err, activity.ErrActivityUnavailable):
		return activity.SourceInspection{Status: activity.SourceStatusUnavailable}, nil
	case err != nil:
		return activity.SourceInspection{}, err
	default:
		return activity.SourceInspection{Status: activity.SourceStatusSupported, SessionCount: len(sessions)}, nil
	}
}

func (*LocalActivityReader) Read(ctx context.Context, request activity.ReadRequest) ([]activity.SourceSession, error) {
	if strings.TrimSpace(request.SourceVersion) == "" || !filepath.IsAbs(request.IdentityHome) {
		return nil, activity.ErrActivityInvalid
	}
	path := filepath.Join(request.IdentityHome, localStateDatabase)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []activity.SourceSession{}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, activity.ErrActivityUnavailable
	}
	dsnPath := filepath.ToSlash(path)
	if volume := filepath.VolumeName(path); len(volume) == 2 && volume[1] == ':' {
		dsnPath = "/" + dsnPath
	}
	dsn := (&url.URL{Scheme: "file", Path: dsnPath, RawQuery: "mode=ro"}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, activity.ErrActivityUnavailable
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if err := validateThreadSchema(contextOrBackground(ctx), database); err != nil {
		return nil, err
	}
	query := `SELECT id, created_at_ms, updated_at_ms, model, cwd, tokens_used FROM threads`
	args := []any{}
	if request.SessionIDs != nil {
		if len(request.SessionIDs) == 0 {
			return []activity.SourceSession{}, nil
		}
		query += ` WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(request.SessionIDs)), ",") + `)`
		for _, id := range request.SessionIDs {
			args = append(args, id)
		}
	}
	rows, err := database.QueryContext(contextOrBackground(ctx), query+` ORDER BY created_at_ms DESC, id DESC`, args...)
	if err != nil {
		return nil, activity.ErrActivityInvalidSchema
	}
	defer rows.Close()
	sessions := []activity.SourceSession{}
	for rows.Next() {
		var id, cwd string
		var createdAt, updatedAt, tokensUsed int64
		var model sql.NullString
		if err := rows.Scan(&id, &createdAt, &updatedAt, &model, &cwd, &tokensUsed); err != nil {
			return nil, activity.ErrActivityInvalidSchema
		}
		startedAt, lastObservedAt := time.UnixMilli(createdAt).UTC(), time.UnixMilli(updatedAt).UTC()
		if !validUUID(id) || !filepath.IsAbs(cwd) || tokensUsed < 0 || lastObservedAt.Before(startedAt) || invalidOptionalLabel(model.String, 200) {
			return nil, activity.ErrActivityInvalidSchema
		}
		tokens := tokensUsed
		sessions = append(sessions, activity.SourceSession{
			SourceSessionID: strings.ToLower(id), Source: activity.SourceLocalMetadata, StartedAt: startedAt,
			LastObservedAt: lastObservedAt, WorkingDirectory: filepath.Clean(cwd), Model: model.String, TokensUsed: &tokens,
		})
	}
	if rows.Err() != nil {
		return nil, activity.ErrActivityInvalidSchema
	}
	return sessions, nil
}

func validateThreadSchema(ctx context.Context, database *sql.DB) error {
	var tableName string
	err := database.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'threads'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.ErrActivityUnsupportedSource
	}
	if err != nil {
		return activity.ErrActivityInvalidSchema
	}
	rows, err := database.QueryContext(ctx, `PRAGMA table_info(threads)`)
	if err != nil {
		return activity.ErrActivityInvalidSchema
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return activity.ErrActivityInvalidSchema
		}
		columns[name] = true
	}
	if rows.Err() != nil {
		return activity.ErrActivityInvalidSchema
	}
	for _, name := range []string{"id", "created_at_ms", "updated_at_ms", "model", "cwd", "tokens_used"} {
		if !columns[name] {
			return activity.ErrActivityInvalidSchema
		}
	}
	return nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func invalidOptionalLabel(value string, limit int) bool {
	return len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0
}
