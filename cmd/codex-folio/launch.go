package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
)

type launchOptions struct {
	serviceOptions
	codexBin  string
	codexArgs []string
}

type foregroundProcess interface {
	Start() error
	Wait() error
	PID() int
	Signal(os.Signal) error
	Kill() error
	ExitStatus() int
}

type launchProcessFactory func(launch.Plan, io.Reader, io.Writer, io.Writer) (foregroundProcess, error)

func runLaunch(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver) int {
	return runLaunchWithInputAndDependenciesAndOwnerOptions(args, os.Stdin, stdout, stderr, resolvePaths, resolver, openServiceStoreWithVaultMode, newForegroundProcess, newServiceDiagnosticSink(), platform.OwnerOptions{})
}

func runLaunchWithInputAndDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink) int {
	return runLaunchWithInputAndDependenciesAndOwnerOptions(args, input, stdout, stderr, resolvePaths, resolver, openStore, newProcess, diagnosticSink, platform.OwnerOptions{})
}

func runLaunchWithInputAndDependenciesAndOwnerOptions(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, ownerOptions platform.OwnerOptions) (resultCode int) {
	childStatusKnown := false
	alias, options, err := parseLaunchArguments(args)
	if err != nil {
		return writeLaunchUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid launch arguments", diagnosticSink)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return writeLaunchUsageDiagnostic(stderr, apperrors.Code(err), serviceRemediation(apperrors.Code(err)), diagnosticSink)
	}
	report, err := launch.Discover(resolver, options.codexBin)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchPlanInvalid, launch.ErrPlanInvalid), diagnosticSink)
	}
	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		if options.stateRoot != nil {
			code := apperrors.Code(err)
			if code == apperrors.PlatformStatePathInvalid || code == apperrors.PlatformStatePathUnsafe {
				return writeLaunchUsageDiagnostic(stderr, code, serviceRemediation(code), diagnosticSink)
			}
		}
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	status, err := platform.Discover(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.PlatformServiceAlreadyRunning, errors.New("launch requires exclusive local state access")), diagnosticSink)
	}
	owner, err := platform.Acquire(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := owner.Close(); closeErr != nil && !childStatusKnown && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()
	childInput := input
	passphrase := ""
	if options.vaultMode == platform.VaultModePassphrase {
		if input != nil {
			bufferedInput := bufio.NewReader(input)
			passphrase, err = readServiceVaultPassphrase(bufferedInput)
			childInput = bufferedInput
		} else {
			passphrase, err = readServiceVaultPassphrase(input)
		}
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
	}
	stateStore, err := openStore(paths, options.vaultMode, passphrase)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := stateStore.Close(); closeErr != nil && !childStatusKnown && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()
	attachServiceDiagnosticStore(diagnosticSink, stateStore)
	workflow, err := launch.NewWorkflow(launch.WorkflowOptions{Repository: stateStore})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if err := workflow.Reconcile(context.Background(), foregroundProcessInspector{}); err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	plan, err := workflow.Prepare(context.Background(), launch.PrepareRequest{
		Alias:            alias,
		Executable:       report.Executable,
		WorkingDirectory: workingDirectory,
		Arguments:        options.codexArgs,
	})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if newProcess == nil {
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, launch.ErrProcessStartFailed), diagnosticSink)
	}
	process, err := newProcess(plan, childInput, stdout, stderr)
	if err != nil || process == nil {
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, errors.Join(launch.ErrProcessStartFailed, err)), diagnosticSink)
	}
	if err := process.Start(); err != nil {
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, errors.Join(launch.ErrProcessStartFailed, err)), diagnosticSink)
	}
	processID := process.PID()
	if processID <= 0 {
		_ = process.Kill()
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, launch.ErrProcessStartFailed), diagnosticSink)
	}
	if err := workflow.MarkStarted(context.Background(), plan.LeaseID, processID); err != nil {
		_ = process.Kill()
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	stopForwarding := forwardForegroundSignals(process)
	_ = process.Wait()
	stopForwarding()
	exitStatus := process.ExitStatus()
	if !launch.ValidProcessStatus(exitStatus) {
		_ = workflow.MarkAbandoned(context.Background(), plan.LeaseID)
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStatusInvalid, launch.ErrProcessStatusInvalid), diagnosticSink)
	}
	resultCode = exitStatus
	childStatusKnown = true
	if err := workflow.MarkExited(context.Background(), plan.LeaseID, exitStatus); err != nil {
		_ = writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	return resultCode
}

