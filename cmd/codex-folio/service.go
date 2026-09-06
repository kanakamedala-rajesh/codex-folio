package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"time"

	codexadapter "venkatasudha.com/codex-folio/internal/adapters/codex"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/store"
)

type serviceOptions struct {
	stateRoot *string
	json      bool
	candidate string
	vaultMode platform.VaultMode
}

type servicePathResolver func(*string) (platform.Paths, error)

func runService(args []string, stdout, stderr io.Writer) int {
	return runServiceWithInput(args, os.Stdin, stdout, stderr, resolveCLIPaths)
}

func runServiceWithPathResolver(args []string, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runServiceWithInput(args, os.Stdin, stdout, stderr, resolvePaths)
}

func runServiceWithInput(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver) int {
	return runServiceWithInputAndDiagnostics(args, input, stdout, stderr, resolvePaths, newServiceDiagnosticSink())
}

func runServiceWithInputAndDiagnostics(args []string, input io.Reader, stdout, stderr io.Writer, resolvePaths servicePathResolver, diagnosticSink diagnostics.Sink) int {
	if len(args) == 0 {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "a service command is required", diagnosticSink)
	}

	command := args[0]
	recoveryAction := ""
	serviceArgs := args[1:]
	if command == "recovery" {
		if len(args) < 2 {
			return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "recovery requires verify, list, or restore", diagnosticSink)
		}
		recoveryAction = args[1]
		if recoveryAction != "verify" && recoveryAction != "list" && recoveryAction != "restore" {
			return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "unknown recovery action", diagnosticSink)
		}
		serviceArgs = args[2:]
	}
	if command != "status" && command != "start" && command != "recovery" {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "unknown service command", diagnosticSink)
	}
	options, err := parseServiceOptions(serviceArgs)
	if err != nil {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "invalid service arguments", diagnosticSink)
	}
	if recoveryAction == "restore" && options.candidate == "" {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "recovery restore requires --candidate ID", diagnosticSink)
	}
	if recoveryAction == "" && options.candidate != "" {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "--candidate is only valid for recovery restore", diagnosticSink)
	}
	if recoveryAction != "" && recoveryAction != "restore" && options.candidate != "" {
		return writeServiceUsageDiagnostic(stderr, apperrors.CLIUsage, "--candidate is only valid for recovery restore", diagnosticSink)
	}

	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		if options.stateRoot != nil {
			code := apperrors.Code(err)
			if code == apperrors.PlatformStatePathInvalid || code == apperrors.PlatformStatePathUnsafe {
				return writeServiceUsageDiagnostic(stderr, code, serviceRemediation(code), diagnosticSink)
			}
		}
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}

	switch command {
	case "status":
		return runServiceStatusWithDiagnostics(paths, options, stdout, stderr, diagnosticSink)
	case "start":
		return runServiceStartWithInputWithDiagnostics(paths, options, input, stdout, stderr, diagnosticSink)
	case "recovery":
		return runServiceRecoveryWithDiagnostics(paths, recoveryAction, options, stdout, stderr, diagnosticSink)
	default:
		fmt.Fprintf(stderr, "codex-folio [%s]: unknown service command %q\n", apperrors.CLIUsage, command)
		writeServiceUsage(stderr)
		return exitUsage
	}
}

func parseServiceOptions(args []string) (serviceOptions, error) {
	var options serviceOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			if options.json {
				return serviceOptions{}, errors.New("--json may be supplied only once")
			}
			options.json = true
		case arg == "--state-root":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return serviceOptions{}, errors.New("--state-root requires a value")
			}
			index++
			if err := setServiceStateRoot(&options, args[index]); err != nil {
				return serviceOptions{}, err
			}
		case strings.HasPrefix(arg, "--state-root="):
			if err := setServiceStateRoot(&options, strings.TrimPrefix(arg, "--state-root=")); err != nil {
				return serviceOptions{}, err
			}
		case arg == "--candidate":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return serviceOptions{}, errors.New("--candidate requires a value")
			}
			index++
			if err := setServiceCandidate(&options, args[index]); err != nil {
				return serviceOptions{}, err
			}
		case strings.HasPrefix(arg, "--candidate="):
			if err := setServiceCandidate(&options, strings.TrimPrefix(arg, "--candidate=")); err != nil {
				return serviceOptions{}, err
			}
		case arg == "--vault-mode":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return serviceOptions{}, errors.New("--vault-mode requires a value")
			}
			index++
			if err := setServiceVaultMode(&options, args[index]); err != nil {
				return serviceOptions{}, err
			}
		case strings.HasPrefix(arg, "--vault-mode="):
			if err := setServiceVaultMode(&options, strings.TrimPrefix(arg, "--vault-mode=")); err != nil {
				return serviceOptions{}, err
			}
		default:
			return serviceOptions{}, errors.New("unexpected service argument")
		}
	}
	return options, nil
}

