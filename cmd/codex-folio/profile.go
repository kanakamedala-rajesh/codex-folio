package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
	profilefeature "venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

type profileOptions struct {
	serviceOptions
	codexBin       string
	identityHome   string
	authMethod     profilefeature.AuthMethod
	displayName    string
	newAlias       string
	email          *string
	workspace      *string
	nonInteractive bool
	yes            bool
	replacement    string
	confirmation   string
	configPackID   string
	configVersion  string
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
	if len(args) < 1 || (args[0] != "add" && args[0] != "reauthenticate" && args[0] != "list" && args[0] != "edit" && args[0] != "remove" && args[0] != "restore" && args[0] != "purge") {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "a profile command is required", diagnosticSink)
	}
	command := args[0]
	if command == "list" {
		options, err := parseProfileOptions(args[1:])
		if err != nil || hasProfileMutationOptions(options) {
			return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile list arguments", diagnosticSink)
		}
		return runProfileRegistryCommand(command, "", options, input, stdout, stderr, resolvePaths, openStore, diagnosticSink)
	}
	if len(args) < 2 || strings.HasPrefix(args[1], "--") {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "profile command requires an alias", diagnosticSink)
	}
	alias := args[1]
	options, err := parseProfileOptions(args[2:])
	if err != nil {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile arguments", diagnosticSink)
	}
	if command == "reauthenticate" && (options.displayName != "" || options.identityHome != "" || options.newAlias != "" || options.email != nil || options.workspace != nil || options.yes || options.replacement != "" || options.confirmation != "" || options.configPackID != "") {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid reauthentication arguments", diagnosticSink)
	}
	if command == "add" && (options.newAlias != "" || options.email != nil || options.workspace != nil || options.replacement != "" || options.confirmation != "") {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile add arguments", diagnosticSink)
	}
	if command == "add" && options.identityHome != "" && options.configPackID != "" {
		return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "configuration packs require a managed Identity Home", diagnosticSink)
	}
	if err := profilefeature.ValidateAlias(alias); err != nil {
		return writeProfileUsageDiagnostic(stderr, apperrors.Code(err), serviceRemediation(apperrors.Code(err)), diagnosticSink)
	}
	if command == "edit" {
		if options.codexBin != "" || options.identityHome != "" || options.authMethod != "" || options.nonInteractive || options.yes || options.replacement != "" || options.confirmation != "" || options.configPackID != "" || (options.newAlias == "" && options.displayName == "" && options.email == nil && options.workspace == nil) {
			return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile edit arguments", diagnosticSink)
		}
		return runProfileRegistryCommand(command, alias, options, input, stdout, stderr, resolvePaths, openStore, diagnosticSink)
	}
	if command == "remove" || command == "restore" || command == "purge" {
		if options.codexBin != "" || options.identityHome != "" || options.authMethod != "" || options.displayName != "" || options.newAlias != "" || options.email != nil || options.workspace != nil || options.yes || options.configPackID != "" || (command != "remove" && options.replacement != "") || (command == "restore" && options.confirmation != "") {
			return writeProfileUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid profile lifecycle arguments", diagnosticSink)
		}
		return runProfileLifecycleCommand(command, alias, options, input, stdout, stderr, resolvePaths, openStore, diagnosticSink, ownerOptions)
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
		connection, err := platform.DiscoverServiceClient(paths, ownerOptions)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		client, err := newServiceCommandClient(connection)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		result, err := client.AuthenticateProfile(context.Background(), httpapi.CommandProfileAuthenticationRequest{
			Action: command, Alias: alias, DisplayName: options.displayName, CodexOverride: options.codexBin,
			ReferencedHomePath: options.identityHome, AuthMethod: options.authMethod, NonInteractive: options.nonInteractive,
			ConfigurationPackID: options.configPackID, ConfigurationPackVersion: options.configVersion,
		}, stderr)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if command == "reauthenticate" {
			if result.Reauthentication == nil {
				return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileAuthenticationFailed, profilefeature.ErrAuthenticationFailed), diagnosticSink)
			}
			if options.json {
				if err := writeServiceJSON(stdout, result.Reauthentication); err != nil {
					return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIInternal, err), diagnosticSink)
				}
			} else {
				writeReauthenticationResult(stdout, *result.Reauthentication)
			}
			return exitSuccess
		}
		if result.Setup == nil {
			return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileSetupInvalid, profilefeature.ErrProfileStateInvalid), diagnosticSink)
		}
		if options.json {
			if err := writeServiceJSON(stdout, result.Setup); err != nil {
				return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIInternal, err), diagnosticSink)
			}
		} else {
			writeProfileResult(stdout, *result.Setup)
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
	var configurationPacks *configpack.Service
	if options.configPackID != "" {
		configurationPacks, err = newConfigurationPackService(stateStore)
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if err := configurationPacks.ValidateAssignment(context.Background(), options.configPackID, options.configVersion); err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
	}

	if newAuthenticator == nil {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authenticator is unavailable")), diagnosticSink)
	}
	authenticator := newAuthenticator()
	if authenticator == nil {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.ProfileSetupInvalid, errors.New("profile authenticator is unavailable")), diagnosticSink)
	}
	if command == "reauthenticate" {
		result, err := profilefeature.Reauthenticate(context.Background(), stateStore, profileDiscoverer{resolver: resolver}, authenticator, profilefeature.ReauthenticationRequest{
			Alias:          alias,
			CodexOverride:  options.codexBin,
			AuthMethod:     options.authMethod,
			NonInteractive: options.nonInteractive,
			Stdin:          input,
			Stdout:         stderr,
			Stderr:         stderr,
		})
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		if options.json {
			if err := writeServiceJSON(stdout, result); err != nil {
				return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIInternal, err), diagnosticSink)
			}
			return exitSuccess
		}
		writeReauthenticationResult(stdout, result)
		return exitSuccess
	}

	homeProvisioner, err := newHome(paths.ManagedHomes)
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	workflow, err := profilefeature.NewWorkflow(profilefeature.WorkflowOptions{
		Repository:             stateStore,
		Discoverer:             profileDiscoverer{resolver: resolver},
		HomeProvisioner:        homeProvisioner,
		ReferencedHomeResolver: platform.NewReferencedHomeResolver(paths.ManagedHomes, paths.ProfileQuarantine),
		Authenticator:          authenticator,
	})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	request := profilefeature.SetupRequest{
		Alias:                    alias,
		DisplayName:              options.displayName,
		CodexOverride:            options.codexBin,
		ReferencedHomePath:       options.identityHome,
		AuthMethod:               options.authMethod,
		NonInteractive:           options.nonInteractive,
		Stdin:                    input,
		Stdout:                   stderr,
		Stderr:                   stderr,
		ConfigurationPackID:      options.configPackID,
		ConfigurationPackVersion: options.configVersion,
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
		case arg == "--configuration-pack":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.configPackID != "" {
				return profileOptions{}, errors.New("--configuration-pack requires PACK_ID@VERSION")
			}
			index++
			if err := setProfileConfigurationPack(&options, args[index]); err != nil {
				return profileOptions{}, err
			}
		case strings.HasPrefix(arg, "--configuration-pack="):
			if options.configPackID != "" {
				return profileOptions{}, errors.New("--configuration-pack may be supplied only once")
			}
			if err := setProfileConfigurationPack(&options, strings.TrimPrefix(arg, "--configuration-pack=")); err != nil {
				return profileOptions{}, err
			}
		case arg == "--replacement":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.replacement != "" {
				return profileOptions{}, errors.New("--replacement requires one value")
			}
			index++
			options.replacement = args[index]
		case strings.HasPrefix(arg, "--replacement="):
			if options.replacement != "" {
				return profileOptions{}, errors.New("--replacement may be supplied only once")
			}
			options.replacement = strings.TrimPrefix(arg, "--replacement=")
			if options.replacement == "" {
				return profileOptions{}, errors.New("--replacement requires one value")
			}
		case arg == "--confirm":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.confirmation != "" {
				return profileOptions{}, errors.New("--confirm requires one value")
			}
			index++
			options.confirmation = args[index]
		case strings.HasPrefix(arg, "--confirm="):
			if options.confirmation != "" {
				return profileOptions{}, errors.New("--confirm may be supplied only once")
			}
			options.confirmation = strings.TrimPrefix(arg, "--confirm=")
			if options.confirmation == "" {
				return profileOptions{}, errors.New("--confirm requires one value")
			}
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
		case arg == "--alias":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.newAlias != "" {
				return profileOptions{}, errors.New("--alias requires one value")
			}
			index++
			options.newAlias = args[index]
		case strings.HasPrefix(arg, "--alias="):
			if options.newAlias != "" {
				return profileOptions{}, errors.New("--alias may be supplied only once")
			}
			options.newAlias = strings.TrimPrefix(arg, "--alias=")
			if options.newAlias == "" {
				return profileOptions{}, errors.New("--alias requires one value")
			}
		case arg == "--email" || arg == "--workspace":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return profileOptions{}, errors.New(arg + " requires one value")
			}
			index++
			if err := setProfileDisplayMetadata(&options, arg, args[index]); err != nil {
				return profileOptions{}, err
			}
		case strings.HasPrefix(arg, "--email="):
			if err := setProfileDisplayMetadata(&options, "--email", strings.TrimPrefix(arg, "--email=")); err != nil {
				return profileOptions{}, err
			}
		case strings.HasPrefix(arg, "--workspace="):
			if err := setProfileDisplayMetadata(&options, "--workspace", strings.TrimPrefix(arg, "--workspace=")); err != nil {
				return profileOptions{}, err
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
		case arg == "--identity-home":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") || options.identityHome != "" {
				return profileOptions{}, errors.New("--identity-home requires one value")
			}
			index++
			options.identityHome = strings.TrimSpace(args[index])
			if options.identityHome == "" {
				return profileOptions{}, errors.New("--identity-home requires one value")
			}
		case strings.HasPrefix(arg, "--identity-home="):
			if options.identityHome != "" {
				return profileOptions{}, errors.New("--identity-home may be supplied only once")
			}
			options.identityHome = strings.TrimSpace(strings.TrimPrefix(arg, "--identity-home="))
			if options.identityHome == "" {
				return profileOptions{}, errors.New("--identity-home requires one value")
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

func setProfileConfigurationPack(options *profileOptions, value string) error {
	id, version, ok := strings.Cut(strings.TrimSpace(value), "@")
	if !ok || id == "" || version == "" || strings.Contains(version, "@") {
		return errors.New("--configuration-pack requires PACK_ID@VERSION")
	}
	options.configPackID, options.configVersion = id, version
	return nil
}

func setProfileDisplayMetadata(options *profileOptions, flag, value string) error {
	target := &options.email
	if flag == "--workspace" {
		target = &options.workspace
	}
	if *target != nil {
		return errors.New(flag + " may be supplied only once")
	}
	value = strings.TrimSpace(value)
	*target = &value
	return nil
}

func hasProfileMutationOptions(options profileOptions) bool {
	return options.codexBin != "" || options.identityHome != "" || options.authMethod != "" || options.displayName != "" || options.newAlias != "" || options.email != nil || options.workspace != nil || options.nonInteractive || options.yes || options.replacement != "" || options.confirmation != "" || options.configPackID != ""
}

func runProfileLifecycleCommand(command, alias string, options profileOptions, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink, ownerOptions platform.OwnerOptions) (resultCode int) {
	if input == nil {
		input = strings.NewReader("")
	}
	bufferedInput := bufio.NewReader(input)
	return withSelectionService(bufferedInput, stderr, resolvePaths, openStore, diagnosticSink, selectionOptions{serviceOptions: options.serviceOptions}, ownerOptions, true, func(client *httpapi.CommandClient) error {
		preview, err := client.PreviewProfileLifecycle(context.Background(), command, alias)
		if err != nil {
			return err
		}
		if command != "restore" {
			if err := confirmProfileLifecycle(bufferedInput, stderr, preview.Profile.Alias, command, preview.Profile.IdentityHomeOwnership, options.confirmation, options.nonInteractive); err != nil {
				return err
			}
		}
		confirmation := ""
		if command != "restore" {
			confirmation = preview.Profile.Alias
		}
		result, err := client.ApplyProfileLifecycle(context.Background(), command, alias, options.replacement, confirmation)
		if err != nil {
			return err
		}
		if options.json {
			return writeServiceJSON(stdout, result)
		}
		_, _ = fmt.Fprintf(stdout, "Profile: %s\nAction: %s\nRemote OpenAI identity affected: false\n", result.Profile.Alias, result.Action)
		if !result.PurgeAfter.IsZero() && result.Action == profilefeature.RemovalQuarantined {
			_, _ = fmt.Fprintf(stdout, "Recoverable until: %s\n", result.PurgeAfter.Format(time.RFC3339))
		}
		return nil
	})
}

func confirmProfileLifecycle(input *bufio.Reader, stderr io.Writer, alias, action string, ownership profilefeature.HomeOwnership, provided string, nonInteractive bool) error {
	if provided != "" {
		if provided == alias {
			return nil
		}
		return apperrors.New(apperrors.ProfileConfirmationInvalid, errors.New("profile confirmation did not match the exact CLI Alias"))
	}
	if nonInteractive {
		return apperrors.New(apperrors.ProfileConfirmationInvalid, errors.New("--non-interactive requires --confirm with the exact CLI Alias"))
	}
	_, _ = fmt.Fprintf(stderr, "%s local profile %s (%s Identity Home). Remote OpenAI identity is unaffected. Type %s to confirm: ", strings.ToUpper(action[:1])+action[1:], alias, ownership, alias)
	confirmation, err := input.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return apperrors.New(apperrors.ProfileConfirmationInvalid, err)
	}
	confirmation = strings.TrimSuffix(strings.TrimSuffix(confirmation, "\n"), "\r")
	if confirmation != alias {
		return apperrors.New(apperrors.ProfileConfirmationInvalid, errors.New("profile confirmation did not match the exact CLI Alias"))
	}
	return nil
}

