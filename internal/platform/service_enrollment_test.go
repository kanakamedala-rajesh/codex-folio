package platform

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type enrollmentCall struct {
	name string
	args []string
}

type recordingEnrollmentRunner struct {
	available bool
	calls     []enrollmentCall
	results   map[string]error
	outputs   map[string][]byte
	run       func(name string, args ...string) ([]byte, error)
}

func (runner *recordingEnrollmentRunner) Available(string) bool { return runner.available }
func (runner *recordingEnrollmentRunner) Run(name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, enrollmentCall{name: name, args: append([]string(nil), args...)})
	if runner.run != nil {
		return runner.run(name, args...)
	}
	key := name + " " + strings.Join(args, " ")
	return runner.outputs[key], runner.results[key]
}

type enrollmentExitError struct {
	code int
	err  error
}

func (err enrollmentExitError) Error() string { return err.err.Error() }
func (err enrollmentExitError) Unwrap() error { return err.err }
func (err enrollmentExitError) ExitCode() int { return err.code }

func TestServiceEnrollmentDefinitionsKeepExecutableAndArgumentsIntact(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	executable := filepath.Join(home, `Codex Folio "preview"`, "codex-folio")
	stateRoot := filepath.Join(home, `state & local`)
	serviceArgs := []string{"service", "start", "--enrolled", "--state-root", stateRoot, "--vault-mode", "passphrase"}

	tests := []struct {
		name      string
		platform  Platform
		mechanism string
		contains  []string
	}{
		{name: "linux systemd user", platform: PlatformLinux, mechanism: "systemd-user", contains: []string{"ExecStart=" + systemdQuote(executable), systemdQuote(stateRoot), "WantedBy=default.target"}},
		{name: "macOS LaunchAgent", platform: PlatformDarwin, mechanism: "launch-agent", contains: []string{"<key>ProgramArguments</key>", "Codex Folio &#34;preview&#34;", "state &amp; local", "RunAtLoad", "<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := lifecycleEnrollmentRunner(test.platform)
			manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{Platform: test.platform, HomeDir: home, UID: 501, Executable: executable, Arguments: serviceArgs, Runner: runner})
			if err != nil {
				t.Fatalf("NewServiceEnrollment() error = %v", err)
			}
			if manager.Mechanism() != test.mechanism {
				t.Fatalf("Mechanism() = %q, want %q", manager.Mechanism(), test.mechanism)
			}
			if _, err := manager.Install(); err != nil {
				t.Fatalf("Install() error = %v", err)
			}
			definition, err := os.ReadFile(manager.DefinitionPath())
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			for _, fragment := range test.contains {
				if !strings.Contains(string(definition), fragment) {
					t.Errorf("definition missing %q:\n%s", fragment, definition)
				}
			}
			status, err := manager.Status()
			if err != nil || !status.Available || !status.Installed {
				t.Fatalf("Status() = %#v, %v", status, err)
			}
			repeated, err := manager.Install()
			if err != nil || repeated.Changed {
				t.Fatalf("repeated Install() = %#v, %v; want unchanged", repeated, err)
			}
			if _, err := manager.Uninstall(); err != nil {
				t.Fatalf("Uninstall() error = %v", err)
			}
			if _, err := os.Stat(manager.DefinitionPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("definition remains after uninstall: %v", err)
			}
			repeatedRemoval, err := manager.Uninstall()
			if err != nil || repeatedRemoval.Changed {
				t.Fatalf("repeated Uninstall() = %#v, %v; want unchanged", repeatedRemoval, err)
			}
		})
	}
}

