//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandPathInput func([]byte) (int, error)

func (read commandPathInput) Read(buffer []byte) (int, error) { return read(buffer) }

func TestCommandPathOfferAcceptRefuseAndRepeat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	executable := filepath.Join(home, "build with spaces", "codex-folio")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	startup := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(startup, []byte("# existing settings\nexport OTHER=1"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	offerCommandPathSetupFor(strings.NewReader("n\n"), &stdout, &stderr, executable, "", "/bin/zsh", true)
	if data, _ := os.ReadFile(startup); string(data) != "# existing settings\nexport OTHER=1" {
		t.Fatalf("refusal changed shell file: %q", data)
	}
	stdout.Reset()
	offerCommandPathSetupFor(strings.NewReader("yes\n"), &stdout, &stderr, executable, "", "/bin/zsh", true)
	data, err := os.ReadFile(startup)
	if err != nil || !strings.Contains(string(data), "export OTHER=1\n"+shellCommandPathLine(filepath.Dir(executable))+"\n") {
		t.Fatalf("acceptance shell file = %q, %v", data, err)
	}
	stdout.Reset()
	offerCommandPathSetupFor(strings.NewReader("yes\n"), &stdout, &stderr, executable, "", "/bin/zsh", true)
	repeated, _ := os.ReadFile(startup)
	if !bytes.Equal(repeated, data) || !strings.Contains(stdout.String(), "already saved") {
		t.Fatalf("repeat changed shell file or missed guidance: %q, %q", repeated, stdout.String())
	}
}

func TestCommandPathUnsupportedShellAndExistingExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	executable := filepath.Join(home, "app", "codex-folio")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, executable, "", "/bin/fish", true)
	if !strings.Contains(stderr.String(), "Add "+filepath.Dir(executable)+" to your user PATH manually") || !strings.Contains(stderr.String(), "new terminal") {
		t.Fatalf("unsupported shell guidance = %q", stderr.String())
	}
	other := filepath.Join(home, "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "codex-folio"), []byte("other"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	stderr.Reset()
	offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, executable, other, "/bin/zsh", true)
	if !strings.Contains(stderr.String(), "another codex-folio") {
		t.Fatalf("existing command guidance = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf("created shell file despite conflict: %v", err)
	}
}

func TestCommandPathShellQuotedDirectoryAndSymlink(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, ".bashrc")
	directory := filepath.Join(home, "owner's apps")
	if err := addShellCommandPath(file, directory); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(data), `owner'"'"'s apps`) {
		t.Fatalf("quoted shell path = %q, %v", data, err)
	}
	linked := filepath.Join(home, "linked")
	if err := os.Symlink(file, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := addShellCommandPath(linked, filepath.Join(home, "other")); err == nil {
		t.Fatal("shell startup symlink was changed")
	}
}

func TestCommandPathRejectsControlCharacters(t *testing.T) {
	t.Setenv("PATH", "")
	var stdout, stderr bytes.Buffer
	offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, filepath.Join(t.TempDir(), "unsafe\ncommand", "codex-folio"), "", "/bin/zsh", true)
	if !strings.Contains(stderr.String(), "cannot be added safely") {
		t.Fatalf("unsafe directory guidance = %q", stderr.String())
	}
}

func TestCommandPathBashLoginAndInteractiveStartup(t *testing.T) {
	for _, loginFile := range []string{".bash_profile", ".bash_login", ".profile"} {
		t.Run(loginFile, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", "")
			login := filepath.Join(home, loginFile)
			if err := os.WriteFile(login, []byte("# existing login settings\n"), 0600); err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(home, "apps", "codex-folio")
			if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, executable, "", "/bin/bash", true)
			if !strings.Contains(stdout.String(), filepath.Join(home, ".bashrc")+" and "+login) {
				t.Fatalf("approval prompt omitted Bash startup files: %q", stdout.String())
			}
			for _, file := range []string{filepath.Join(home, ".bashrc"), login} {
				data, err := os.ReadFile(file)
				if err != nil || !strings.Contains(string(data), shellCommandPathLine(filepath.Dir(executable))) {
					t.Fatalf("startup file %s = %q, %v; stderr = %q", file, data, err, stderr.String())
				}
			}
			stdout.Reset()
			offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, executable, "", "/bin/bash", true)
			if !strings.Contains(stdout.String(), "already saved") {
				t.Fatalf("repeat guidance = %q", stdout.String())
			}
		})
	}
}

func TestCommandPathRejectsUnixPathSeparator(t *testing.T) {
	t.Setenv("PATH", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	offerCommandPathSetupFor(strings.NewReader("y\n"), &stdout, &stderr, filepath.Join(home, "unsafe:folder", "codex-folio"), "", "/bin/bash", true)
	if !strings.Contains(stderr.String(), "cannot be one PATH entry") || !strings.Contains(stderr.String(), "Run the binary directly") {
		t.Fatalf("unsafe directory guidance = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("created startup file for invalid PATH entry: %v", err)
	}
}

func TestCommandPathBashWritesFilesNamedBeforeApproval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	executable := filepath.Join(home, "apps", "codex-folio")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	newLogin := filepath.Join(home, ".bash_profile")
	answer := strings.NewReader("y\n")
	input := commandPathInput(func(buffer []byte) (int, error) {
		if answer.Len() == 2 {
			if err := os.WriteFile(newLogin, []byte("# new login file\n"), 0600); err != nil {
				return 0, err
			}
		}
		return answer.Read(buffer)
	})
	var stdout, stderr bytes.Buffer
	offerCommandPathSetupFor(input, &stdout, &stderr, executable, "", "/bin/bash", true)
	if !strings.Contains(stdout.String(), filepath.Join(home, ".profile")) {
		t.Fatalf("approval did not name selected login file: %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil || !strings.Contains(string(data), shellCommandPathLine(filepath.Dir(executable))) {
		t.Fatalf("approved login file = %q, %v; stderr = %q", data, err, stderr.String())
	}
	data, err = os.ReadFile(newLogin)
	if err != nil || string(data) != "# new login file\n" {
		t.Fatalf("unapproved login file changed: %q, %v", data, err)
	}
}