func setServiceVaultMode(options *serviceOptions, value string) error {
	if options.vaultMode != "" {
		return errors.New("only one vault-mode selection may be supplied")
	}
	mode := platform.VaultMode(strings.TrimSpace(value))
	if mode != platform.VaultModeSecretService && mode != platform.VaultModePassphrase {
		return errors.New("vault mode must be secret-service or passphrase")
	}
	options.vaultMode = mode
	return nil
}

func setServiceStateRoot(options *serviceOptions, value string) error {
	if options.stateRoot != nil {
		return errors.New("only one state-root override may be supplied")
	}
	options.stateRoot = &value
	return nil
}

func setServiceCandidate(options *serviceOptions, value string) error {
	if options.candidate != "" {
		return errors.New("only one recovery candidate may be supplied")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("recovery candidate must not be empty")
	}
	options.candidate = value
	return nil
}

func resolveCLIPaths(override *string) (platform.Paths, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return platform.Paths{}, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("working directory is unavailable"))
	}

	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return platform.Paths{}, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("user home is unavailable"))
	}
	currentUser, err := user.Current()
	if err != nil || currentUser == nil || strings.TrimSpace(currentUser.HomeDir) == "" {
		return platform.Paths{}, apperrors.New(apperrors.PlatformStatePathInvalid, errors.New("current user home is unavailable"))
	}
	return platform.ResolvePaths(platform.PathOptions{
		HomeDir:           homeDirectory,
		OwnerHomeDir:      currentUser.HomeDir,
		StateRootOverride: override,
		WorkingDirectory:  workingDirectory,
	})
}

func runServiceStatus(paths platform.Paths, options serviceOptions, stdout, stderr io.Writer) int {
	return runServiceStatusWithDiagnostics(paths, options, stdout, stderr, nil)
}

func runServiceStatusWithDiagnostics(paths platform.Paths, options serviceOptions, stdout, stderr io.Writer, diagnosticSink diagnostics.Sink) int {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if options.json {
		if err := writeServiceJSON(stdout, serviceOutput{
			Status:    statusName(status.Running),
			PID:       metadataPID(status.Metadata),
			StartedAt: metadataStart(status.Metadata),
			Reused:    false,
		}); err != nil {
			recordServiceDiagnostic(diagnosticSink, apperrors.CLIInternal, diagnostics.SeverityError)
			fmt.Fprintf(stderr, "codex-folio [%s]: could not encode service status\n", apperrors.CLIInternal)
			return exitFailure
		}
		return exitSuccess
	}
	if status.Running {
		_, _ = io.WriteString(stdout, "service owner running\n")
	} else {
		_, _ = io.WriteString(stdout, "service owner stopped\n")
	}
	return exitSuccess
}

func runServiceStart(paths platform.Paths, options serviceOptions, stdout, stderr io.Writer) int {
	return runServiceStartWithInput(paths, options, nil, stdout, stderr)
}

func runServiceStartWithInput(paths platform.Paths, options serviceOptions, input io.Reader, stdout, stderr io.Writer) int {
	return runServiceStartWithInputWithDiagnostics(paths, options, input, stdout, stderr, nil)
}

