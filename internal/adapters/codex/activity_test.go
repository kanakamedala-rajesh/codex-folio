package codex

import (
	"context"
	"database/sql"
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
}

func TestLocalActivityReaderReturnsNoSessionsWithoutCodexState(t *testing.T) {
	sessions, err := NewLocalActivityReader().Read(context.Background(), activity.ReadRequest{IdentityHome: t.TempDir(), SourceVersion: "0.150.1"})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("Read() = %#v, %v", sessions, err)
	}
}
