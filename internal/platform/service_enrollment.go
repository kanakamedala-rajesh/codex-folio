package platform

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	EnrollmentUnavailable  = "unavailable"
	EnrollmentNotInstalled = "not_installed"
	EnrollmentInstalled    = "installed"
	EnrollmentActive       = "active"

	linuxServiceName   = "codex-folio.service"
	launchAgentLabel   = "com.venkatasudha.codex-folio"
	windowsTaskName    = "CodexFolio User Service"
	windowsStateScript = `[int](Get-ScheduledTask -TaskName 'CodexFolio User Service' -ErrorAction Stop).State`
)

// ServiceEnrollmentRunner is the native process seam used by per-user service
// enrollment. Commands are always supplied as argument slices; no shell is
// involved.
type ServiceEnrollmentRunner interface {
	Available(name string) bool
	Run(name string, arguments ...string) ([]byte, error)
}

type nativeServiceEnrollmentRunner struct{}

func (nativeServiceEnrollmentRunner) Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (nativeServiceEnrollmentRunner) Run(name string, arguments ...string) ([]byte, error) {
	command := exec.Command(name, arguments...)
	return command.CombinedOutput()
}

// ServiceEnrollmentOptions identifies one inspectable native per-user
// enrollment. Arguments must begin with the executable's service command.
type ServiceEnrollmentOptions struct {
	Platform   Platform
	HomeDir    string
	UID        int
	Executable string
	Arguments  []string
	Runner     ServiceEnrollmentRunner
}

type ServiceEnrollmentStatus struct {
	Mechanism  string `json:"mechanism"`
	State      string `json:"state"`
	Available  bool   `json:"available"`
	Installed  bool   `json:"installed"`
	Active     bool   `json:"active"`
	Definition string `json:"-"`
}

type ServiceEnrollmentResult struct {
	ServiceEnrollmentStatus
	Changed bool `json:"changed"`
}

// ServiceEnrollment owns only the native startup registration. It never owns
// application state and uninstall never removes the state root.
type ServiceEnrollment struct {
	platform   Platform
	home       string
	uid        int
	executable string
	arguments  []string
	runner     ServiceEnrollmentRunner
}

func NewServiceEnrollment(options ServiceEnrollmentOptions) (*ServiceEnrollment, error) {
	platform := options.Platform
	if platform == "" {
		platform = Platform(runtime.GOOS)
	}
	if platform != PlatformLinux && platform != PlatformDarwin && platform != PlatformWindows {
		return nil, apperrors.New(apperrors.PlatformServiceUnavailable, fmt.Errorf("unsupported native enrollment platform %q", platform))
	}
	home := strings.TrimSpace(options.HomeDir)
	executable := strings.TrimSpace(options.Executable)
	if home == "" || executable == "" || !filepath.IsAbs(home) || !isAbsolutePath(platform, executable) {
		return nil, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("enrollment home and executable must be absolute"))
	}
	if len(options.Arguments) < 2 || options.Arguments[0] != "service" || options.Arguments[1] != "start" {
		return nil, apperrors.New(apperrors.CLIUsage, errors.New("native enrollment must run service start"))
	}
	runner := options.Runner
	if runner == nil {
		runner = nativeServiceEnrollmentRunner{}
	}
	return &ServiceEnrollment{platform: platform, home: filepath.Clean(home), uid: options.UID, executable: executable, arguments: append([]string(nil), options.Arguments...), runner: runner}, nil
}

func (enrollment *ServiceEnrollment) Mechanism() string {
	switch enrollment.platform {
	case PlatformWindows:
		return "task-scheduler"
	case PlatformDarwin:
		return "launch-agent"
	default:
		return "systemd-user"
	}
}

func (enrollment *ServiceEnrollment) commandName() string {
	switch enrollment.platform {
	case PlatformWindows:
		return "schtasks.exe"
	case PlatformDarwin:
		return "launchctl"
	default:
		return "systemctl"
	}
}

func (enrollment *ServiceEnrollment) DefinitionPath() string {
	switch enrollment.platform {
	case PlatformWindows:
		return ""
	case PlatformDarwin:
		return filepath.Join(enrollment.home, "Library", "LaunchAgents", launchAgentLabel+".plist")
	default:
		return filepath.Join(enrollment.home, ".config", "systemd", "user", linuxServiceName)
	}
}