func runServiceStartWithInputWithDiagnostics(paths platform.Paths, options serviceOptions, input io.Reader, stdout, stderr io.Writer, diagnosticSink diagnostics.Sink) int {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		if err := writeServiceState(stdout, stderr, options.json, status, true); err != nil {
			recordServiceDiagnostic(diagnosticSink, apperrors.CLIInternal, diagnostics.SeverityError)
			return exitFailure
		}
		return exitSuccess
	}

	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		if apperrors.Code(err) == apperrors.PlatformServiceAlreadyRunning {
			status, statusErr := platform.Discover(paths, platform.OwnerOptions{})
			if statusErr == nil && status.Running {
				if err := writeServiceState(stdout, stderr, options.json, status, true); err != nil {
					recordServiceDiagnostic(diagnosticSink, apperrors.CLIInternal, diagnostics.SeverityError)
					return exitFailure
				}
				return exitSuccess
			}
		}
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	passphrase := ""
	if options.vaultMode == platform.VaultModePassphrase {
		passphrase, err = readServiceVaultPassphrase(input)
		if err != nil {
			_ = owner.Close()
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
	}
	stateStore, err := openServiceStoreWithVaultMode(paths, options.vaultMode, passphrase)
	if err != nil {
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	attachServiceDiagnosticStore(diagnosticSink, stateStore)
	selector, err := profile.NewSelector(stateStore)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	registry, err := profile.NewRegistry(stateStore)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	lifecycle, err := newProfileLifecycle(paths, stateStore)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	configurationPacks, err := newConfigurationPackService(stateStore)
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	launches, err := newLaunchCommandService(stateStore, configurationPacks, codexadapter.NewAuthenticator())
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if err := launches.workflow.Reconcile(context.Background(), foregroundProcessInspector{}); err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	profileAuthentication, err := newProfileAuthenticationCommandService(paths, stateStore, configurationPacks, codexadapter.NewResolver(codexadapter.ResolverOptions{}), codexadapter.NewAuthenticator())
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	commandToken, err := newCommandToken()
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	server, err := httpapi.NewServer(httpapi.Options{Diagnostics: diagnosticSink, Selection: selector, Profiles: registry, ProfileLifecycle: lifecycle, ProfileAuthentication: profileAuthentication, ConfigurationPacks: configurationPacks, Launches: launches, CommandToken: commandToken})
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	listener, err := server.Listen()
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: commandToken}); err != nil {
		_ = server.Close()
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	return waitForServiceStopWithDiagnostics(owner, stateStore, server, options, stdout, stderr, false, serveErrors, diagnosticSink)
}

func newProfileLifecycle(paths platform.Paths, stateStore *store.Store) (*profile.Lifecycle, error) {
	homes, err := platform.NewProfileHomeLifecycle(paths.ManagedHomes, paths.ProfileQuarantine)
	if err != nil {
		return nil, err
	}
	return profile.NewLifecycle(stateStore, homes)
}

func newConfigurationPackService(stateStore *store.Store) (*configpack.Service, error) {
	return configpack.NewService(stateStore, configpack.NewProjector(nil))
}

func newCommandToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func runServiceRecovery(paths platform.Paths, action string, options serviceOptions, stdout, stderr io.Writer) (resultCode int) {
	return runServiceRecoveryWithDiagnostics(paths, action, options, stdout, stderr, nil)
}

func runServiceRecoveryWithDiagnostics(paths platform.Paths, action string, options serviceOptions, stdout, stderr io.Writer, diagnosticSink diagnostics.Sink) (resultCode int) {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if status.Running {
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.PlatformServiceAlreadyRunning, errors.New("recovery requires the service owner to be stopped")), diagnosticSink)
	}

	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	defer func() {
		if closeErr := owner.Close(); closeErr != nil && resultCode == exitSuccess {
			resultCode = writeServiceErrorWithDiagnostics(stderr, closeErr, diagnosticSink)
		}
	}()

	recovery, err := store.NewRecovery(store.RecoveryOptions{DatabasePath: paths.DatabaseFile})
	if err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}

	switch action {
	case "verify":
		verification, verifyErr := recovery.VerifyActive(context.Background())
		if verifyErr != nil {
			return writeServiceErrorWithDiagnostics(stderr, verifyErr, diagnosticSink)
		}
		if options.json {
			if err := writeServiceJSON(stdout, verification); err != nil {
				return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "active database verified (schema %d)\n", verification.SchemaVersion)
		}
	case "list":
		candidates, listErr := recovery.ListCandidates(context.Background())
		if listErr != nil {
			return writeServiceErrorWithDiagnostics(stderr, listErr, diagnosticSink)
		}
		if options.json {
			if err := writeServiceJSON(stdout, struct {
				Candidates []store.RecoveryCandidate `json:"candidates"`
			}{Candidates: candidates}); err != nil {
				return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
			}
		} else if len(candidates) == 0 {
			_, _ = io.WriteString(stdout, "no recovery candidates\n")
		} else {
			for _, candidate := range candidates {
				state := "invalid"
				if candidate.Valid {
					state = "valid"
				}
				_, _ = fmt.Fprintf(stdout, "%s: %s (schema %d, captured %s)\n", candidate.ID, state, candidate.SchemaVersion, candidate.CreatedAt.UTC().Format(time.RFC3339))
			}
		}
	case "restore":
		restored, restoreErr := recovery.Restore(context.Background(), options.candidate)
		if restoreErr != nil {
			return writeServiceErrorWithDiagnostics(stderr, restoreErr, diagnosticSink)
		}
		if options.json {
			if err := writeServiceJSON(stdout, restored); err != nil {
				return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "restored %s; known loss window %s to %s\n", restored.CandidateID, restored.KnownLossWindowStart.UTC().Format(time.RFC3339), restored.KnownLossWindowEnd.UTC().Format(time.RFC3339))
			if restored.PreservedDatabaseID != "" {
				_, _ = fmt.Fprintf(stdout, "preserved displaced state: %s\n", restored.PreservedDatabaseID)
			}
		}
	default:
		return writeServiceErrorWithDiagnostics(stderr, apperrors.New(apperrors.CLIUsage, errors.New("unknown recovery action")), diagnosticSink)
	}
	return resultCode
}

