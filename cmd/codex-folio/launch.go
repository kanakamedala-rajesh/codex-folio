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
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
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
	return runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(args, os.Stdin, stdout, stderr, resolvePaths, resolver, openServiceStoreWithVaultMode, newForegroundProcess, newServiceDiagnosticSink(), func() profile.Authenticator {
		return codexadapter.NewAuthenticator()
	}, platform.OwnerOptions{})
}

func runLaunchWithInputAndDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink) int {
	return runLaunchWithInputAndDependenciesAndOwnerOptions(args, input, stdout, stderr, resolvePaths, resolver, openStore, newProcess, diagnosticSink, platform.OwnerOptions{})
}

func runLaunchWithInputAndDependenciesAndOwnerOptions(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, ownerOptions platform.OwnerOptions) (resultCode int) {
	return runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(args, input, stdout, stderr, resolvePaths, resolver, openStore, newProcess, diagnosticSink, nil, ownerOptions)
}

func runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticator(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, newAuthenticator profileAuthenticatorFactory, ownerOptions platform.OwnerOptions) (resultCode int) {
	return runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticatorAndContinuation(args, input, stdout, stderr, resolvePaths, resolver, openStore, newProcess, diagnosticSink, newAuthenticator, editCheckpointFile, newUsageCommandService, ownerOptions)
}

func runLaunchWithInputAndDependenciesAndOwnerOptionsAndAuthenticatorAndContinuation(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink, newAuthenticator profileAuthenticatorFactory, editor func(string, io.Reader, io.Writer, io.Writer) error, newUsage func(*store.Store, launch.ExecutableResolver) (*usageCommandService, error), ownerOptions platform.OwnerOptions) (resultCode int) {
	alias, options, err := parseLaunchArguments(args)
	if err != nil {
		return writeLaunchUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid launch arguments", diagnosticSink)
	}
	if err := profile.ValidateAlias(alias); err != nil {
		return writeLaunchUsageDiagnostic(stderr, apperrors.Code(err), serviceRemediation(apperrors.Code(err)), diagnosticSink)
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
	report, err := launch.Discover(resolver, options.codexBin)
	return withLaunchCommandServiceAndUsage(input, stderr, paths, options, openStore, diagnosticSink, newAuthenticator, newUsage, ownerOptions, func(client *httpapi.CommandClient, childInput io.Reader) int {
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		prepared, prepareErr := client.Launch(context.Background(), httpapi.CommandLaunchRequest{
			Action: "prepare", Alias: alias, Executable: report.Executable, Version: report.Version,
			WorkingDirectory: workingDirectory, Arguments: options.codexArgs,
		})
		if prepareErr != nil {
			return writeServiceErrorWithDiagnostics(stderr, prepareErr, diagnosticSink)
		}
		if prepared.Plan == nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchPlanInvalid, launch.ErrPlanInvalid), diagnosticSink)
		}
		if prepared.Warning != "" {
			_, _ = fmt.Fprintf(stderr, "codex-folio: warning: %s\n", prepared.Warning)
		}
		exitStatus, offer := runForegroundLaunch(client, *prepared.Plan, report, childInput, stdout, stderr, newProcess, diagnosticSink)
		if offer == nil {
			return exitStatus
		}
		continuationInput := bufferedReader(childInput)
		return runSafeContinuationOffer(exitStatus, offer, continuationInput, stdout, stderr, func(target string) int {
			return runHandoffJourney(client, target, httpapi.CommandCheckpointRequest{Action: "capture", Path: workingDirectory}, report, continuationInput, stdout, stderr, newProcess, diagnosticSink, editor)
		})
	})
}

func runForegroundLaunch(client *httpapi.CommandClient, plan launch.Plan, report launch.Discovery, input io.Reader, stdout, stderr io.Writer, newProcess launchProcessFactory, diagnosticSink diagnostics.Sink) (int, *launch.SafeContinuationOffer) {
	abandon := func() {
		_, _ = client.Launch(context.Background(), httpapi.CommandLaunchRequest{Action: "abandoned", LeaseID: plan.LeaseID})
	}
	if newProcess == nil {
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, launch.ErrProcessStartFailed), diagnosticSink), nil
	}
	process, err := newProcess(plan, input, stdout, stderr)
	if err != nil || process == nil {
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, errors.Join(launch.ErrProcessStartFailed, err)), diagnosticSink), nil
	}
	if err := process.Start(); err != nil {
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, errors.Join(launch.ErrProcessStartFailed, err)), diagnosticSink), nil
	}
	processID := process.PID()
	if processID <= 0 {
		_ = process.Kill()
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStartFailed, launch.ErrProcessStartFailed), diagnosticSink), nil
	}
	if _, err := client.Launch(context.Background(), httpapi.CommandLaunchRequest{Action: "started", LeaseID: plan.LeaseID, ProcessID: processID}); err != nil {
		_ = process.Kill()
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink), nil
	}
	stopForwarding := forwardForegroundSignals(process)
	_ = process.Wait()
	stopForwarding()
	exitStatus := process.ExitStatus()
	if !launch.ValidProcessStatus(exitStatus) {
		abandon()
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchProcessStatusInvalid, launch.ErrProcessStatusInvalid), diagnosticSink), nil
	}
	result, err := client.Launch(context.Background(), httpapi.CommandLaunchRequest{Action: "exited", LeaseID: plan.LeaseID, ExitStatus: exitStatus, Executable: report.Executable, Version: report.Version})
	if err != nil {
		_ = writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		return exitStatus, nil
	}
	return exitStatus, result.Offer
}

