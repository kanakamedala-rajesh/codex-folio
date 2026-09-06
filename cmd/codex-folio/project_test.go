package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

func TestProjectCLIManagesSafeAppLocalIdentity(t *testing.T) {
	paths := launchTestPaths(t)
	repositoryRoot := t.TempDir()
	repository := filepath.Join(repositoryRoot, "private", "project")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	secureVault, err := vault.NewMemoryVault(vault.MemoryVaultOptions{Key: bytes.Repeat([]byte{0x51}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	opener := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
		return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
	}
	resolve := func(*string) (platform.Paths, error) { return paths, nil }
	var stdout, stderr bytes.Buffer
	code := runProjectWithDependencies([]string{"resolve", repository, "--alias", "Work", "--json"}, strings.NewReader(""), &stdout, &stderr, resolve, opener, nil)
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("resolve exit/stderr = %d/%q", code, stderr.String())
	}
	var created activity.ProjectIdentity
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Alias != "Work" || created.Basename != "project" || strings.Contains(stdout.String(), repository) {
		t.Fatalf("resolve projection = %q", stdout.String())
	}
	caseVariant := filepath.Join(repositoryRoot, "private", "PROJECT")
	if _, err := os.Stat(caseVariant); err == nil {
		stdout.Reset()
		if code := runProjectWithDependencies([]string{"resolve", caseVariant, "--alias", "Ignored", "--json"}, strings.NewReader(""), &stdout, &stderr, resolve, opener, nil); code != exitSuccess {
			t.Fatalf("case-variant resolve exit/stderr = %d/%q", code, stderr.String())
		}
		var caseResolved activity.ProjectIdentity
		if err := json.Unmarshal(stdout.Bytes(), &caseResolved); err != nil {
			t.Fatal(err)
		}
		if caseResolved.ID != created.ID {
			t.Fatalf("case-variant Project Identity ID = %q, want %q", caseResolved.ID, created.ID)
		}
	}
	entries, err := os.ReadDir(repository)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".git" {
		t.Fatalf("repository artifacts = %v, error = %v", entries, err)
	}

	stdout.Reset()
	if code := runProjectWithDependencies([]string{"edit", created.ID, "--alias", "Renamed", "--json"}, strings.NewReader(""), &stdout, &stderr, resolve, opener, nil); code != exitSuccess {
		t.Fatalf("edit exit/stderr = %d/%q", code, stderr.String())
	}
	var edited activity.ProjectIdentity
	if err := json.Unmarshal(stdout.Bytes(), &edited); err != nil {
		t.Fatal(err)
	}
	if edited.ID != created.ID || edited.Alias != "Renamed" || strings.Contains(stdout.String(), repository) {
		t.Fatalf("edit projection = %q", stdout.String())
	}

	moved := filepath.Join(repositoryRoot, "moved")
	if err := os.Rename(repository, moved); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runProjectWithDependencies([]string{"reconcile", created.ID, moved, "--json"}, strings.NewReader(""), &stdout, &stderr, resolve, opener, nil); code != exitSuccess {
		t.Fatalf("reconcile exit/stderr = %d/%q", code, stderr.String())
	}
	var reconciled activity.ProjectIdentity
	if err := json.Unmarshal(stdout.Bytes(), &reconciled); err != nil {
		t.Fatal(err)
	}
	if reconciled.ID != created.ID || reconciled.Alias != "Renamed" || reconciled.Basename != "moved" || strings.Contains(stdout.String(), moved) {
		t.Fatalf("reconcile projection = %q", stdout.String())
	}
	entries, err = os.ReadDir(moved)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".git" {
		t.Fatalf("moved repository artifacts = %v, error = %v", entries, err)
	}

	stdout.Reset()
	if code := runProjectWithDependencies([]string{"list", "--json"}, strings.NewReader(""), &stdout, &stderr, resolve, opener, nil); code != exitSuccess {
		t.Fatalf("list exit/stderr = %d/%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), moved) || !strings.Contains(stdout.String(), `"alias":"Renamed"`) || !strings.Contains(stdout.String(), `"id":"`+created.ID+`"`) {
		t.Fatalf("list projection = %q", stdout.String())
	}
}
