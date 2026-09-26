package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserTrustDigestPersistsAcrossStoreReopenAndCanBeRevoked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite3")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{0x42}, 32)
	ctx := context.Background()
	if err := state.GrantBrowserTrust(ctx, digest, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	trusted, err := state.BrowserTrusted(ctx, digest)
	if err != nil || !trusted {
		t.Fatalf("reopened trust = %v, %v", trusted, err)
	}
	if err := state.ForgetBrowser(ctx, digest); err != nil {
		t.Fatal(err)
	}
	trusted, err = state.BrowserTrusted(ctx, digest)
	if err != nil || trusted {
		t.Fatalf("forgotten trust = %v, %v", trusted, err)
	}
	if err := state.GrantBrowserTrust(ctx, digest, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := state.RevokeAllBrowsers(ctx); err != nil {
		t.Fatal(err)
	}
	trusted, err = state.BrowserTrusted(ctx, digest)
	if err != nil || trusted {
		t.Fatalf("revoked trust = %v, %v", trusted, err)
	}
}