func bufferedReader(input io.Reader) *bufio.Reader {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader
	}
	if input == nil {
		input = strings.NewReader("")
	}
	return bufio.NewReader(input)
}

func runSafeContinuationOffer(sourceExitStatus int, offer *launch.SafeContinuationOffer, input *bufio.Reader, stdout, stderr io.Writer, continueWith func(string) int) int {
	if offer == nil || len(offer.Alternatives) == 0 || input == nil || continueWith == nil {
		return sourceExitStatus
	}
	io.WriteString(stdout, "Safe Continuation is available after an observed quota condition:\n")
	for index, alternative := range offer.Alternatives {
		best := ""
		if alternative.Recommended {
			best = "; best"
		}
		fmt.Fprintf(stdout, "  %d. %s — capacity %s; %s%s\n", index+1, alternative.Alias, alternative.CapacityState, alternative.Provenance, best)
	}
	io.WriteString(stderr, "Choose an alternative to review a repository-first checkpoint, or press Enter to decline: ")
	line, err := input.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return sourceExitStatus
	}
	choice, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || choice < 1 || choice > len(offer.Alternatives) {
		return sourceExitStatus
	}
	return continueWith(offer.Alternatives[choice-1].Alias)
}

func withLaunchCommandService(input io.Reader, stderr io.Writer, paths platform.Paths, options launchOptions, openStore profileStoreOpener, diagnosticSink diagnostics.Sink, newAuthenticator profileAuthenticatorFactory, ownerOptions platform.OwnerOptions, action func(*httpapi.CommandClient, io.Reader) int) int {
	return withLaunchCommandServiceAndUsage(input, stderr, paths, options, openStore, diagnosticSink, newAuthenticator, newUsageCommandService, ownerOptions, action)
}

func withLaunchCommandServiceAndUsage(input io.Reader, stderr io.Writer, paths platform.Paths, options launchOptions, openStore profileStoreOpener, diagnosticSink diagnostics.Sink, newAuthenticator profileAuthenticatorFactory, newUsage func(*store.Store, launch.ExecutableResolver) (*usageCommandService, error), ownerOptions platform.OwnerOptions, action func(*httpapi.CommandClient, io.Reader) int) int {
	status, err := platform.Discover(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		connection, err := platform.DiscoverServiceClient(paths, ownerOptions)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		return action(httpapi.NewCommandClient(connection.Origin, connection.Token, nil), input)
	}
	owner, err := platform.Acquire(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() { _ = owner.Close() }()
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
	defer func() { _ = stateStore.Close() }()
	attachServiceDiagnosticStore(diagnosticSink, stateStore)
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: stateStore, Paths: platform.NewProjectPaths()})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	configurationPacks, err := newConfigurationPackService(stateStore)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	var authenticator profile.Authenticator
	if newAuthenticator != nil {
		authenticator = newAuthenticator()
		if authenticator == nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileAuthenticationUnavailable, errors.New("profile authenticator is unavailable")), diagnosticSink)
		}
	}
	usageCommands, err := newUsage(stateStore, nil)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	launches, err := newLaunchCommandService(stateStore, configurationPacks, authenticator, projects, usageCommands)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	checkpoints, err := newCheckpointService(stateStore, projects)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	launches.continuations = checkpoints
	commandToken, err := newCommandToken()
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	server, err := httpapi.NewServer(httpapi.Options{Diagnostics: diagnosticSink, Launches: launches, Usage: usageCommands, Checkpoints: checkpoints, CommandToken: commandToken})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	listener, err := server.Listen()
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() { _ = server.Close() }()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: commandToken}); err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	go func() { _ = server.Serve(listener) }()
	return action(httpapi.NewCommandClient(server.Origin(), commandToken, nil), childInput)
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
	command := foregroundCommand(plan.Executable, plan.Arguments...)
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
		blocked[key] = struct{}{}
	}
	environment := make([]string, 0, len(os.Environ())+len(keys))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found {
			for key := range blocked {
				if platform.SameEnvironmentName(name, key) {
					found = false
					break
				}
			}
			if !found {
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