func TestWindowsServiceEnrollmentUsesArgumentSafeTaskCommand(t *testing.T) {
	t.Parallel()
	runner := lifecycleEnrollmentRunner(PlatformWindows)
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformWindows, HomeDir: t.TempDir(), Executable: `C:\Program Files\Codex Folio\codex-folio.exe`,
		Arguments: []string{"service", "start", "--state-root", `C:\Users\A B\Codex "state"`}, Runner: runner,
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	if _, err := manager.Install(); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(runner.calls) < 2 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	var create enrollmentCall
	for _, call := range runner.calls {
		if containsArgument(call.args, "/Create") {
			create = call
			break
		}
	}
	if create.name != "schtasks.exe" || !containsArgument(create.args, "/RL") || !containsArgument(create.args, "LIMITED") {
		t.Fatalf("create call = %#v", create)
	}
	command := argumentAfter(create.args, "/TR")
	if !strings.Contains(command, `"C:\Program Files\Codex Folio\codex-folio.exe"`) || !strings.Contains(command, `"C:\Users\A B\Codex \"state\""`) {
		t.Fatalf("task command does not preserve Windows arguments: %q", command)
	}
}

func TestWindowsInstallOnlyRecreatesChangedTaskDefinition(t *testing.T) {
	t.Parallel()
	executable := `C:\Program Files\Codex Folio\codex-folio.exe`
	registeredArguments := []string{"service", "start", "--state-root", `C:\Users\A B\state`}
	definitionOutput := windowsTaskDefinitionFixture(executable, registeredArguments)
	var definitionErr error
	createCalls := 0
	runner := &recordingEnrollmentRunner{available: true}
	runner.run = func(name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch {
		case command == "schtasks.exe /Query /FO CSV /NH":
			return []byte(`"\CodexFolio User Service","N/A","Running"` + "\r\n"), nil
		case command == "powershell.exe -NoProfile -NonInteractive -Command "+windowsStateScript:
			return []byte("4\r\n"), nil
		case command == "schtasks.exe /Query /TN "+windowsTaskName+" /XML":
			return definitionOutput, definitionErr
		case strings.HasPrefix(command, "schtasks.exe /Create "):
			createCalls++
		}
		return nil, nil
	}
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformWindows, HomeDir: t.TempDir(), Executable: executable,
		Arguments: registeredArguments, Runner: runner,
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	repeated, err := manager.Install()
	if err != nil || repeated.Changed || createCalls != 0 {
		t.Fatalf("matching Install() = %#v, %v; create calls = %d", repeated, err, createCalls)
	}

	changedArguments := []string{"service", "start", "--state-root", `C:\Users\A B\changed`}
	changedManager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformWindows, HomeDir: t.TempDir(), Executable: executable,
		Arguments: changedArguments, Runner: runner,
	})
	if err != nil {
		t.Fatalf("changed NewServiceEnrollment() error = %v", err)
	}
	changed, err := changedManager.Install()
	if err != nil || !changed.Changed || createCalls != 1 {
		t.Fatalf("changed Install() = %#v, %v; create calls = %d", changed, err, createCalls)
	}
	var lastCreate enrollmentCall
	for _, call := range runner.calls {
		if call.name == "schtasks.exe" && containsArgument(call.args, "/Create") {
			lastCreate = call
		}
	}
	if lastCreate.name != "schtasks.exe" || !containsArgument(lastCreate.args, "/Create") || argumentAfter(lastCreate.args, "/TR") != windowsCommandLine(executable, changedArguments) {
		t.Fatalf("changed task create call = %#v", lastCreate)
	}

	nativeFailure := errors.New("task XML query failed")
	definitionErr = nativeFailure
	if _, err := manager.Install(); err == nil || !errors.Is(err, nativeFailure) {
		t.Fatalf("definition query Install() error = %v, want native failure", err)
	}
	definitionErr = nil
	definitionOutput = []byte("not XML")
	if _, err := manager.Install(); err == nil {
		t.Fatal("malformed definition Install() error = nil")
	}
}