func (enrollment *ServiceEnrollment) Status() (ServiceEnrollmentStatus, error) {
	status := ServiceEnrollmentStatus{Mechanism: enrollment.Mechanism(), State: EnrollmentUnavailable, Definition: enrollment.DefinitionPath()}
	if !enrollment.runner.Available(enrollment.commandName()) {
		return status, nil
	}
	status.Available = true
	switch enrollment.platform {
	case PlatformWindows:
		output, err := enrollment.runner.Run("schtasks.exe", "/Query", "/FO", "CSV", "/NH")
		if err != nil {
			return status, enrollmentError(err)
		}
		installed, err := windowsTaskRegistered(output)
		if err != nil {
			return status, enrollmentError(err)
		}
		status.Installed = installed
		if status.Installed {
			if !enrollment.runner.Available("powershell.exe") {
				return status, enrollmentError(errors.New("native Task Scheduler state inspection is unavailable"))
			}
			stateOutput, stateErr := enrollment.runner.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsStateScript)
			if stateErr != nil {
				return status, enrollmentError(stateErr)
			}
			state, parseErr := strconv.Atoi(strings.TrimSpace(string(stateOutput)))
			if parseErr != nil || state < 0 || state > 4 {
				return status, enrollmentError(errors.New("unexpected Task Scheduler state output"))
			}
			status.Active = state == 4
		}
	case PlatformLinux:
		if _, err := enrollment.runner.Run("systemctl", "--user", "show-environment"); err != nil {
			status.Available = false
			return status, nil
		}
		_, err := os.Stat(status.Definition)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				status.State = EnrollmentNotInstalled
				return status, nil
			}
			return status, enrollmentError(err)
		}
		enabledOutput, enabledErr := enrollment.runner.Run("systemctl", "--user", "is-enabled", linuxServiceName)
		enabledState := strings.TrimSpace(string(enabledOutput))
		if enabledErr != nil && enabledState != "disabled" && enabledState != "masked" && enabledState != "not-found" {
			return status, enrollmentError(enabledErr)
		}
		status.Installed = enabledErr == nil && (enabledState == "enabled" || enabledState == "enabled-runtime")
		if status.Installed {
			activeOutput, activeErr := enrollment.runner.Run("systemctl", "--user", "is-active", linuxServiceName)
			activeState := strings.TrimSpace(string(activeOutput))
			if activeErr != nil && activeState != "inactive" && activeState != "failed" && activeState != "unknown" {
				return status, enrollmentError(activeErr)
			}
			status.Active = activeErr == nil && activeState == "active"
		}
	case PlatformDarwin:
		if _, err := enrollment.runner.Run("launchctl", "manageruid"); err != nil {
			status.Available = false
			return status, nil
		}
		output, err := enrollment.runner.Run("launchctl", "print", enrollment.launchTarget())
		if err != nil {
			if exitCodeIs(err, 113) {
				status.State = EnrollmentNotInstalled
				return status, nil
			}
			return status, enrollmentError(err)
		}
		status.Installed = true
		status.Active = launchAgentRunning(output)
	}
	status.State = EnrollmentNotInstalled
	if status.Installed {
		status.State = EnrollmentInstalled
	}
	if status.Active {
		status.State = EnrollmentActive
	}
	return status, nil
}

func (enrollment *ServiceEnrollment) Install() (ServiceEnrollmentResult, error) {
	before, err := enrollment.Status()
	if err != nil {
		return ServiceEnrollmentResult{}, err
	}
	if !before.Available {
		return ServiceEnrollmentResult{}, apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("native per-user service mechanism is unavailable"))
	}
	changed := !before.Installed
	switch enrollment.platform {
	case PlatformLinux:
		definition := enrollment.systemdDefinition()
		fileChanged, writeErr := writeEnrollmentDefinition(enrollment.DefinitionPath(), definition, 0o600)
		if writeErr != nil {
			return ServiceEnrollmentResult{}, writeErr
		}
		changed = changed || fileChanged
		if err := enrollment.run("systemctl", "--user", "daemon-reload"); err != nil {
			return ServiceEnrollmentResult{}, err
		}
		if err := enrollment.run("systemctl", "--user", "enable", linuxServiceName); err != nil {
			return ServiceEnrollmentResult{}, err
		}
	case PlatformDarwin:
		definition := enrollment.launchAgentDefinition()
		fileChanged, writeErr := writeEnrollmentDefinition(enrollment.DefinitionPath(), definition, 0o600)
		if writeErr != nil {
			return ServiceEnrollmentResult{}, writeErr
		}
		changed = changed || fileChanged
		if before.Installed && fileChanged {
			if err := enrollment.run("launchctl", "bootout", enrollment.launchTarget()); err != nil {
				return ServiceEnrollmentResult{}, err
			}
		}
		if !before.Installed || fileChanged {
			if err := enrollment.run("launchctl", "bootstrap", enrollment.launchDomain(), enrollment.DefinitionPath()); err != nil {
				return ServiceEnrollmentResult{}, err
			}
		}
	case PlatformWindows:
		if before.Installed {
			definition, queryErr := enrollment.runner.Run("schtasks.exe", "/Query", "/TN", windowsTaskName, "/XML")
			if queryErr != nil {
				return ServiceEnrollmentResult{}, enrollmentError(queryErr)
			}
			matches, matchErr := enrollment.windowsTaskDefinitionMatches(definition)
			if matchErr != nil {
				return ServiceEnrollmentResult{}, enrollmentError(matchErr)
			}
			changed = !matches
		}
		if changed {
			arguments := []string{"/Create", "/TN", windowsTaskName, "/SC", "ONLOGON", "/TR", windowsCommandLine(enrollment.executable, enrollment.arguments), "/RL", "LIMITED", "/F"}
			if err := enrollment.run("schtasks.exe", arguments...); err != nil {
				return ServiceEnrollmentResult{}, err
			}
		}
	}
	if err := enrollment.Start(); err != nil {
		return ServiceEnrollmentResult{}, err
	}
	after, err := enrollment.Status()
	return ServiceEnrollmentResult{ServiceEnrollmentStatus: after, Changed: changed}, err
}

