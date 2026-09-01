package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
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
	if len(args) == 0 {
		writeServiceUsage(stderr)
		return exitUsage
	}

	command := args[0]
	recoveryAction := ""
	serviceArgs := args[1:]
	if command == "recovery" {
		if len(args) < 2 {
			fmt.Fprintf(stderr, "codex-folio [%s]: recovery requires verify, list, or restore\n", apperrors.CLIUsage)
			writeServiceUsage(stderr)
			return exitUsage
		}
		recoveryAction = args[1]
		if recoveryAction != "verify" && recoveryAction != "list" && recoveryAction != "restore" {
			fmt.Fprintf(stderr, "codex-folio [%s]: unknown recovery action %q\n", apperrors.CLIUsage, recoveryAction)
			writeServiceUsage(stderr)
			return exitUsage
		}
		serviceArgs = args[2:]
	}
	if command != "status" && command != "start" && command != "recovery" {
		fmt.Fprintf(stderr, "codex-folio [%s]: unknown service command %q\n", apperrors.CLIUsage, command)
		writeServiceUsage(stderr)
		return exitUsage
	}
	options, err := parseServiceOptions(serviceArgs)
	if err != nil {
		fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", apperrors.CLIUsage, err)
		writeServiceUsage(stderr)
		return exitUsage
	}
	if recoveryAction == "restore" && options.candidate == "" {
		fmt.Fprintf(stderr, "codex-folio [%s]: recovery restore requires --candidate ID\n", apperrors.CLIUsage)
		writeServiceUsage(stderr)
		return exitUsage
	}
	if recoveryAction == "" && options.candidate != "" {
		fmt.Fprintf(stderr, "codex-folio [%s]: --candidate is only valid for recovery restore\n", apperrors.CLIUsage)
		writeServiceUsage(stderr)
		return exitUsage
	}
	if recoveryAction != "" && recoveryAction != "restore" && options.candidate != "" {
		fmt.Fprintf(stderr, "codex-folio [%s]: --candidate is only valid for recovery restore\n", apperrors.CLIUsage)
		writeServiceUsage(stderr)
		return exitUsage
	}

	paths, err := resolvePaths(options.stateRoot)
	if err != nil {
		if options.stateRoot != nil {
			code := apperrors.Code(err)
			if code == apperrors.PlatformStatePathInvalid || code == apperrors.PlatformStatePathUnsafe {
				fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, serviceRemediation(code))
				writeServiceUsage(stderr)
				return exitUsage
			}
		}
		return writeServiceError(stderr, err)
	}

	switch command {
	case "status":
		return runServiceStatus(paths, options, stdout, stderr)
	case "start":
		return runServiceStartWithInput(paths, options, input, stdout, stderr)
	case "recovery":
		return runServiceRecovery(paths, recoveryAction, options, stdout, stderr)
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
				return serviceOptions{}, fmt.Errorf("%s requires a value", arg)
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
				return serviceOptions{}, fmt.Errorf("%s requires a value", arg)
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
				return serviceOptions{}, fmt.Errorf("%s requires a value", arg)
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
			return serviceOptions{}, fmt.Errorf("unexpected service argument %q", arg)
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
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if options.json {
		if err := writeServiceJSON(stdout, serviceOutput{
			Status:    statusName(status.Running),
			PID:       metadataPID(status.Metadata),
			StartedAt: metadataStart(status.Metadata),
			Reused:    false,
		}); err != nil {
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
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if status.Running {
		if err := writeServiceState(stdout, stderr, options.json, status, true); err != nil {
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
					return exitFailure
				}
				return exitSuccess
			}
		}
		return writeServiceError(stderr, err)
	}
	passphrase := ""
	if options.vaultMode == platform.VaultModePassphrase {
		passphrase, err = readServiceVaultPassphrase(input)
		if err != nil {
			_ = owner.Close()
			return writeServiceError(stderr, err)
		}
	}
	stateStore, err := openServiceStoreWithVaultMode(paths, options.vaultMode, passphrase)
	if err != nil {
		_ = owner.Close()
		return writeServiceError(stderr, err)
	}
	server, err := httpapi.NewServer(httpapi.Options{})
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceError(stderr, err)
	}
	listener, err := server.Listen()
	if err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceError(stderr, err)
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	return waitForServiceStop(owner, stateStore, server, options, stdout, stderr, false, serveErrors)
}

