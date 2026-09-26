package codex

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"venkatasudha.com/codex-folio/internal/activity"
)

func TestLocalActivityReaderReadsOnlySupportedThreadMetadata(t *testing.T) {
	home := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/local-state/v5/threads.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(t.TempDir(), "repository")
	otherWorkspace := filepath.Join(t.TempDir(), "other")
	if _, err := database.Exec(`UPDATE threads SET cwd = CASE id WHEN '018f4f70-6f77-7c3f-9b77-93aa087dfc4d' THEN ? ELSE ? END`, workspace, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(home, localStateDatabase))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := NewLocalActivityReader().Probe(context.Background(), home)
	if err != nil || inspection.Status != activity.SourceStatusSupported || inspection.SessionCount != 2 {
		t.Fatalf("Probe() = %#v, %v", inspection, err)
	}

	sessions, err := NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: home, SourceVersion: "0.150.1"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v", sessions)
	}
	first := sessions[1]
	if first.SourceSessionID != "018f4f70-6f77-7c3f-9b77-93aa087dfc4d" || first.Source != activity.SourceLocalMetadata || first.Model != "gpt-5" || first.TokensUsed == nil || *first.TokensUsed != 42 {
		t.Fatalf("first session = %#v", first)
	}
	if first.WorkingDirectory != workspace || !first.StartedAt.Equal(time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)) || !first.LastObservedAt.Equal(time.Date(2026, 9, 6, 12, 5, 0, 0, time.UTC)) {
		t.Fatalf("first session metadata = %#v", first)
	}
	if sessions[0].Model != "" || sessions[0].TokensUsed == nil || *sessions[0].TokensUsed != 0 {
		t.Fatalf("optional metadata = %#v", sessions[0])
	}
	filtered, err := NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: home, SourceVersion: "state_5", SessionIDs: []string{first.SourceSessionID}})
	if err != nil || len(filtered) != 1 || filtered[0].SourceSessionID != first.SourceSessionID {
		t.Fatalf("restricted refresh = %#v, %v", filtered, err)
	}
	filtered, err = NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: home, SourceVersion: "state_5", SessionIDs: []string{}})
	if err != nil || len(filtered) != 0 {
		t.Fatalf("empty authorized set = %#v, %v", filtered, err)
	}
	after, err := os.ReadFile(filepath.Join(home, localStateDatabase))
	if err != nil || string(before) != string(after) {
		t.Fatalf("source database changed during probe/read: %v", err)
	}
}

func TestLocalActivityReaderReturnsNoSessionsWithoutCodexState(t *testing.T) {
	home := t.TempDir()
	inspection, err := NewLocalActivityReader().Probe(context.Background(), home)
	if err != nil || inspection.Status != activity.SourceStatusMissing {
		t.Fatalf("Probe() = %#v, %v", inspection, err)
	}
	sessions, err := NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: home, SourceVersion: "0.150.1"})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("Read() = %#v, %v", sessions, err)
	}
}

func TestLocalActivityReaderReportsUnavailableNonregularSource(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, localStateDatabase), 0o700); err != nil {
		t.Fatal(err)
	}
	inspection, err := NewLocalActivityReader().Probe(context.Background(), home)
	if err != nil || inspection.Status != activity.SourceStatusUnavailable {
		t.Fatalf("Probe() = %#v, %v", inspection, err)
	}
}

func TestLocalActivityReaderReportsUnsupportedAndInvalidSchema(t *testing.T) {
	tests := []struct {
		name         string
		setup        string
		malformedRow bool
		status       string
		readError    error
	}{
		{name: "unsupported layout", setup: `CREATE TABLE unrelated (id TEXT)`, status: activity.SourceStatusUnsupported, readError: activity.ErrActivityUnsupportedSource},
		{name: "missing required column", setup: `CREATE TABLE threads (id TEXT PRIMARY KEY, created_at_ms INTEGER)`, status: activity.SourceStatusSchemaInvalid, readError: activity.ErrActivityInvalidSchema},
		{name: "malformed row", setup: `CREATE TABLE threads (id TEXT, created_at_ms INTEGER, updated_at_ms INTEGER, model TEXT, cwd TEXT, tokens_used INTEGER)`, malformedRow: true, status: activity.SourceStatusSchemaInvalid, readError: activity.ErrActivityInvalidSchema},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			database, err := sql.Open("sqlite", filepath.Join(home, localStateDatabase))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(test.setup); err != nil {
				t.Fatal(err)
			}
			if test.malformedRow {
				if _, err := database.Exec(`INSERT INTO threads VALUES (?, 1000, 2000, NULL, ?, 0)`, "invalid-id", t.TempDir()); err != nil {
					t.Fatal(err)
				}
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			inspection, err := NewLocalActivityReader().Probe(context.Background(), home)
			if err != nil || inspection.Status != test.status {
				t.Fatalf("Probe() = %#v, %v; want %q", inspection, err, test.status)
			}
			_, err = NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: home, SourceVersion: LocalActivitySourceVersion})
			if !errors.Is(err, test.readError) {
				t.Fatalf("Read() error = %v; want %v", err, test.readError)
			}
		})
	}
}