func (enrollment *ServiceEnrollment) Start() error {
	status, err := enrollment.Status()
	if err != nil {
		return err
	}
	if !status.Available {
		return apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("native per-user service mechanism is unavailable"))
	}
	if !status.Installed {
		return apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("native per-user service is not installed"))
	}
	if status.Active {
		return nil
	}
	switch enrollment.platform {
	case PlatformLinux:
		return enrollment.run("systemctl", "--user", "start", linuxServiceName)
	case PlatformDarwin:
		return enrollment.run("launchctl", "kickstart", "-k", enrollment.launchTarget())
	default:
		return enrollment.run("schtasks.exe", "/Run", "/TN", windowsTaskName)
	}
}

func (enrollment *ServiceEnrollment) Uninstall() (ServiceEnrollmentResult, error) {
	before, err := enrollment.Status()
	if err != nil {
		return ServiceEnrollmentResult{}, err
	}
	if !before.Available {
		return ServiceEnrollmentResult{}, apperrors.New(apperrors.PlatformServiceUnavailable, errors.New("native per-user service mechanism is unavailable"))
	}
	if !before.Installed {
		return ServiceEnrollmentResult{ServiceEnrollmentStatus: before}, nil
	}
	switch enrollment.platform {
	case PlatformLinux:
		_, _ = enrollment.runner.Run("systemctl", "--user", "stop", linuxServiceName)
		if err := enrollment.run("systemctl", "--user", "disable", linuxServiceName); err != nil {
			return ServiceEnrollmentResult{}, err
		}
		if err := os.Remove(enrollment.DefinitionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ServiceEnrollmentResult{}, enrollmentError(err)
		}
		if err := enrollment.run("systemctl", "--user", "daemon-reload"); err != nil {
			return ServiceEnrollmentResult{}, err
		}
	case PlatformDarwin:
		if before.Installed {
			if err := enrollment.run("launchctl", "bootout", enrollment.launchTarget()); err != nil {
				return ServiceEnrollmentResult{}, err
			}
		}
		if err := os.Remove(enrollment.DefinitionPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ServiceEnrollmentResult{}, enrollmentError(err)
		}
	case PlatformWindows:
		if err := enrollment.run("schtasks.exe", "/Delete", "/TN", windowsTaskName, "/F"); err != nil {
			return ServiceEnrollmentResult{}, err
		}
	}
	after, err := enrollment.Status()
	return ServiceEnrollmentResult{ServiceEnrollmentStatus: after, Changed: true}, err
}

func (enrollment *ServiceEnrollment) run(name string, arguments ...string) error {
	if _, err := enrollment.runner.Run(name, arguments...); err != nil {
		return enrollmentError(err)
	}
	return nil
}

func exitCodeIs(err error, code int) bool {
	var exitError interface{ ExitCode() int }
	return errors.As(err, &exitError) && exitError.ExitCode() == code
}

