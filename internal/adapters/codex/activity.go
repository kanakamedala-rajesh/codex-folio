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

const localStateDatabase = "state_5.sqlite"

type LocalActivityReader struct{}

func NewLocalActivityReader() *LocalActivityReader { return &LocalActivityReader{} }

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
	rows, err := database.QueryContext(contextOrBackground(ctx), `SELECT id, created_at_ms, updated_at_ms, model, cwd, tokens_used FROM threads ORDER BY created_at_ms DESC, id DESC`)
	if err != nil {
		return nil, activity.ErrActivityUnavailable
	}
	defer rows.Close()
	sessions := []activity.SourceSession{}
	for rows.Next() {
		var id, cwd string
		var createdAt, updatedAt, tokensUsed int64
		var model sql.NullString
		if err := rows.Scan(&id, &createdAt, &updatedAt, &model, &cwd, &tokensUsed); err != nil {
			return nil, activity.ErrActivityUnavailable
		}
		startedAt, lastObservedAt := time.UnixMilli(createdAt).UTC(), time.UnixMilli(updatedAt).UTC()
		if !validUUID(id) || !filepath.IsAbs(cwd) || tokensUsed < 0 || lastObservedAt.Before(startedAt) || invalidOptionalLabel(model.String, 200) {
			return nil, activity.ErrActivityUnavailable
		}
		tokens := tokensUsed
		sessions = append(sessions, activity.SourceSession{
			SourceSessionID: strings.ToLower(id), Source: activity.SourceLocalMetadata, StartedAt: startedAt,
			LastObservedAt: lastObservedAt, WorkingDirectory: filepath.Clean(cwd), Model: model.String, TokensUsed: &tokens,
		})
	}
	if rows.Err() != nil {
		return nil, activity.ErrActivityUnavailable
	}
	return sessions, nil
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