func runProfileRegistryCommand(command, alias string, options profileOptions, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, openStore profileStoreOpener, diagnosticSink diagnostics.Sink) int {
	return withSelectionService(input, stderr, resolvePaths, openStore, diagnosticSink, selectionOptions{serviceOptions: options.serviceOptions}, platform.OwnerOptions{}, false, func(client *httpapi.CommandClient) error {
		if command == "list" {
			response, err := client.ListProfiles(context.Background())
			if err != nil {
				return err
			}
			result := profilefeature.InventoryResult{Profiles: make([]profilefeature.IdentityProfile, 0, len(response.Profiles))}
			for _, item := range response.Profiles {
				result.Profiles = append(result.Profiles, commandProfileIdentity(item))
			}
			if options.json {
				return writeServiceJSON(stdout, result)
			}
			for _, item := range result.Profiles {
				_, _ = fmt.Fprintf(stdout, "%s (%s) %s %s auth=%s selected=%t", item.DisplayName, item.Alias, item.Status, item.IdentityHomeOwnership, item.AuthenticationMethod, item.Selected)
				if item.Email != "" {
					_, _ = fmt.Fprintf(stdout, " email=%s", item.Email)
				}
				if item.Workspace != "" {
					_, _ = fmt.Fprintf(stdout, " workspace=%s", item.Workspace)
				}
				_, _ = fmt.Fprintln(stdout)
			}
			return nil
		}
		edits := profilefeature.ProfileEdits{Email: options.email, Workspace: options.workspace}
		if options.newAlias != "" {
			edits.Alias = &options.newAlias
		}
		if options.displayName != "" {
			edits.DisplayName = &options.displayName
		}
		response, err := client.EditProfile(context.Background(), alias, edits)
		if err != nil {
			return err
		}
		if response.Updated == nil {
			return apperrors.New(apperrors.ProfileSetupInvalid, profilefeature.ErrProfileStateInvalid)
		}
		updated := commandProfileIdentity(*response.Updated)
		if options.json {
			return writeServiceJSON(stdout, updated)
		}
		_, _ = fmt.Fprintf(stdout, "Profile: %s (%s)\n", updated.DisplayName, updated.Alias)
		if updated.Email != "" {
			_, _ = fmt.Fprintf(stdout, "Email: %s\n", updated.Email)
		}
		if updated.Workspace != "" {
			_, _ = fmt.Fprintf(stdout, "Workspace: %s\n", updated.Workspace)
		}
		return nil
	})
}

