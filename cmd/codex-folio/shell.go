package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/platform"
)

const (
	shellIntegrationFormat = "1"
	shellKindCompletion    = "completion"
	shellKindWrapper       = "wrapper"
	shellMarker            = "codex-folio-shell-integration"
)

type shellOptions struct {
	serviceOptions
	shell   string
	wrapper bool
}

type shellIntegrationResult struct {
	Action      string `json:"action"`
	Shell       string `json:"shell"`
	Kind        string `json:"kind"`
	Version     string `json:"version"`
	Path        string `json:"path"`
	SourceLine  string `json:"source_line"`
	RemovalLine string `json:"removal_line"`
	Present     bool   `json:"present"`
}

type shellIntegrationHeader struct {
	Format  string
	Kind    string
	Shell   string
	Version string
	Digest  string
}

func runShell(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver, version string) int {
	if len(args) == 0 {
		return writeShellUsage(stderr, "a shell action is required")
	}
	action := args[0]
	if action != "generate" && action != "remove" {
		return writeShellUsage(stderr, "unknown shell action")
	}
	options, err := parseShellOptions(args[1:])
	if err != nil {
		return writeShellUsage(stderr, "invalid shell arguments")
	}
	shell, err := resolveShell(options.shell)
	if err != nil {
		return writeShellUsage(stderr, err.Error())
	}
	kind := shellKindCompletion
	if options.wrapper {
		kind = shellKindWrapper
	}
	version = shellIntegrationVersion(version)
	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		if options.stateRoot != nil {
			code := apperrors.Code(err)
			if code == apperrors.PlatformStatePathInvalid || code == apperrors.PlatformStatePathUnsafe {
				return writeShellUsage(stderr, serviceRemediation(code))
			}
		}
		return writeServiceErrorWithDiagnostics(stderr, err, nil)
	}

	path := shellIntegrationFilePath(paths, shell, kind)
	filesystem := platform.NewFileSystem()
	sourceLine := shellSourceLine(shell, path)
	result := shellIntegrationResult{
		Shell:       shell,
		Kind:        kind,
		Version:     version,
		Path:        path,
		SourceLine:  sourceLine,
		RemovalLine: shellRemovalLine(shell, kind, options.stateRoot),
	}
	if action == "generate" {
		content := renderShellIntegration(shell, kind, version)
		if err := writeShellIntegration(filesystem, path, content, version); err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, nil)
		}
		result.Action = "generated"
		result.Present = true
	} else {
		present, err := removeShellIntegration(filesystem, path, shell, kind, version)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, nil)
		}
		result.Present = present
		if present {
			result.Action = "removed"
		} else {
			result.Action = "absent"
		}
	}
	if options.json {
		if err := writeServiceJSON(stdout, result); err != nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIInternal, err), nil)
		}
		return exitSuccess
	}
	writeShellResult(stdout, result)
	return exitSuccess
}

func parseShellOptions(args []string) (shellOptions, error) {
	var options shellOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			if options.json {
				return shellOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--wrapper":
			if options.wrapper {
				return shellOptions{}, errors.New("--wrapper may be supplied only once")
			}
			options.wrapper = true
		case arg == "--shell":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.shell != "" {
				return shellOptions{}, errors.New("--shell requires one value")
			}
			index++
			options.shell = strings.TrimSpace(args[index])
			if options.shell == "" {
				return shellOptions{}, errors.New("--shell requires one value")
			}
		case strings.HasPrefix(arg, "--shell="):
			if options.shell != "" {
				return shellOptions{}, errors.New("--shell may be supplied only once")
			}
			options.shell = strings.TrimSpace(strings.TrimPrefix(arg, "--shell="))
			if options.shell == "" {
				return shellOptions{}, errors.New("--shell requires one value")
			}
		case arg == "--state-root":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return shellOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options.serviceOptions, args[index]); err != nil {
				return shellOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options.serviceOptions, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return shellOptions{}, err
			}
		default:
			return shellOptions{}, errors.New("unexpected shell argument")
		}
	}
	return options, nil
}

func resolveShell(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		if runtime.GOOS == "windows" {
			return "powershell", nil
		}
		name := filepath.Base(strings.TrimSpace(os.Getenv("SHELL")))
		if name == "zsh" {
			return "zsh", nil
		}
		return "bash", nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bash":
		return "bash", nil
	case "zsh":
		return "zsh", nil
	case "powershell", "pwsh":
		return "powershell", nil
	default:
		return "", errors.New("supported shells are bash, zsh, and powershell")
	}
}

func shellIntegrationVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		version = strings.TrimSpace(buildinfo.Version)
	}
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		return "unknown"
	}
	return version
}