func windowsTaskDefinitionFixture(executable string, arguments []string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<Task><Principals><Principal><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>` +
		`<Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers>` +
		`<Actions><Exec><Command>` + executable + `</Command><Arguments>` + windowsArgumentsLine(arguments) +
		`</Arguments></Exec></Actions></Task>`)
}

func TestServiceEnrollmentUnavailablePreservesInspectableStatus(t *testing.T) {
	t.Parallel()
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: runtimePlatform(), HomeDir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "codex-folio"),
		Arguments: []string{"service", "start"}, Runner: &recordingEnrollmentRunner{available: false},
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	status, err := manager.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Available || status.Installed || status.State != EnrollmentUnavailable {
		t.Fatalf("Status() = %#v", status)
	}
	if _, err := manager.Install(); err == nil {
		t.Fatal("Install() error = nil, want unavailable error")
	}
}

func TestServiceEnrollmentReportsFailedNativeCommandWithoutFallback(t *testing.T) {
	t.Parallel()
	nativeFailure := errors.New("native command failed")
	runner := &recordingEnrollmentRunner{available: true, results: map[string]error{
		"systemctl --user daemon-reload": nativeFailure,
	}}
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformLinux, HomeDir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "codex-folio"),
		Arguments: []string{"service", "start"}, Runner: runner,
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	if _, err := manager.Install(); err == nil || !errors.Is(err, nativeFailure) {
		t.Fatalf("Install() error = %v, want native failure", err)
	}
	for _, call := range runner.calls {
		if call.name != "systemctl" {
			t.Fatalf("unexpected fallback command: %#v", call)
		}
	}
}

func TestSystemdEnrollmentStartsAnInstalledInactiveUnit(t *testing.T) {
	t.Parallel()
	runner := lifecycleEnrollmentRunner(PlatformLinux)
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformLinux, HomeDir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "codex-folio"),
		Arguments: []string{"service", "start"}, Runner: runner,
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	if _, err := manager.Install(); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	for _, call := range runner.calls {
		if call.name == "systemctl" && strings.Join(call.args, " ") == "--user start codex-folio.service" {
			return
		}
	}
	t.Fatalf("systemd start call missing: %#v", runner.calls)
}

func TestServiceEnrollmentStatusUsesNativeRegistrationAndActivity(t *testing.T) {
	t.Parallel()
	t.Run("Windows distinguishes absence activity and query failure", func(t *testing.T) {
		t.Parallel()
		query := "schtasks.exe /Query /FO CSV /NH"
		runner := &recordingEnrollmentRunner{available: true, results: map[string]error{}, outputs: map[string][]byte{
			query: []byte(`"\Another Task","N/A","Ready"` + "\r\n"),
		}}
		manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
			Platform: PlatformWindows, HomeDir: t.TempDir(), Executable: `C:\codex-folio.exe`,
			Arguments: []string{"service", "start"}, Runner: runner,
		})
		if err != nil {
			t.Fatalf("NewServiceEnrollment() error = %v", err)
		}
		status, err := manager.Status()
		if err != nil || status.Installed || status.Active || status.State != EnrollmentNotInstalled {
			t.Fatalf("absent Status() = %#v, %v", status, err)
		}
		runner.outputs[query] = []byte(`"\CodexFolio User Service","N/A","Running"` + "\r\n")
		runner.outputs["powershell.exe -NoProfile -NonInteractive -Command "+windowsStateScript] = []byte("4\r\n")
		status, err = manager.Status()
		if err != nil || !status.Installed || !status.Active || status.State != EnrollmentActive {
			t.Fatalf("running Status() = %#v, %v", status, err)
		}
		nativeFailure := errors.New("access denied")
		runner.results[query] = nativeFailure
		if _, err := manager.Status(); err == nil || !errors.Is(err, nativeFailure) {
			t.Fatalf("failed Status() error = %v, want native failure", err)
		}
	})

	t.Run("systemd distinguishes disabled inactive and inspection failure", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		runner := &recordingEnrollmentRunner{available: true, results: map[string]error{}, outputs: map[string][]byte{}}
		manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
			Platform: PlatformLinux, HomeDir: home, Executable: filepath.Join(home, "codex-folio"),
			Arguments: []string{"service", "start"}, Runner: runner,
		})
		if err != nil {
			t.Fatalf("NewServiceEnrollment() error = %v", err)
		}
		if _, err := writeEnrollmentDefinition(manager.DefinitionPath(), manager.systemdDefinition(), 0o600); err != nil {
			t.Fatalf("writeEnrollmentDefinition() error = %v", err)
		}
		enabledQuery := "systemctl --user is-enabled " + linuxServiceName
		activeQuery := "systemctl --user is-active " + linuxServiceName
		runner.outputs[enabledQuery] = []byte("disabled\n")
		runner.results[enabledQuery] = enrollmentExitError{code: 1, err: errors.New("disabled")}
		status, err := manager.Status()
		if err != nil || status.Installed || status.Active || status.State != EnrollmentNotInstalled {
			t.Fatalf("disabled Status() = %#v, %v", status, err)
		}
		delete(runner.results, enabledQuery)
		runner.outputs[enabledQuery] = []byte("enabled\n")
		runner.outputs[activeQuery] = []byte("inactive\n")
		runner.results[activeQuery] = enrollmentExitError{code: 3, err: errors.New("inactive")}
		status, err = manager.Status()
		if err != nil || !status.Installed || status.Active || status.State != EnrollmentInstalled {
			t.Fatalf("inactive Status() = %#v, %v", status, err)
		}
		nativeFailure := errors.New("systemctl transport failed")
		runner.results[enabledQuery] = nativeFailure
		if _, err := manager.Status(); err == nil || !errors.Is(err, nativeFailure) {
			t.Fatalf("failed Status() error = %v, want native failure", err)
		}
	})
}

func TestLaunchAgentInstallRecoversBootstrapFailureAndReloadsChangedDefinition(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	loaded := false
	bootstrapCalls := 0
	bootoutCalls := 0
	runner := &recordingEnrollmentRunner{available: true}
	runner.run = func(name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch {
		case command == "launchctl print gui/501/"+launchAgentLabel:
			if !loaded {
				return nil, enrollmentExitError{code: 113, err: errors.New("service not found")}
			}
			return []byte("state = running\n"), nil
		case strings.HasPrefix(command, "launchctl bootstrap gui/501 "):
			bootstrapCalls++
			if bootstrapCalls == 1 {
				return nil, errors.New("bootstrap failed")
			}
			loaded = true
		case command == "launchctl bootout gui/501/"+launchAgentLabel:
			bootoutCalls++
			loaded = false
		}
		return nil, nil
	}
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformDarwin, HomeDir: home, UID: 501, Executable: filepath.Join(home, "codex-folio"),
		Arguments: []string{"service", "start"}, Runner: runner,
	})
	if err != nil {
		t.Fatalf("NewServiceEnrollment() error = %v", err)
	}
	if _, err := manager.Install(); err == nil {
		t.Fatal("first Install() error = nil, want bootstrap failure")
	}
	recovered, err := manager.Install()
	if err != nil || !recovered.Changed || !recovered.Installed || !recovered.Active {
		t.Fatalf("retry Install() = %#v, %v", recovered, err)
	}
	changedManager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformDarwin, HomeDir: home, UID: 501, Executable: filepath.Join(home, "codex-folio"),
		Arguments: []string{"service", "start", "--state-root", filepath.Join(home, "changed")}, Runner: runner,
	})
	if err != nil {
		t.Fatalf("changed NewServiceEnrollment() error = %v", err)
	}
	changed, err := changedManager.Install()
	if err != nil || !changed.Changed || !changed.Installed || !changed.Active {
		t.Fatalf("changed Install() = %#v, %v", changed, err)
	}
	if bootstrapCalls != 3 || bootoutCalls != 1 {
		t.Fatalf("bootstrap calls = %d, bootout calls = %d; want 3, 1", bootstrapCalls, bootoutCalls)
	}
}

func TestLaunchAgentStopCurrentSessionPreservesLoginDefinition(t *testing.T) {
	home := t.TempDir()
	loaded := true
	runner := &recordingEnrollmentRunner{available: true}
	runner.run = func(name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch command {
		case "launchctl print gui/501/" + launchAgentLabel:
			if loaded {
				return []byte("state = running\n"), nil
			}
			return nil, enrollmentExitError{code: 113, err: errors.New("service not found")}
		case "launchctl bootout gui/501/" + launchAgentLabel:
			loaded = false
		}
		return nil, nil
	}
	manager, err := NewServiceEnrollment(ServiceEnrollmentOptions{
		Platform: PlatformDarwin, HomeDir: home, UID: 501, Executable: filepath.Join(home, "codex-folio"),
		Arguments: []string{"service", "start", "--enrolled"}, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldDefinition := []byte("<key>KeepAlive</key><true/>")
	if err := os.MkdirAll(filepath.Dir(manager.DefinitionPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.DefinitionPath(), oldDefinition, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopCurrentSession(); err != nil {
		t.Fatal(err)
	}
	if loaded {
		t.Fatal("previously loaded LaunchAgent remains active")
	}
	definition, err := os.ReadFile(manager.DefinitionPath())
	if err != nil || !bytes.Equal(definition, oldDefinition) {
		t.Fatalf("login definition = %q, %v", definition, err)
	}
}

func lifecycleEnrollmentRunner(platform Platform) *recordingEnrollmentRunner {
	runner := &recordingEnrollmentRunner{available: true, results: map[string]error{}, outputs: map[string][]byte{}}
	installed := false
	active := false
	runner.run = func(name string, args ...string) ([]byte, error) {
		command := name + " " + strings.Join(args, " ")
		switch platform {
		case PlatformLinux:
			switch command {
			case "systemctl --user is-enabled codex-folio.service":
				if installed {
					return []byte("enabled\n"), nil
				}
				return []byte("disabled\n"), enrollmentExitError{code: 1, err: errors.New("disabled")}
			case "systemctl --user is-active codex-folio.service":
				if active {
					return []byte("active\n"), nil
				}
				return []byte("inactive\n"), enrollmentExitError{code: 3, err: errors.New("inactive")}
			case "systemctl --user enable codex-folio.service":
				installed = true
			case "systemctl --user disable codex-folio.service":
				installed = false
			case "systemctl --user start codex-folio.service":
				active = true
			case "systemctl --user stop codex-folio.service":
				active = false
			}
		case PlatformDarwin:
			switch {
			case command == "launchctl print gui/501/"+launchAgentLabel:
				if !installed {
					return nil, enrollmentExitError{code: 113, err: errors.New("service not found")}
				}
				if active {
					return []byte("state = running\n"), nil
				}
				return []byte("state = not running\n"), nil
			case strings.HasPrefix(command, "launchctl bootstrap gui/501 "):
				installed = true
				active = true
			case command == "launchctl bootout gui/501/"+launchAgentLabel:
				installed = false
				active = false
			case command == "launchctl kickstart -k gui/501/"+launchAgentLabel:
				active = true
			}
		case PlatformWindows:
			switch {
			case command == "schtasks.exe /Query /FO CSV /NH":
				if !installed {
					return []byte(`"\Another Task","N/A","Ready"` + "\r\n"), nil
				}
				state := "Ready"
				if active {
					state = "Running"
				}
				return []byte(`"\CodexFolio User Service","N/A","` + state + `"` + "\r\n"), nil
			case command == "powershell.exe -NoProfile -NonInteractive -Command "+windowsStateScript:
				if active {
					return []byte("4\r\n"), nil
				}
				return []byte("3\r\n"), nil
			case strings.HasPrefix(command, "schtasks.exe /Create "):
				installed = true
			case command == "schtasks.exe /Run /TN "+windowsTaskName:
				active = true
			case command == "schtasks.exe /Delete /TN "+windowsTaskName+" /F":
				installed = false
				active = false
			}
		}
		return nil, nil
	}
	return runner
}

func runtimePlatform() Platform {
	switch runtime.GOOS {
	case "windows":
		return PlatformWindows
	case "darwin":
		return PlatformDarwin
	default:
		return PlatformLinux
	}
}

func containsArgument(arguments []string, wanted string) bool {
	return argumentIndex(arguments, wanted) >= 0
}
func argumentAfter(arguments []string, wanted string) string {
	index := argumentIndex(arguments, wanted)
	if index < 0 || index+1 == len(arguments) {
		return ""
	}
	return arguments[index+1]
}
func argumentIndex(arguments []string, wanted string) int {
	for index, argument := range arguments {
		if argument == wanted {
			return index
		}
	}
	return -1
}