func waitForServiceStop(owner *platform.Owner, stateStore *store.Store, server *httpapi.Server, options serviceOptions, stdout, stderr io.Writer, reused bool, serveErrors <-chan error) int {
	return waitForServiceStopWithDiagnostics(owner, stateStore, server, options, stdout, stderr, reused, serveErrors, nil)
}

func waitForServiceStopWithDiagnostics(owner *platform.Owner, stateStore *store.Store, server *httpapi.Server, options serviceOptions, stdout, stderr io.Writer, reused bool, serveErrors <-chan error, diagnosticSink diagnostics.Sink) int {
	metadata := owner.Metadata()
	status := platform.OwnerStatus{Running: true, Metadata: &metadata}
	if err := writeServiceStateWithDashboard(stdout, stderr, options.json, status, reused, server.BootstrapURL()); err != nil {
		recordServiceDiagnostic(diagnosticSink, apperrors.CLIInternal, diagnostics.SeverityError)
		_ = server.Close()
		_ = stateStore.Close()
		_ = owner.Close()
		return exitFailure
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		_ = stateStore.Close()
		_ = owner.Close()
		if err != nil {
			return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
		}
		return exitSuccess
	}
	if err := server.Close(); err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if err := stateStore.Close(); err != nil {
		_ = owner.Close()
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	if err := owner.Close(); err != nil {
		return writeServiceErrorWithDiagnostics(stderr, err, diagnosticSink)
	}
	return exitSuccess
}

type serviceOutput struct {
	Status    string     `json:"status"`
	PID       int        `json:"pid,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	Reused    bool       `json:"reused,omitempty"`
	Dashboard string     `json:"dashboard_url,omitempty"`
}

func writeServiceState(stdout, stderr io.Writer, jsonOutput bool, status platform.OwnerStatus, reused bool) error {
	return writeServiceStateWithDashboard(stdout, stderr, jsonOutput, status, reused, "")
}

func writeServiceStateWithDashboard(stdout, stderr io.Writer, jsonOutput bool, status platform.OwnerStatus, reused bool, dashboardURL string) error {
	if jsonOutput {
		if err := writeServiceJSON(stdout, serviceOutput{
			Status:    statusName(status.Running),
			PID:       metadataPID(status.Metadata),
			StartedAt: metadataStart(status.Metadata),
			Reused:    reused,
			Dashboard: dashboardURL,
		}); err != nil {
			fmt.Fprintf(stderr, "codex-folio [%s]: could not encode service status\n", apperrors.CLIInternal)
			return err
		}
		return nil
	}
	if reused {
		_, _ = io.WriteString(stdout, "service owner reused\n")
	} else if dashboardURL != "" {
		_, _ = fmt.Fprintf(stdout, "service owner started; dashboard: %s\n", dashboardURL)
		_, _ = io.WriteString(stdout, "press Ctrl-C to stop\n")
	} else {
		_, _ = io.WriteString(stdout, "service owner started; press Ctrl-C to stop\n")
	}
	return nil
}

func writeServiceJSON(stdout io.Writer, output any) error {
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

func writeServiceError(stderr io.Writer, err error) int {
	return writeServiceErrorWithDiagnostics(stderr, err, nil)
}

type serviceDiagnosticSink struct {
	recorder   *diagnostics.Recorder
	stateStore *store.Store
}

func newServiceDiagnosticSink() diagnostics.Sink {
	recorder, err := diagnostics.NewRecorder(diagnostics.RecorderOptions{})
	if err != nil {
		return nil
	}
	return &serviceDiagnosticSink{recorder: recorder}
}

func attachServiceDiagnosticStore(sink diagnostics.Sink, stateStore *store.Store) {
	serviceSink, ok := sink.(*serviceDiagnosticSink)
	if !ok {
		return
	}
	serviceSink.stateStore = stateStore
}

func (sink *serviceDiagnosticSink) Record(event diagnostics.Event) error {
	if sink == nil {
		return nil
	}
	if sink.recorder != nil {
		if err := sink.recorder.Record(event); err != nil {
			return err
		}
	}
	if sink.stateStore != nil {
		return sink.stateStore.RecordDiagnostic(context.Background(), event)
	}
	return nil
}

func writeServiceErrorWithDiagnostics(stderr io.Writer, err error, diagnosticSink diagnostics.Sink) int {
	code := diagnostics.CodeFor(err, apperrors.CLIInternal)
	recordServiceDiagnostic(diagnosticSink, code, diagnostics.SeverityError)
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, serviceRemediation(code))
	return exitFailure
}

func writeServiceUsageDiagnostic(stderr io.Writer, code, message string, diagnosticSink diagnostics.Sink) int {
	code = diagnostics.CodeFor(nil, code)
	if strings.TrimSpace(message) == "" {
		message = serviceRemediation(code)
	}
	recordServiceDiagnostic(diagnosticSink, code, diagnostics.SeverityWarning)
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, message)
	writeServiceUsage(stderr)
	return exitUsage
}

func recordServiceDiagnostic(diagnosticSink diagnostics.Sink, code string, severity diagnostics.Severity) {
	if diagnosticSink == nil {
		return
	}
	code = diagnostics.CodeFor(nil, code)
	event, err := diagnostics.NewEvent(time.Now().UTC(), severity, diagnostics.ComponentForCode(code), code, diagnostics.Context{
		Operation: diagnostics.OperationCommand,
		State:     serviceDiagnosticState(code),
	})
	if err != nil {
		return
	}
	_ = diagnosticSink.Record(event)
}

func serviceDiagnosticState(code string) string {
	switch code {
	case apperrors.CLIUsage,
		apperrors.CLIShellIntegrationInvalid,
		apperrors.LaunchLeaseInvalid,
		apperrors.LaunchProfileNotFound,
		apperrors.ProfileNotSelectable,
		apperrors.ProfileReauthenticationRequired,
		apperrors.ConfigurationPackInvalid,
		apperrors.ConfigurationPackNotFound,
		apperrors.ConfigurationPackNotApproved,
		apperrors.ConfigurationPackAssignmentInvalid,
		apperrors.ConfigurationPackPromotionReviewRequired,
		apperrors.HTTPAPIHostInvalid,
		apperrors.HTTPAPIOriginInvalid,
		apperrors.HTTPAPIBootstrapInvalid,
		apperrors.HTTPAPISessionInvalid,
		apperrors.HTTPAPISessionExpired,
		apperrors.HTTPAPICSRFInvalid,
		apperrors.HTTPAPIMethodNotAllowed,
		apperrors.HTTPAPIRouteNotFound:
		return diagnostics.StateRejected
	case apperrors.PlatformStatePathInvalid,
		apperrors.PlatformStatePathUnsafe,
		apperrors.LaunchPlanInvalid,
		apperrors.LaunchProcessStatusInvalid,
		apperrors.DiagnosticsConfigurationInvalid,
		apperrors.DiagnosticsEventInvalid,
		apperrors.StoreSchemaIncompatible,
		apperrors.StoreIntegrityFailed,
		apperrors.StoreMigrationFailed,
		apperrors.StoreMigrationPartial,
		apperrors.VaultKeyInvalid,
		apperrors.VaultEnvelopeInvalid,
		apperrors.VaultEnvelopeUnsupported,
		apperrors.VaultKeyGenerationMismatch,
		apperrors.VaultEncryptionFailed:
		return diagnostics.StateInvalid
	case apperrors.ConfigurationPackProjectionFailed:
		return diagnostics.StateFailed
	case apperrors.PlatformServiceAlreadyRunning:
		return diagnostics.StateContention
	case apperrors.VaultLocked:
		return diagnostics.StateLocked
	case apperrors.PlatformPermissionDenied,
		apperrors.LaunchProfileUnavailable,
		apperrors.ProfileAuthenticationUnavailable,
		apperrors.PlatformServiceUnavailable,
		apperrors.HTTPAPIServiceUnavailable,
		apperrors.StoreOpenFailed,
		apperrors.VaultUnavailable:
		return diagnostics.StateUnavailable
	default:
		return diagnostics.StateFailed
	}
}

func serviceRemediation(code string) string {
	switch code {
	case apperrors.ProfileSetupInvalid:
		return "profile setup state or arguments are invalid"
	case apperrors.CLIShellIntegrationInvalid:
		return "generated shell integration is changed or incompatible; remove it manually before retrying"
	case apperrors.CLIShellIntegrationFailed:
		return "the shell integration file could not be read or written"
	case apperrors.ProfileAliasInvalid:
		return "alias must start with a letter or number and contain only portable ASCII letters, numbers, '.', '_' or '-'; maximum 64 characters"
	case apperrors.ProfileAliasTaken:
		return "profile alias is already in use; choose another alias"
	case apperrors.ProfileSetupChoiceRequired:
		return "non-interactive profile authentication requires exactly one of --browser or --device-code"
	case apperrors.ProfileAuthenticationCancelled:
		return "Codex authentication was cancelled; rerun profile add or profile reauthenticate to retry"
	case apperrors.ProfileAuthenticationFailed:
		return "Codex authentication failed; rerun profile add or profile reauthenticate to retry"
	case apperrors.ProfileReauthenticationRequired:
		return "Codex authentication requires recovery; run profile reauthenticate ALIAS [--browser|--device-code]"
	case apperrors.ProfileAuthenticationUnavailable:
		return "Codex authentication status is unavailable; retry profile reauthenticate"
	case apperrors.ProfileHomeInvalid:
		return "the Identity Home could not be resolved or validated safely"
	case apperrors.ProfileValidationFailed:
		return "Codex did not validate the Identity Home"
	case apperrors.ProfileNotSelectable:
		return "the Identity Profile is not eligible for selection"
	case apperrors.ProfileRemovalBlocked:
		return "the Identity Profile is the Launch Profile of a running Managed Launch"
	case apperrors.ProfileReplacementRequired:
		return "removing the Selected Profile requires an eligible --replacement"
	case apperrors.ProfileConfirmationInvalid:
		return "type the exact CLI Alias or provide it with --confirm"
	case apperrors.ProfileQuarantineInvalid:
		return "the Profile Quarantine operation could not be completed safely"
	case apperrors.ProfileQuarantineExpired:
		return "the Profile Quarantine recovery period has expired"
	case apperrors.ConfigurationPackInvalid:
		return "the configuration pack or its reviewed files are invalid"
	case apperrors.ConfigurationPackNotFound:
		return "the configuration pack version was not found"
	case apperrors.ConfigurationPackNotApproved:
		return "only an approved configuration pack version can be assigned or projected"
	case apperrors.ConfigurationPackAssignmentInvalid:
		return "configuration packs require a ready Managed Identity Profile"
	case apperrors.ConfigurationPackProjectionFailed:
		return "the configuration pack projection failed; the prior Identity Home remains recoverable"
	case apperrors.ConfigurationPackPromotionReviewRequired:
		return "promotion requires explicit reviewed confirmation"
	case apperrors.LaunchProfileNotFound:
		return "the requested Identity Profile was not found"
	case apperrors.LaunchProfileUnavailable:
		return "the requested Identity Profile is not ready to launch"
	case apperrors.LaunchPlanInvalid:
		return "the launch plan is invalid"
	case apperrors.LaunchLeaseInvalid:
		return "the Managed Launch lease is invalid or already completed"
	case apperrors.LaunchProcessStartFailed:
		return "Codex could not be started in the foreground"
	case apperrors.LaunchProcessStatusInvalid:
		return "Codex returned an invalid process status"
	case apperrors.DiagnosticsConfigurationInvalid:
		return "the local diagnostics configuration is invalid"
	case apperrors.DiagnosticsEventInvalid:
		return "the diagnostic event was rejected"
	case apperrors.PlatformStatePathInvalid:
		return "state root must be an absolute path"
	case apperrors.PlatformStatePathUnsafe:
		return "state root is unsafe; choose an absolute path outside the working repository"
	case apperrors.PlatformPermissionDenied:
		return "app-local state is unavailable with its current permissions"
	case apperrors.PlatformServiceAlreadyRunning:
		return "a user-scoped service already owns this state"
	case apperrors.PlatformServiceMetadataInvalid:
		return "the running service owner descriptor is invalid"
	case apperrors.PlatformServiceUnavailable:
		return "the user-scoped service cannot access local state"
	case apperrors.HTTPAPIServiceUnavailable:
		return "the local dashboard service could not bind its loopback listener"
	case apperrors.StoreOpenFailed:
		return "the local SQLite store could not be opened"
	case apperrors.StoreIntegrityFailed:
		return "local SQLite integrity verification failed; writes are stopped"
	case apperrors.StoreSchemaIncompatible:
		return "the local SQLite schema is incompatible with this CodexFolio build"
	case apperrors.StoreMigrationFailed:
		return "the local SQLite migration failed; the previous state was preserved"
	case apperrors.StoreMigrationPartial:
		return "the local SQLite migration is incomplete; writes are stopped"
	case apperrors.StoreReadFailed:
		return "the local SQLite state could not be read"
	case apperrors.StoreWriteFailed:
		return "the local SQLite write failed; no partial state was accepted"
	case apperrors.StoreDiagnosticWriteFailed:
		return "local diagnostics could not be stored; the operation remains bounded"
	case apperrors.StoreBackupFailed:
		return "a validated local SQLite recovery backup could not be created; writes are stopped"
	case apperrors.StoreRecoveryCandidateInvalid:
		return "the selected recovery candidate failed validation"
	case apperrors.StoreRecoveryCandidateNotFound:
		return "the recovery candidate was not found; list candidates and choose one explicitly"
	case apperrors.StoreRecoveryRestoreFailed:
		return "the local SQLite restore failed; active state was preserved or rolled back"
	case apperrors.VaultUnavailable:
		return "the local encryption vault is unavailable; sensitive state is blocked"
	case apperrors.VaultLocked:
		return "the local encryption vault is locked; unlock it before using sensitive state"
	case apperrors.VaultKeyInvalid:
		return "the local encryption vault material is invalid; sensitive state is blocked"
	case apperrors.VaultEnvelopeInvalid:
		return "the local encryption envelope is invalid; sensitive state is blocked"
	case apperrors.VaultEnvelopeUnsupported:
		return "the local encryption envelope is not supported by this build"
	case apperrors.VaultKeyGenerationMismatch:
		return "the local encryption vault generation does not match this state"
	case apperrors.VaultEncryptionFailed:
		return "local encryption failed; sensitive state was not written"
	default:
		return "the command could not complete"
	}
}

func writeServiceUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "Usage:")
	fmt.Fprintln(stderr, "  codex-folio service status [--state-root PATH] [--vault-mode MODE] [--json]")
	fmt.Fprintln(stderr, "  codex-folio service start [--state-root PATH] [--vault-mode secret-service|passphrase] [--json]")
	fmt.Fprintln(stderr, "  codex-folio service recovery {verify|list|restore} [--state-root PATH] [--candidate ID] [--json]")
}

func readServiceVaultPassphrase(input io.Reader) (string, error) {
	if input == nil {
		return "", apperrors.New(apperrors.VaultLocked, errors.New("headless vault requires an explicit passphrase input"))
	}
	reader, ok := input.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(input)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", apperrors.New(apperrors.VaultLocked, errors.New("headless vault passphrase input is unavailable"))
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", apperrors.New(apperrors.VaultLocked, errors.New("headless vault requires a non-empty passphrase"))
	}
	return line, nil
}

func statusName(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}

func metadataPID(metadata *platform.OwnerMetadata) int {
	if metadata == nil {
		return 0
	}
	return metadata.PID
}

func metadataStart(metadata *platform.OwnerMetadata) *time.Time {
	if metadata == nil {
		return nil
	}
	startedAt := metadata.StartedAt
	return &startedAt
}