func shellIntegrationFilePath(paths platform.Paths, shell, kind string) string {
	root := paths.ShellIntegration
	if root == "" {
		root = filepath.Join(paths.Root, "shell-integration")
	}
	extension := ".sh"
	if shell == "powershell" {
		extension = ".ps1"
	}
	return filepath.Join(root, shell+"-"+kind+extension)
}

func shellSourceLine(shell, path string) string {
	if shell == "powershell" {
		return ". " + powershellQuote(path)
	}
	return "source " + posixQuote(path)
}

func shellRemovalLine(shell, kind string, stateRoot *string) string {
	line := "codex-folio shell remove --shell " + shell
	if kind == shellKindWrapper {
		line += " --wrapper"
	}
	if stateRoot != nil {
		line += " --state-root " + shellQuote(shell, *stateRoot)
	}
	return line
}

func shellQuote(shell, value string) string {
	if shell == "powershell" {
		return powershellQuote(value)
	}
	return posixQuote(value)
}

func posixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func renderShellIntegration(shell, kind, version string) string {
	body := shellIntegrationBody(shell, kind)
	digest := sha256.Sum256([]byte(body))
	return fmt.Sprintf("# %s format=%s kind=%s shell=%s version=%s digest=sha256:%s\n%s", shellMarker, shellIntegrationFormat, kind, shell, version, hex.EncodeToString(digest[:]), body)
}

func shellIntegrationBody(shell, kind string) string {
	if kind == shellKindWrapper {
		if shell == "powershell" {
			return "function Invoke-CodexFolioLaunch {\n    & codex-folio launch @args\n}\n"
		}
		return "codex_folio_launch() {\n  command codex-folio launch \"$@\"\n}\n"
	}
	commands := "version service codex profile launch select configuration-pack shell help"
	switch shell {
	case "zsh":
		return fmt.Sprintf("#compdef codex-folio\n\n_codex_folio() {\n  _arguments '1:command:(%s)'\n}\n\ncompdef _codex_folio codex-folio\n", commands)
	case "powershell":
		quotedCommands := make([]string, 0, len(strings.Fields(commands)))
		for _, command := range strings.Fields(commands) {
			quotedCommands = append(quotedCommands, "'"+command+"'")
		}
		return fmt.Sprintf("Register-ArgumentCompleter -Native -CommandName codex-folio -ScriptBlock {\n    param($wordToComplete, $commandAst, $cursorPosition)\n    @(%s) | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n    }\n}\n", strings.Join(quotedCommands, ", "))
	default:
		return fmt.Sprintf("_codex_folio_complete() {\n  local current=\"${COMP_WORDS[COMP_CWORD]}\"\n  local commands=\"%s\"\n  COMPREPLY=( $(compgen -W \"$commands\" -- \"$current\") )\n}\ncomplete -F _codex_folio_complete codex-folio\n", commands)
	}
}

func writeShellIntegration(filesystem platform.FileSystem, path, content, expectedVersion string) error {
	if !filepath.IsAbs(path) {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("shell integration path is not absolute"))
	}
	directory := filepath.Dir(path)
	if err := rejectShellSymlinkAncestors(filesystem, directory); err != nil {
		return err
	}
	if err := filesystem.MkdirAll(directory, 0o700); err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if err := filesystem.EnforcePrivatePermissions(directory); err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if err := ensureShellIntegrationDirectory(filesystem, directory); err != nil {
		return err
	}
	info, err := filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeShellIntegrationFile(filesystem, path, content); err != nil {
			return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
		}
		return nil
	}
	if err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration is not a regular file"))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration permissions are not private"))
	}
	existing, err := readShellIntegrationFile(filesystem, path)
	if err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if err := validateShellIntegration(existing, path, expectedVersion); err != nil {
		return err
	}
	if string(existing) == content {
		return nil
	}
	if err := writeShellIntegrationFile(filesystem, path, content); err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	return nil
}

func readShellIntegrationFile(filesystem platform.FileSystem, path string) ([]byte, error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return content, nil
}

func writeShellIntegrationFile(filesystem platform.FileSystem, path, content string) error {
	file, err := filesystem.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(file, strings.NewReader(content))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return filesystem.EnforcePrivatePermissions(path)
}

func ensureShellIntegrationDirectory(filesystem platform.FileSystem, directory string) error {
	info, err := filesystem.Lstat(directory)
	if err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("shell integration directory is not a directory"))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("shell integration directory permissions are not private"))
	}
	return nil
}

