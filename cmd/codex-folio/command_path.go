package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/term"
)

func offerCommandPathSetup(input io.Reader, stdout, stderr io.Writer) {
	prompt, ok := input.(*foregroundPromptInput)
	if !ok || prompt.terminal == nil || !term.IsTerminal(int(prompt.terminal.Fd())) {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "codex-folio: cannot locate this binary for optional PATH setup; direct execution still works")
		return
	}
	offerCommandPathSetupFor(input, stdout, stderr, executable, os.Getenv("PATH"), os.Getenv("SHELL"), true)
}

func offerCommandPathSetupFor(input io.Reader, stdout, stderr io.Writer, executable, currentPath, shell string, interactive bool) {
	offerCommandPathSetupForPlatform(input, stdout, stderr, executable, currentPath, shell, interactive, runtime.GOOS, windowsCommandPathContains, addWindowsCommandPath)
}

func offerCommandPathSetupForPlatform(input io.Reader, stdout, stderr io.Writer, executable, currentPath, shell string, interactive bool, goos string, windowsContains func(string) (bool, error), windowsAdd func(string) error) {
	if !interactive {
		return
	}
	directory := filepath.Clean(filepath.Dir(executable))
	if directory == "." || !filepath.IsAbs(directory) {
		fmt.Fprintln(stderr, "codex-folio: optional PATH setup needs an absolute binary path; direct execution still works")
		return
	}
	if strings.IndexFunc(directory, unicode.IsControl) >= 0 || goos == "windows" && strings.Contains(directory, ";") {
		fmt.Fprintf(stderr, "codex-folio: directory %q cannot be added safely to PATH; continue using the direct binary path.\n", directory)
		return
	}
	if goos != "windows" && strings.Contains(directory, ":") {
		fmt.Fprintf(stderr, "codex-folio: directory %q contains ':' and cannot be one PATH entry. Run the binary directly or move it to a directory without ':' before setting up the plain command.\n", directory)
		return
	}
	if found, err := exec.LookPath("codex-folio"); err == nil {
		if sameExecutable(found, executable) {
			return
		}
		fmt.Fprintf(stderr, "codex-folio: another codex-folio is already on PATH at %s; keeping it unchanged. Run %s directly or resolve the conflict manually.\n", found, executable)
		return
	}
	for _, entry := range filepath.SplitList(currentPath) {
		if samePath(entry, directory) {
			return
		}
	}
	if goos == "windows" {
		changed, err := windowsContains(directory)
		if err == nil && changed {
			fmt.Fprintln(stdout, "Command PATH setup is already saved. Open a new terminal to use codex-folio; this terminal can still use the direct binary path.")
			return
		}
	} else if files, err := commandShellFiles(shell); err == nil {
		installed := true
		for _, file := range files {
			found, readErr := shellCommandPathContains(file, directory)
			if readErr != nil || !found {
				installed = false
				break
			}
		}
		if installed {
			fmt.Fprintln(stdout, "Command PATH setup is already saved. Open a new terminal to use codex-folio.")
			return
		}
	}
	changeTarget := "your shell startup file"
	var shellFiles []string
	var shellErr error
	if goos == "windows" {
		changeTarget = "your Windows user PATH"
	} else {
		shellFiles, shellErr = commandShellFiles(shell)
		if shellErr == nil {
			changeTarget = strings.Join(shellFiles, " and ")
		}
	}
	fmt.Fprintf(stdout, "Add %s to your user PATH for the plain codex-folio command? This changes %s only. [y/N]: ", directory, changeTarget)
	answer, err := readCompanionSetupLine(input)
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "codex-folio: could not read PATH choice: %v\n", err)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		fmt.Fprintln(stdout, "PATH unchanged; continue using the direct binary path.")
		return
	}
	if goos == "windows" {
		err = windowsAdd(directory)
	} else {
		err = shellErr
		if err == nil {
			for _, file := range shellFiles {
				if err = addShellCommandPath(file, directory); err != nil {
					break
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio: automatic PATH setup unavailable: %v. Add %s to your user PATH manually; open a new terminal afterward. Direct execution still works.\n", err, directory)
		return
	}
	fmt.Fprintln(stdout, "Command PATH saved. Open a new terminal to use codex-folio; this terminal can continue with the direct binary path.")
}

func sameExecutable(a, b string) bool {
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func windowsPathContains(value, directory string) bool {
	for _, entry := range strings.Split(value, ";") {
		left := normalizeWindowsPathEntry(entry)
		right := normalizeWindowsPathEntry(directory)
		if strings.EqualFold(left, right) {
			return true
		}
	}
	return false
}

func normalizeWindowsPathEntry(path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "/", `\`)
	trimmed := strings.TrimRight(path, `\`)
	if len(trimmed) == 2 && trimmed[1] == ':' && len(path) > 2 {
		return trimmed + `\`
	}
	if trimmed == "" && path != "" {
		return `\`
	}
	return trimmed
}

func appendWindowsPath(value, directory string) string {
	if windowsPathContains(value, directory) {
		return value
	}
	if value != "" && !strings.HasSuffix(value, ";") {
		value += ";"
	}
	return value + directory
}

func commandShellFiles(shell string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("home directory unavailable")
	}
	switch filepath.Base(shell) {
	case "bash":
		login := filepath.Join(home, ".profile")
		for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
			file := filepath.Join(home, name)
			if _, err := os.Lstat(file); err == nil {
				login = file
				break
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		return []string{filepath.Join(home, ".bashrc"), login}, nil
	case "zsh":
		return []string{filepath.Join(home, ".zshrc")}, nil
	default:
		return nil, errors.New("supported automatic shells are bash and zsh")
	}
}

func shellCommandPathLine(directory string) string {
	quoted := strings.ReplaceAll(directory, "'", "'\"'\"'")
	return "export PATH='" + quoted + "':\"$PATH\" # codex-folio command path"
}

func shellCommandPathContains(file, directory string) (bool, error) {
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == shellCommandPathLine(directory) {
			return true, nil
		}
	}
	return false, nil
}

func addShellCommandPath(file, directory string) error {
	info, err := os.Lstat(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("shell startup file is not a regular file")
	}
	installed, err := shellCommandPathContains(file, directory)
	if err != nil || installed {
		return err
	}
	output, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	prefix := ""
	if info != nil && info.Size() > 0 {
		data, readErr := os.ReadFile(file)
		if readErr != nil {
			return readErr
		}
		if data[len(data)-1] != '\n' {
			prefix = "\n"
		}
	}
	_, err = io.WriteString(output, prefix+shellCommandPathLine(directory)+"\n")
	return err
}
