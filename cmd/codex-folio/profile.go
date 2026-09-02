package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	profilefeature "venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

type profileOptions struct {
	serviceOptions
	codexBin       string
	authMethod     profilefeature.AuthMethod
	displayName    string
	nonInteractive bool
	yes            bool
}

type profileStoreOpener func(platform.Paths, platform.VaultMode, string) (*store.Store, error)
type profileHomeFactory func(string) (profilefeature.ManagedHomeProvisioner, error)
type profileAuthenticatorFactory func() profilefeature.Authenticator

func runProfile(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver) int {
	return runProfileWithInputAndDependencies(args, os.Stdin, stdout, stderr, resolvePaths, resolver, openServiceStoreWithVaultMode, func(root string) (profilefeature.ManagedHomeProvisioner, error) {
		return platform.NewManagedHomeProvisioner(root)
	}, func() profilefeature.Authenticator {
		return codexadapter.NewAuthenticator()
	}, newServiceDiagnosticSink())
}

func runProfileWithInputAndDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newHome profileHomeFactory, newAuthenticator profileAuthenticatorFactory, diagnosticSink diagnostics.Sink) (resultCode int) {
	return runProfileWithInputAndDependenciesAndOwnerOptions(args, input, stdout, stderr, resolvePaths, resolver, openStore, newHome, newAuthenticator, diagnosticSink, platform.OwnerOptions{})
}

func runProfileWithInputAndDependenciesAndOwnerOptions(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, resolver launch.ExecutableResolver, openStore profileStoreOpener, newHome profileHomeFactory, newAuthenticator profileAuthenticatorFactory, diagnosticSink diagnostics.Sink, ownerOptions platform.OwnerOptions) (resultCode int) {
	if len(args) < 1 || args[0] != "add" {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "profile add is required", diagnosticSink)
	}
	if len(args) < 2 || strings.HasPrefix(args[1], "--") {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "profile add requires an alias", diagnosticSink)
	}
	alias := args[1]
	options, err := parseProfileOptions(args[2:])
	if err != nil {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile arguments", diagnosticSink)
	}
	if err := profilefeature.ValidateAlias(alias); err != nil {
		return writeProfileUsageDiagnostic(stderr, apperrors.Code(err), serviceRemediation(apperrors.Code(err)), diagnosticSink)
	}

	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	status, err := platform.Discover(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.PlatformServiceAlreadyRunning, errors.New("profile setup requires exclusive local state access")), diagnosticSink)
	}
	owner, err := platform.Acquire(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := owner.Close(); closeErr != nil && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()

	passphrase := ""
	if options.vaultMode == platform.VaultModePassphrase {
		passphrase, err = readServiceVaultPassphrase(input)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
	}
	stateStore, err := openStore(paths, options.vaultMode, passphrase)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := stateStore.Close(); closeErr != nil && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()
	attachServiceDiagnosticStore(diagnosticSink, stateStore)

	homeProvisioner, err := newHome(paths.ManagedHomes)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if newAuthenticator == nil {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authenticator is unavailable")), diagnosticSink)
	}
	workflow, err := profilefeature.NewWorkflow(profilefeature.WorkflowOptions{
		Repository:      stateStore,
		Discoverer:      profileDiscoverer{resolver: resolver},
		HomeProvisioner: homeProvisioner,
		Authenticator:   newAuthenticator(),
	})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	request := profilefeature.SetupRequest{
		Alias:          alias,
		DisplayName:    options.displayName,
		CodexOverride:  options.codexBin,
		AuthMethod:     options.authMethod,
		NonInteractive: options.nonInteractive,
		Stdin:          input,
		Stdout:         stderr,
		Stderr:         stderr,
	}
	result, err := workflow.Add(context.Background(), request)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if options.json {
		if err := writeServiceJSON(stdout, result); err != nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIInternal, err), diagnosticSink)
		}
		return exitSuccess
	}
	writeProfileResult(stdout, result)
	return exitSuccess
}