func commandProfileIdentity(item httpapi.CommandProfile) profilefeature.IdentityProfile {
	return profilefeature.IdentityProfile{
		ID: item.ID, Alias: item.Alias, DisplayName: item.DisplayName, Email: item.Email, Workspace: item.Workspace,
		Status: item.Status, IdentityHomeID: item.IdentityHomeID, IdentityHomeOwnership: item.IdentityHomeOwnership,
		AuthenticationMethod: item.AuthenticationMethod, Selected: item.Selected, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
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
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
}

func writeReauthenticationResult(stdout io.Writer, result profilefeature.ReauthenticationResult) {
	_, _ = fmt.Fprintf(stdout, "Profile: %s (%s)\n", result.Profile.Alias, result.Profile.Status)
	_, _ = fmt.Fprintf(stdout, "Identity Home: %s\n", result.Profile.IdentityHomeOwnership)
	_, _ = fmt.Fprintf(stdout, "Reauthenticated: %t\n", result.Reauthenticated)
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
	fmt.Fprintln(stderr, "  codex-folio profile add ALIAS [--identity-home PATH] [--configuration-pack PACK_ID@VERSION] [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--yes] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile reauthenticate ALIAS [--browser|--device-code] [--codex-bin PATH] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile list [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile edit ALIAS [--alias ALIAS] [--display-name NAME] [--email LABEL] [--workspace LABEL] [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile remove ALIAS [--replacement ALIAS] [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile restore ALIAS [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stderr, "  codex-folio profile purge ALIAS [--confirm ALIAS] [--state-root PATH] [--vault-mode MODE] [--non-interactive] [--json]")
}
