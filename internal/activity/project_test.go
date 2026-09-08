package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type memoryProjectRepository struct{ records []ProjectRecord }

type testProjectPaths struct{}

func (testProjectPaths) CanonicalRepository(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", ErrPathInvalid
	}
	return filepath.Clean(path), nil
}

func (testProjectPaths) SameRepository(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func (testProjectPaths) Basename(path string) string { return filepath.Base(path) }

func (repository *memoryProjectRepository) SaveProjectRecord(_ context.Context, record ProjectRecord) error {
	for index := range repository.records {
		if repository.records[index].ID == record.ID {
			repository.records[index] = record
			return nil
		}
	}
	repository.records = append(repository.records, record)
	return nil
}

func (repository *memoryProjectRepository) ListProjectRecords(context.Context) ([]ProjectRecord, error) {
	return append([]ProjectRecord(nil), repository.records...), nil
}

func (repository *memoryProjectRepository) ListProjectIdentities(context.Context) ([]ProjectIdentity, error) {
	projects := make([]ProjectIdentity, 0, len(repository.records))
	for _, record := range repository.records {
		projects = append(projects, projectIdentity(record))
	}
	return projects, nil
}

func (repository *memoryProjectRepository) UpdateProjectAlias(_ context.Context, id, alias string, updatedAt time.Time) error {
	for index := range repository.records {
		if repository.records[index].ID == id {
			repository.records[index].Alias = alias
			repository.records[index].UpdatedAt = updatedAt
			return nil
		}
	}
	return ErrProjectNotFound
}

func TestProjectIdentityLifecycleLeavesRepositoryUntouchedAndProjectionPrivate(t *testing.T) {
	root := t.TempDir()
	repositoryPath := filepath.Join(root, "private", "original")
	if err := os.MkdirAll(repositoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repositoryPath, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository := &memoryProjectRepository{}
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	projects, err := NewProjectService(ProjectServiceOptions{Repository: repository, Paths: testProjectPaths{}, Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 16))})
	if err != nil {
		t.Fatal(err)
	}
	created, err := projects.Resolve(context.Background(), repositoryPath, "Secret work")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(created)
	if created.Alias != "Secret work" || created.Basename != "original" || bytes.Contains(encoded, []byte(root)) {
		t.Fatalf("safe projection = %s", encoded)
	}
	resolvedAgain, err := projects.Resolve(context.Background(), repositoryPath, "Ignored replacement")
	if err != nil || resolvedAgain.ID != created.ID || resolvedAgain.Alias != created.Alias {
		t.Fatalf("second Resolve() = %#v, %v", resolvedAgain, err)
	}
	entries, err := os.ReadDir(repositoryPath)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".git" {
		t.Fatalf("repository artifacts = %v, error = %v", entries, err)
	}
	edited, err := projects.EditAlias(context.Background(), created.ID, "Renamed")
	if err != nil || edited.ID != created.ID || edited.Alias != "Renamed" {
		t.Fatalf("EditAlias() = %#v, %v", edited, err)
	}
	moved := filepath.Join(root, "moved")
	if err := os.Rename(repositoryPath, moved); err != nil {
		t.Fatal(err)
	}
	reconciled, err := projects.Reconcile(context.Background(), created.ID, moved)
	if err != nil || reconciled.ID != created.ID || reconciled.Basename != "moved" {
		t.Fatalf("Reconcile() = %#v, %v", reconciled, err)
	}
	if got, err := projects.CanonicalLocation(context.Background(), created.ID); err != nil || got != moved {
		t.Fatalf("CanonicalLocation() = %q, %v", got, err)
	}
}

func TestProjectReconciliationFailurePreservesPriorIdentity(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(path, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	projects, _ := NewProjectService(ProjectServiceOptions{Repository: &memoryProjectRepository{}, Paths: testProjectPaths{}})
	one, err := projects.Resolve(context.Background(), first, "One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Resolve(context.Background(), second, "Two"); err != nil {
		t.Fatal(err)
	}
	for _, failedPath := range []string{second, filepath.Join(root, "missing")} {
		if _, err := projects.Reconcile(context.Background(), one.ID, failedPath); err == nil {
			t.Fatalf("Reconcile(%q) succeeded", failedPath)
		}
		if got, err := projects.CanonicalLocation(context.Background(), one.ID); err != nil || got != first {
			t.Fatalf("prior location = %q, %v", got, err)
		}
	}
}