func parseProfileOptions(args []string) (profileOptions, error) {
	var options profileOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			if options.json {
				return profileOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--non-interactive":
			if options.nonInteractive {
				return profileOptions{}, errors.New("--non-interactive may be supplied only once")
			}
			options.nonInteractive = true
		case arg == "--yes":
			if options.yes {
				return profileOptions{}, errors.New("--yes may be supplied only once")
			}
			options.yes = true
		case arg == "--browser":
			if options.authMethod != "" {
				return profileOptions{}, errors.New("only one authentication method may be selected")
			}
			options.authMethod = profilefeature.AuthMethodBrowser
		case arg == "--device-code":
			if options.authMethod != "" {
				return profileOptions{}, errors.New("only one authentication method may be selected")
			}
			options.authMethod = profilefeature.AuthMethodDeviceCode
		case arg == "--display-name":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.displayName != "" {
				return profileOptions{}, errors.New("--display-name requires one value")
			}
			index++
			options.displayName = args[index]
		case strings.HasPrefix(arg, "--display-name="):
			if options.displayName != "" {
				return profileOptions{}, errors.New("--display-name may be supplied only once")
			}
			options.displayName = strings.TrimPrefix(arg, "--display-name=")
			if options.displayName == "" {
				return profileOptions{}, errors.New("--display-name requires one value")
			}
		case arg == "--codex-bin":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.codexBin != "" {
				return profileOptions{}, errors.New("--codex-bin requires one value")
			}
			index++
			options.codexBin = strings.TrimSpace(args[index])
			if options.codexBin == "" {
				return profileOptions{}, errors.New("--codex-bin requires one value")
			}
		case strings.HasPrefix(arg, "--codex-bin="):
			if options.codexBin != "" {
				return profileOptions{}, errors.New("--codex-bin may be supplied only once")
			}
			options.codexBin = strings.TrimSpace(strings.TrimPrefix(arg, "--codex-bin="))
			if options.codexBin == "" {
				return profileOptions{}, errors.New("--codex-bin requires one value")
			}
		case arg == "--state-root":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return profileOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options.serviceOptions, args[index]); err != nil {
				return profileOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options.serviceOptions, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return profileOptions{}, err
			}
		case arg == "--vault-mode":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return profileOptions{}, errors.New("--vault-mode requires a value")
			}
			index++
			if err := setServiceVaultMode(&options.serviceOptions, args[index]); err != nil {
				return profileOptions{}, err
			}
		case strings.HasPrefix(arg, "--vault-mode="):
			if err := setServiceVaultMode(&options.serviceOptions, strings.TrimPrefix(arg, "--vault-mode=")); err != nil {
				return profileOptions{}, err
			}
		default:
			return profileOptions{}, errors.New("unexpected profile argument")
		}
	}
	return options, nil
}

type profileDiscoverer struct {
	resolver launch.ExecutableResolver
}

func (discoverer profileDiscoverer) Discover(override string) (profilefeature.Discovery, error) {
	report, err := launch.Discover(discoverer.resolver, override)
	if err != nil {
		return profilefeature.Discovery{}, err
	}
	return profilefeature.Discovery{Executable: report.Executable, Version: report.Version}, nil
}

func writeProfileResult(stdout io.Writer, result profilefeature.SetupResult) {
	_, _ = fmt.Fprintf(stdout, "Profile: %s (%s)\n", result.Profile.Alias, result.Profile.Status)
	_, _ = fmt.Fprintf(stdout, "Identity Home: %s\n", result.Profile.IdentityHomeOwnership)
	_, _ = fmt.Fprintf(stdout, "Selected: %t\n", result.Profile.Selected)
	_, _ = fmt.Fprintf(stdout, "Codex version: %s\n", result.Discovery.Version)
	if result.AuthenticationMethod != "" {
		_, _ = fmt.Fprintf(stdout, "Authentication: %s\n", result.AuthenticationMethod)
	}
}

func writeProfileUsageDiagnostic(stderr io.Writer, code, message string, diagnosticSink diagnostics.Sink) int {
	code = diagnostics.CodeFor(nil, code)
	if strings.TrimSpace(message) == "" {
		message = serviceRemediation(code)
	}
	recordServiceDiagnostic(diagnosticSink, code, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, message)
	writeProfileUsage(stderr)
	return exitUsage
}

func writeProfileUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  codex-folio profile add ALIAS [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--yes] [--json]")
}