func parseLaunchArguments(args []string) (string, launchOptions, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return "", launchOptions{}, errors.New("launch requires a profile alias")
	}
	alias := args[0]
	separator := -1
	for index := 1; index < len(args); index++ {
		if args[index] == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return "", launchOptions{}, errors.New("launch arguments require -- separator")
	}
	var options launchOptions
	for index := 1; index < separator; index++ {
		arg := args[index]
		switch {
		case arg == "--codex-bin":
			if index+1 >= separator || strings.HasPrefix(args[index+1], "--") || options.codexBin != "" {
				return "", launchOptions{}, errors.New("--codex-bin requires one value")
			}
			index++
			options.codexBin = strings.TrimSpace(args[index])
			if options.codexBin == "" {
				return "", launchOptions{}, errors.New("--codex-bin requires one value")
			}
		case strings.HasPrefix(arg, "--codex-bin="):
			if options.codexBin != "" {
				return "", launchOptions{}, errors.New("--codex-bin may be supplied only once")
			}
			options.codexBin = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-bin="))
			if options.codexBin == "" {
				return "", launchOptions{}, errors.New("--codex-bin requires one value")
			}
		case arg == "--state-root":
			if index+1 >= separator || strings.HasPrefix(args[index+1], "--") {
				return "", launchOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options.serviceOptions, args[index]); err != nil {
				return "", launchOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options.serviceOptions, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return "", launchOptions{}, err
			}
		case arg == "--vault-mode":
			if index+1 >= separator || strings.HasPrefix(args[index+1], "--") {
				return "", launchOptions{}, errors.New("--vault-mode requires a value")
			}
			index++
			if err := setServiceVaultMode(&options.serviceOptions, args[index]); err != nil {
				return "", launchOptions{}, err
			}
		case strings.HasPrefix(arg, "--vault-mode="):
			if err := setServiceVaultMode(&options.serviceOptions, strings.TrimPrefix(arg, "--vault-mode=")); err != nil {
				return "", launchOptions{}, err
			}
		default:
			return "", launchOptions{}, errors.New("unexpected launch option")
		}
	}
	options.codexArgs = append([]string(nil), args[separator+1:]...)
	return alias, options, nil
}

func writeLaunchUsageDiagnostic(stderr io.Writer, code, message string, diagnosticSink diagnostics.Sink) int {
	code = diagnostics.CodeFor(nil, code)
	if strings.TrimSpace(message) == "" {
		message = serviceRemediation(code)
	}
	recordServiceDiagnostic(diagnosticSink, code, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, message)
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  codex-folio launch ALIAS [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] -- [CODEX ARGS ...]")
	return exitUsage
}

type nativeForegroundProcess struct{ command *exec.Cmd }

func newForegroundProcess(plan launch.Plan, stdin io.Reader, stdout, stderr io.Writer) (foregroundProcess, error) {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	command := exec.Command(plan.Executable, plan.Arguments...)
	command.Dir = plan.WorkingDirectory
	command.Env = environmentWithDelta(plan.Environment)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return &nativeForegroundProcess{command: command}, nil
}

func (process *nativeForegroundProcess) Start() error { return process.command.Start() }

func (process *nativeForegroundProcess) Wait() error { return process.command.Wait() }

func (process *nativeForegroundProcess) PID() int {
	if process == nil || process.command == nil || process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

func (process *nativeForegroundProcess) Signal(signal os.Signal) error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return errors.New("foreground process is not running")
	}
	return process.command.Process.Signal(signal)
}

func (process *nativeForegroundProcess) Kill() error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return nil
	}
	return process.command.Process.Kill()
}

func (process *nativeForegroundProcess) ExitStatus() int {
	if process == nil || process.command == nil {
		return -1
	}
	return foregroundExitStatus(process.command.ProcessState)
}

func environmentWithDelta(delta map[string]string) []string {
	if len(delta) == 0 {
		return os.Environ()
	}
	keys := make([]string, 0, len(delta))
	for key := range delta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[strings.ToLower(key)] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+len(keys))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, skip := blocked[strings.ToLower(name)]; skip {
				continue
			}
		}
		environment = append(environment, entry)
	}
	for _, key := range keys {
		environment = append(environment, key+"="+delta[key])
	}
	return environment
}
