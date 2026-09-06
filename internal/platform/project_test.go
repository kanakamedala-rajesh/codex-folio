package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectPathsFollowFilesystemIdentity(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "Repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	paths := NewProjectPaths()
	canonical, err := paths.CanonicalRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	if !paths.SameRepository(canonical, repository) {
		t.Fatal("canonical and original repository paths compare unequal")
	}

	differentCase := filepath.Join(root, "repository")
	_, caseErr := os.Stat(differentCase)
	if got := paths.SameRepository(canonical, differentCase); got != (caseErr == nil) {
		t.Fatalf("case-variant identity = %t, path exists = %t", got, caseErr == nil)
	}
}
