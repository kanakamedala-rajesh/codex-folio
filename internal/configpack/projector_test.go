package configpack

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectorWritesReviewedFilesPreservesLocalStateAndRegeneratesDeterministically(t *testing.T) {
	home := t.TempDir()
	localPath := filepath.Join(home, "local-state", "threads.sqlite3")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(localPath, []byte("profile-owned"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	files := map[string]string{
		"config/base.toml":   "model = \"gpt-5\"\n",
		"guidance/AGENTS.md": "Keep changes small.\n",
	}
	projector := NewProjector(nil)
	first, err := projector.Project(context.Background(), home, files)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	second, err := projector.Project(context.Background(), home, files)
	if err != nil {
		t.Fatalf("Project() repeat error = %v", err)
	}
	if first.Digest != DigestFiles(files) || first.Digest != second.Digest || len(first.Files) != 2 || first.Files[0] != "config/base.toml" {
		t.Fatalf("projection results = %#v / %#v, want deterministic reviewed output", first, second)
	}
	if content, err := os.ReadFile(filepath.Join(home, "config/base.toml")); err != nil || string(content) != files["config/base.toml"] {
		t.Fatalf("projected config = %q, error = %v", content, err)
	}
	if content, err := os.ReadFile(localPath); err != nil || string(content) != "profile-owned" {
		t.Fatalf("local state = %q, error = %v", content, err)
	}
}

func TestProjectorRemovesOnlyFilesFromThePriorProjection(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "config", "local.toml")
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("local = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	projector := NewProjector(nil)
	if _, err := projector.Project(context.Background(), home, map[string]string{
		"config/base.toml":   "model = \"gpt-5\"\n",
		"guidance/AGENTS.md": "Version A\n",
	}); err != nil {
		t.Fatalf("Project(version A) error = %v", err)
	}
	versionB := map[string]string{"config/base.toml": "model = \"gpt-5-mini\"\n"}
	first, err := projector.Project(context.Background(), home, versionB)
	if err != nil {
		t.Fatalf("Project(version B) error = %v", err)
	}
	second, err := projector.Project(context.Background(), home, versionB)
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("Project(version B replay) = %#v/%v, want deterministic replay", second, err)
	}
	if _, err := os.Stat(filepath.Join(home, "guidance", "AGENTS.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale projected file stat error = %v, want removed", err)
	}
	if content, err := os.ReadFile(local); err != nil || string(content) != "local = true\n" {
		t.Fatalf("local override = %q/%v, want preserved", content, err)
	}
}

func TestProjectorPreservesCodexChangesToPreviouslyProjectedFile(t *testing.T) {
	home := t.TempDir()
	projector := NewProjector(nil)
	path := filepath.Join(home, "config", "base.toml")
	if _, err := projector.Project(context.Background(), home, map[string]string{"config/base.toml": "model = \"reviewed\"\n"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"codex-local\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := projector.Project(context.Background(), home, map[string]string{"config/base.toml": "model = \"next\"\n"}); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "model = \"codex-local\"\n" {
		t.Fatalf("local content = %q/%v", content, err)
	}
}

func TestProjectorLeavesPriorFilesWhenStagingFails(t *testing.T) {
	home := t.TempDir()
	priorPath := filepath.Join(home, "config", "base.toml")
	if err := os.MkdirAll(filepath.Dir(priorPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(priorPath, []byte("model = \"prior\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fs := &failingFileSystem{FileSystem: osFileSystem{}, failWrite: true}
	_, err := NewProjector(fs).Project(context.Background(), home, map[string]string{
		"config/base.toml":   "model = \"next\"\n",
		"guidance/AGENTS.md": "guidance\n",
	})
	if err == nil || !errors.Is(err, ErrProjectionFailed) {
		t.Fatalf("Project() error = %v, want projection failure", err)
	}
	content, readErr := os.ReadFile(priorPath)
	if readErr != nil || string(content) != "model = \"prior\"\n" {
		t.Fatalf("prior config = %q, error = %v, want unchanged", content, readErr)
	}
}

type failingFileSystem struct {
	FileSystem
	failWrite bool
}

func TestProjectorRetainsRecoverableBackupWhenApplicationAndRollbackFail(t *testing.T) {
	home := t.TempDir()
	prior := map[string]string{
		"config/base.toml":   "model = \"prior\"\n",
		"guidance/AGENTS.md": "prior guidance\n",
	}
	manifest := make(map[string]string, len(prior))
	for path, content := range prior {
		target := filepath.Join(home, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest[path] = contentDigest(content)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, projectionManifest), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &renameFailingFileSystem{FileSystem: osFileSystem{}, fail: map[int]bool{5: true, 6: true}}
	_, err = NewProjector(fs).Project(context.Background(), home, map[string]string{
		"config/base.toml":   "model = \"next\"\n",
		"guidance/AGENTS.md": "next guidance\n",
	})
	if err == nil || !errors.Is(err, ErrProjectionFailed) || strings.Contains(err.Error(), home) {
		t.Fatalf("Project() error = %v, want redacted projection failure", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(home, ".codex-folio-projection-*", "prior", "config", "base.toml"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("recoverable backups = %#v/%v, want retained prior config", backups, globErr)
	}
	if content, readErr := os.ReadFile(backups[0]); readErr != nil || string(content) != "model = \"prior\"\n" {
		t.Fatalf("recoverable prior config = %q/%v", content, readErr)
	}
}

type renameFailingFileSystem struct {
	FileSystem
	calls int
	fail  map[int]bool
}

func (fs *renameFailingFileSystem) Rename(oldPath, newPath string) error {
	fs.calls++
	if fs.fail[fs.calls] {
		return errors.New("injected rename failure")
	}
	return fs.FileSystem.Rename(oldPath, newPath)
}

func (fs failingFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	if fs.failWrite {
		return errors.New("injected projection write failure")
	}
	return fs.FileSystem.WriteFile(name, data, perm)
}