func windowsTaskRegistered(output []byte) (bool, error) {
	records, err := csv.NewReader(bytes.NewReader(output)).ReadAll()
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if len(record) < 3 {
			return false, errors.New("unexpected schtasks query output")
		}
		if strings.TrimPrefix(strings.TrimSpace(record[0]), `\`) == windowsTaskName {
			return true, nil
		}
	}
	return false, nil
}

type windowsScheduledTaskDefinition struct {
	Principals struct {
		Principal struct {
			RunLevel string `xml:"RunLevel"`
		} `xml:"Principal"`
	} `xml:"Principals"`
	Triggers struct {
		LogonTriggers []struct{} `xml:"LogonTrigger"`
	} `xml:"Triggers"`
	Actions struct {
		Exec struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

func (enrollment *ServiceEnrollment) windowsTaskDefinitionMatches(output []byte) (bool, error) {
	decoded, err := decodeWindowsTaskXML(output)
	if err != nil {
		return false, err
	}
	var definition windowsScheduledTaskDefinition
	if err := xml.Unmarshal(decoded, &definition); err != nil {
		return false, err
	}
	return len(definition.Triggers.LogonTriggers) > 0 &&
		strings.EqualFold(strings.TrimSpace(definition.Principals.Principal.RunLevel), "LeastPrivilege") &&
		definition.Actions.Exec.Command == enrollment.executable &&
		definition.Actions.Exec.Arguments == windowsArgumentsLine(enrollment.arguments), nil
}

func decodeWindowsTaskXML(output []byte) ([]byte, error) {
	if len(output) >= 2 && ((output[0] == 0xff && output[1] == 0xfe) || (output[0] == 0xfe && output[1] == 0xff)) {
		littleEndian := output[0] == 0xff
		output = output[2:]
		if len(output)%2 != 0 {
			return nil, errors.New("invalid UTF-16 task definition")
		}
		units := make([]uint16, len(output)/2)
		for index := range units {
			if littleEndian {
				units[index] = binary.LittleEndian.Uint16(output[index*2:])
			} else {
				units[index] = binary.BigEndian.Uint16(output[index*2:])
			}
		}
		output = []byte(string(utf16.Decode(units)))
	}
	output = bytes.Replace(output, []byte(`encoding="UTF-16"`), []byte(`encoding="UTF-8"`), 1)
	output = bytes.Replace(output, []byte(`encoding="utf-16"`), []byte(`encoding="UTF-8"`), 1)
	return output, nil
}

func launchAgentRunning(output []byte) bool {
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true
		}
	}
	return false
}

func enrollmentError(err error) error {
	return apperrors.New(apperrors.PlatformServiceUnavailable, err)
}

func writeEnrollmentDefinition(path string, contents []byte, mode os.FileMode) (bool, error) {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, contents) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, enrollmentError(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, enrollmentError(err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".codex-folio-enrollment-*")
	if err != nil {
		return false, enrollmentError(err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(contents)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporaryPath, path)
	}
	if err != nil {
		return false, enrollmentError(err)
	}
	return true, nil
}

func (enrollment *ServiceEnrollment) systemdDefinition() []byte {
	arguments := append([]string{enrollment.executable}, enrollment.arguments...)
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		quoted = append(quoted, systemdQuote(argument))
	}
	return []byte("[Unit]\nDescription=CodexFolio user service\n\n[Service]\nType=simple\nExecStart=" + strings.Join(quoted, " ") + "\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n")
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "$", "$$")
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\t", `\t`)
	return `"` + value + `"`
}

func (enrollment *ServiceEnrollment) launchAgentDefinition() []byte {
	var body strings.Builder
	body.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict>\n<key>Label</key><string>")
	_ = xml.EscapeText(&body, []byte(launchAgentLabel))
	body.WriteString("</string>\n<key>ProgramArguments</key><array>\n")
	for _, argument := range append([]string{enrollment.executable}, enrollment.arguments...) {
		body.WriteString("<string>")
		_ = xml.EscapeText(&body, []byte(argument))
		body.WriteString("</string>\n")
	}
	body.WriteString("</array>\n<key>RunAtLoad</key><true/>\n<key>KeepAlive</key><true/>\n</dict></plist>\n")
	return []byte(body.String())
}

func (enrollment *ServiceEnrollment) launchDomain() string {
	return "gui/" + strconv.Itoa(enrollment.uid)
}
func (enrollment *ServiceEnrollment) launchTarget() string {
	return enrollment.launchDomain() + "/" + launchAgentLabel
}

func windowsCommandLine(executable string, arguments []string) string {
	return strings.TrimSpace(quoteWindowsArgument(executable) + " " + windowsArgumentsLine(arguments))
}

func windowsArgumentsLine(arguments []string) string {
	parts := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		parts = append(parts, quoteWindowsArgument(argument))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsArgument(argument string) string {
	if argument != "" && !strings.ContainsAny(argument, " \t\n\v\"") {
		return argument
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			result.WriteString(strings.Repeat(`\`, backslashes*2+1))
			result.WriteRune(character)
			backslashes = 0
			continue
		}
		result.WriteString(strings.Repeat(`\`, backslashes))
		backslashes = 0
		result.WriteRune(character)
	}
	result.WriteString(strings.Repeat(`\`, backslashes*2))
	result.WriteByte('"')
	return result.String()
}
