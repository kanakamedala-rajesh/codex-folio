package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandPathWindowsListSemantics(t *testing.T) {
	if !windowsPathContains(`C:\Tools;%USERPROFILE%\bin;C:\Program Files\Folio`, `c:/program files/folio/`) {
		t.Fatal("Windows path matching lost case and separator semantics")
	}
	if windowsPathContains(`C:\Tools;C:\Program Files\Folio`, `C:\Program Files\FolioOther`) {
		t.Fatal("Windows path matching accepted a prefix")
	}
	if windowsPathContains(`C:`, `C:\`) || windowsPathContains(`C:\`, `C:`) {
		t.Fatal("Windows drive-relative path matched drive root")
	}
	if !windowsPathContains(`C:\`, `c:/`) {
		t.Fatal("Windows drive root did not match its slash variant")
	}
}

func TestCommandPathWindowsOfferWithIsolatedUserPath(t *testing.T) {
	t.Setenv("PATH", "")
	executable := filepath.Join(t.TempDir(), "folder with spaces", "codex-folio")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	userPath := `%USERPROFILE%\bin;C:\Other`
	contains := func(directory string) (bool, error) { return windowsPathContains(userPath, directory), nil }
	add := func(directory string) error { userPath = appendWindowsPath(userPath, directory); return nil }
	var stdout, stderr bytes.Buffer
	offer := func(answer string) {
		t.Helper()
		stdout.Reset()
		offerCommandPathSetupForPlatform(strings.NewReader(answer), &stdout, &stderr, executable, "", "", true, "windows", contains, add)
	}
	offer("n\n")
	if userPath != `%USERPROFILE%\bin;C:\Other` {
		t.Fatalf("refusal changed user Path: %q", userPath)
	}
	offer("yes\n")
	accepted := userPath
	if !strings.HasPrefix(accepted, `%USERPROFILE%\bin;C:\Other;`) || !windowsPathContains(accepted, filepath.Dir(executable)) {
		t.Fatalf("acceptance user Path = %q", accepted)
	}
	offer("yes\n")
	if userPath != accepted || !strings.Contains(stdout.String(), "already saved") {
		t.Fatalf("repeat changed user Path or missed guidance: %q, %q", userPath, stdout.String())
	}
}