func runServiceRecovery(paths platform.Paths, action string, options serviceOptions, stdout, stderr io.Writer) (resultCode int) {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if status.Running {
		return writeServiceError(stderr, apperrors.New(apperrors.PlatformServiceAlreadyRunning, errors.New("recovery requires the service owner to be stopped")))
	}

	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	defer func() {
		if closeErr := owner.Close(); closeErr != nil && resultCode == exitSuccess {
			resultCode = writeServiceError(stderr, closeErr)
		}
	}()

	recovery, err := store.NewRecovery(store.RecoveryOptions{DatabasePath: paths.DatabaseFile})
	if err != nil {
		return writeServiceError(stderr, err)
	}

	switch action {
	case "verify":
		verification, verifyErr := recovery.VerifyActive(context.Background())
		if verifyErr != nil {
			return writeServiceError(stderr, verifyErr)
		}
		if options.json {
			if err := writeServiceJSON(stdout, verification); err != nil {
				return writeServiceError(stderr, err)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "active database verified (schema %d)\n", verification.SchemaVersion)
		}
	case "list":
		candidates, listErr := recovery.ListCandidates(context.Background())
		if listErr != nil {
			return writeServiceError(stderr, listErr)
		}
		if options.json {
			if err := writeServiceJSON(stdout, struct {
				Candidates []store.RecoveryCandidate `json:"candidates"`
			}{Candidates: candidates}); err != nil {
				return writeServiceError(stderr, err)
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
			return writeServiceError(stderr, restoreErr)
		}
		if options.json {
			if err := writeServiceJSON(stdout, restored); err != nil {
				return writeServiceError(stderr, err)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "restored %s; known loss window %s to %s\n", restored.CandidateID, restored.KnownLossWindowStart.UTC().Format(time.RFC3339), restored.KnownLossWindowEnd.UTC().Format(time.RFC3339))
			if restored.PreservedDatabaseID != "" {
				_, _ = fmt.Fprintf(stdout, "preserved displaced state: %s\n", restored.PreservedDatabaseID)
			}
		}
	default:
		return writeServiceError(stderr, apperrors.New(apperrors.CLIUsage, errors.New("unknown recovery action")))
	}
	return resultCode
}

func waitForServiceStop(owner *platform.Owner, stateStore *store.Store, server *httpapi.Server, options serviceOptions, stdout, stderr io.Writer, reused bool, serveErrors <-chan error) int {
	metadata := owner.Metadata()
	status := platform.OwnerStatus{Running: true, Metadata: &metadata}
	if err := writeServiceStateWithDashboard(stdout, stderr, options.json, status, reused, server.BootstrapURL()); err != nil {
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
			return writeServiceError(stderr, err)
		}
		return exitSuccess
	}
	if err := server.Close(); err != nil {
		_ = stateStore.Close()
		_ = owner.Close()
		return writeServiceError(stderr, err)
	}
	if err := stateStore.Close(); err != nil {
		_ = owner.Close()
		return writeServiceError(stderr, err)
	}
	if err := owner.Close(); err != nil {
		return writeServiceError(stderr, err)
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
	code := apperrors.Code(err)
	if code == "" {
		code = apperrors.CLIInternal
	}
	fmt.Fprintf(stderr, "codex-folio [%s]: %s\n", code, serviceRemediation(code))
	return exitFailure
}

func serviceRemediation(code string) string {
	switch code {
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
	line, err := bufio.NewReader(input).ReadString('\n')
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
