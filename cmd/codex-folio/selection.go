package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

type selectionOptions struct{ serviceOptions }

func runSelect(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runSelectWithDependencies(args, input, stdout, stderr, resolvePaths, openServiceStoreWithVaultMode, newServiceDiagnosticSink())
}

func runSelectWithDependencies(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return writeSelectionUsageDiagnostic(stderr, "select requires a profile alias", diagnosticSink)
	}
	alias := args[0]
	if err := profile.ValidateAlias(alias); err != nil {
		return writeSelectionUsageDiagnostic(stderr, serviceRemediation(apperrors.Code(err)), diagnosticSink)
	}
	options, err := parseSelectionOptions(args[1:])
	if err != nil {
		return writeSelectionUsageDiagnostic(stderr, "invalid select arguments", diagnosticSink)
	}
	return withSelectionService(input, stderr, resolvePaths, openStore, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		response, err := client.SetSelection(context.Background(), alias)
		if err != nil {
			return err
		}
		if response.Selected == nil {
			return apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
		}
		result := profile.SelectionResult{Profile: selectionProfileIdentity(*response.Selected)}
		if response.Warning != "" {
			result.Warnings = []string{response.Warning}
		}
		if options.json {
			return writeServiceJSON(stdout, result)
		}
		_, _ = fmt.Fprintf(stdout, "Selected Profile: %s (%s)\n", result.Profile.DisplayName, result.Profile.Alias)
		for _, warning := range result.Warnings {
			_, _ = fmt.Fprintf(stderr, "codex-folio: warning: %s\n", warning)
		}
		return nil
	})
}

func selectionProfileIdentity(item httpapi.CommandSelectionProfile) profile.IdentityProfile {
	return profile.IdentityProfile{
		ID: item.ID, Alias: item.Alias, DisplayName: item.DisplayName, Email: item.Email, Workspace: item.Workspace,
		Status: item.Status, IdentityHomeID: item.IdentityHomeID, IdentityHomeOwnership: item.IdentityHomeOwnership,
		AuthenticationMethod: item.AuthenticationMethod, Selected: item.Selected, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func runInteractiveSelectionWithDependencies(input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, launchSelected func(string) int, diagnosticSink diagnostics.Sink) int {
	return runInteractiveSelectionWithOptions(input, stdout, stderr, resolvePaths, openStore, launchSelected, diagnosticSink, selectionOptions{})
}

func runInteractiveSelectionWithOptions(input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, launchSelected func(string) int, diagnosticSink diagnostics.Sink, options selectionOptions) int {
	chosen := ""
	code := withSelectionService(input, stderr, resolvePaths, openStore, diagnosticSink, options, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		response, err := client.GetSelection(context.Background())
		if err != nil {
			return err
		}
		profiles := response.Profiles
		if len(profiles) == 0 {
			return apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
		}
		_, _ = io.WriteString(stdout, "Choose an Identity Profile (q to cancel):\n")
		for index, candidate := range profiles {
			marker := " "
			if candidate.Selected {
				marker = "*"
			}
			_, _ = fmt.Fprintf(stdout, "%s %d. %s (%s)\n", marker, index+1, candidate.DisplayName, candidate.Alias)
		}
		_, _ = io.WriteString(stdout, "> ")
		scanner := bufio.NewScanner(input)
		if !scanner.Scan() {
			return scanner.Err()
		}
		choice := strings.TrimSpace(scanner.Text())
		if strings.EqualFold(choice, "q") || choice == "" {
			return nil
		}
		index, err := strconv.Atoi(choice)
		if err != nil || index < 1 || index > len(profiles) {
			return apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
		}
		result, err := client.SetSelection(context.Background(), profiles[index-1].Alias)
		if err != nil {
			return err
		}
		if result.Selected == nil {
			return apperrors.New(apperrors.ProfileNotSelectable, profile.ErrNotSelectable)
		}
		chosen = result.Selected.Alias
		if result.Warning != "" {
			_, _ = fmt.Fprintf(stderr, "codex-folio: warning: %s\n", result.Warning)
		}
		return nil
	})
	if code != exitSuccess || chosen == "" {
		return code
	}
	if launchSelected == nil {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.LaunchPlanInvalid, errors.New("foreground launcher is unavailable")), diagnosticSink)
	}
	return launchSelected(chosen)
}

func withSelectionService(input io.Reader, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink, options selectionOptions, ownerOptions platform.OwnerOptions, includeLifecycle bool, action func(*httpapi.CommandClient) error) (resultCode int) {
	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	status, err := platform.Discover(paths, ownerOptions)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		connection, err := platform.DiscoverServiceClient(paths, ownerOptions)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if err := action(httpapi.NewCommandClient(connection.Origin, connection.Token, nil)); err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		return exitSuccess
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
	selector, err := profile.NewSelector(stateStore)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	registry, err := profile.NewRegistry(stateStore)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	var lifecycle *profile.Lifecycle
	if includeLifecycle {
		lifecycle, err = newProfileLifecycle(paths, stateStore)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
	}
	configurationPacks, err := newConfigurationPackService(stateStore)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	usageCommands, err := newUsageCommandService(stateStore, nil)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: stateStore, Paths: platform.NewProjectPaths()})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	activities, err := newActivityCommandService(stateStore, projects, nil)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	commandToken, err := newCommandToken()
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	server, err := httpapi.NewServer(httpapi.Options{Diagnostics: diagnosticSink, Selection: selector, Profiles: registry, ProfileLifecycle: lifecycle, ConfigurationPacks: configurationPacks, Usage: usageCommands, Projects: projects, Activities: activities, History: usage.NewHistoryService(stateStore), CommandToken: commandToken})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	listener, err := server.Listen()
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: commandToken}); err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	if err := action(httpapi.NewCommandClient(server.Origin(), commandToken, nil)); err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	return exitSuccess
}

func parseSelectionOptions(args []string) (selectionOptions, error) {
	var options selectionOptions
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--json":
			if options.json {
				return selectionOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--state-root":
			if index+1 >= len(args) {
				return selectionOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options.serviceOptions, args[index]); err != nil {
				return selectionOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options.serviceOptions, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return selectionOptions{}, err
			}
		case arg == "--vault-mode":
			if index+1 >= len(args) {
				return selectionOptions{}, errors.New("--vault-mode requires a value")
			}
			index++
			if err := setServiceVaultMode(&options.serviceOptions, args[index]); err != nil {
				return selectionOptions{}, err
			}
		case strings.HasPrefix(arg, "--vault-mode="):
			if err := setServiceVaultMode(&options.serviceOptions, strings.TrimPrefix(arg, "--vault-mode=")); err != nil {
				return selectionOptions{}, err
			}
		default:
			return selectionOptions{}, errors.New("unexpected select argument")
		}
	}
	return options, nil
}

func writeSelectionUsageDiagnostic(stderr io.Writer, message string, diagnosticSink diagnostics.Sink) int {
	recordServiceDiagnostic(diagnosticSink, apperrors.CLIUsage, diagnostics.SeverityWarning)
	_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", apperrors.CLIUsage, message)
	_, _ = io.WriteString(stderr, "Usage: codex-folio select ALIAS [--state-root PATH] [--vault-mode MODE] [--json]\n")
	return exitUsage
}