func removeShellIntegration(filesystem platform.FileSystem, path, shell, kind, expectedVersion string) (bool, error) {
	if !filepath.IsAbs(path) {
		return false, apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("shell integration path is not absolute"))
	}
	if err := rejectShellSymlinkAncestors(filesystem, filepath.Dir(path)); err != nil {
		return false, err
	}
	info, err := filesystem.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration is not a regular file"))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return false, apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration permissions are not private"))
	}
	content, err := readShellIntegrationFile(filesystem, path)
	if err != nil {
		return false, apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	if err := validateShellIntegrationFor(content, shell, kind, expectedVersion); err != nil {
		return false, err
	}
	if err := filesystem.Remove(path); err != nil {
		return false, apperrors.New(apperrors.CLIShellIntegrationFailed, err)
	}
	return true, nil
}

func rejectShellSymlinkAncestors(filesystem platform.FileSystem, path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := filesystem.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return apperrors.New(apperrors.CLIShellIntegrationFailed, err)
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("shell integration path contains a symbolic link"))
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func validateShellIntegration(content []byte, path, expectedVersion string) error {
	base := filepath.Base(path)
	for _, shell := range []string{"bash", "zsh", "powershell"} {
		for _, kind := range []string{shellKindCompletion, shellKindWrapper} {
			if strings.HasPrefix(base, shell+"-"+kind) {
				return validateShellIntegrationFor(content, shell, kind, expectedVersion)
			}
		}
	}
	return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration has an unknown name"))
}

func validateShellIntegrationFor(content []byte, shell, kind, _ string) error {
	header, _, err := parseShellIntegration(content)
	if err != nil {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, err)
	}
	if header.Format != shellIntegrationFormat || header.Shell != shell || header.Kind != kind {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration is incompatible"))
	}
	if string(content) != renderShellIntegration(shell, kind, header.Version) {
		return apperrors.New(apperrors.CLIShellIntegrationInvalid, errors.New("generated shell integration was changed"))
	}
	return nil
}

func parseShellIntegration(content []byte) (shellIntegrationHeader, []byte, error) {
	lineEnd := strings.IndexByte(string(content), '\n')
	if lineEnd < 0 {
		return shellIntegrationHeader{}, nil, errors.New("generated shell integration header is missing")
	}
	fields := strings.Fields(string(content[:lineEnd]))
	if len(fields) < 6 || fields[0] != "#" || fields[1] != shellMarker {
		return shellIntegrationHeader{}, nil, errors.New("generated shell integration header is invalid")
	}
	values := make(map[string]string, len(fields)-2)
	for _, field := range fields[2:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" || value == "" {
			return shellIntegrationHeader{}, nil, errors.New("generated shell integration header is invalid")
		}
		if _, exists := values[key]; exists {
			return shellIntegrationHeader{}, nil, errors.New("generated shell integration header is invalid")
		}
		values[key] = value
	}
	header := shellIntegrationHeader{Format: values["format"], Kind: values["kind"], Shell: values["shell"], Version: values["version"], Digest: values["digest"]}
	if header.Format == "" || header.Kind == "" || header.Shell == "" || header.Version == "" || header.Digest == "" {
		return shellIntegrationHeader{}, nil, errors.New("generated shell integration header is incomplete")
	}
	return header, content[lineEnd+1:], nil
}

func writeShellResult(stdout io.Writer, result shellIntegrationResult) {
	if result.Action == "generated" {
		_, _ = fmt.Fprintf(stdout, "generated %s %s integration: %s\n", result.Shell, result.Kind, result.Path)
	} else if result.Action == "removed" {
		_, _ = fmt.Fprintf(stdout, "removed %s %s integration\n", result.Shell, result.Kind)
	} else {
		_, _ = fmt.Fprintf(stdout, "no %s %s integration found\n", result.Shell, result.Kind)
	}
	_, _ = fmt.Fprintf(stdout, "source manually: %s\n", result.SourceLine)
	_, _ = fmt.Fprintf(stdout, "remove generated integration: %s\n", result.RemovalLine)
	_, _ = fmt.Fprintf(stdout, "remove that source line manually from shell startup files: %s\n", result.SourceLine)
}

func writeShellUsage(stderr io.Writer, message string) int {
	_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", apperrors.CLIUsage, message)
	_, _ = io.WriteString(stderr, "Usage:\n")
	_, _ = io.WriteString(stderr, "  codex-folio shell generate [--shell bash|zsh|powershell] [--wrapper] [--state-root PATH] [--json]\n")
	_, _ = io.WriteString(stderr, "  codex-folio shell remove [--shell bash|zsh|powershell] [--wrapper] [--state-root PATH] [--json]\n")
	return exitUsage
}
