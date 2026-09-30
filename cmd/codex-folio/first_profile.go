package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/profile"
)

// guideFirstProfile runs after the persistent owner is ready. The service
// remains the sole writer throughout onboarding and the subsequent launch.
func guideFirstProfile(paths platform.Paths, input io.Reader, stdout, stderr io.Writer) (bool, int) {
	connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
	if err != nil {
		return false, writeServiceError(stderr, err)
	}
	client, err := newServiceCommandClient(connection)
	if err != nil {
		return false, writeServiceError(stderr, err)
	}
	selection, err := client.GetSelection(context.Background())
	if err != nil {
		return false, writeServiceError(stderr, err)
	}
	if len(selection.Profiles) != 0 {
		return true, exitSuccess
	}
	for {
		inventory, err := client.ListProfiles(context.Background())
		if err != nil {
			return false, writeServiceError(stderr, err)
		}
		_, _ = io.WriteString(stdout, "No ready Identity Profiles. Choose a setup option (q to cancel):\n")
		for index, item := range inventory.Profiles {
			if item.Status == profile.StatusPending {
				_, _ = fmt.Fprintf(stdout, "%d. Resume %s (%s)\n", index+1, item.DisplayName, item.Alias)
			} else if item.Status != profile.StatusReady {
				_, _ = fmt.Fprintf(stdout, "%d. Reauthenticate %s (%s)\n", index+1, item.DisplayName, item.Alias)
			}
		}
		_, _ = io.WriteString(stdout, "m. Create an isolated Managed Identity Home\nr. Register an existing Referenced Identity Home\n> ")
		choice, ok := readFirstProfileChoice(input)
		if !ok || strings.EqualFold(choice, "q") {
			return false, exitSuccess
		}
		request := httpapi.CommandProfileAuthenticationRequest{Action: "add", AuthMethod: profile.AuthMethodAutomatic}
		switch strings.ToLower(choice) {
		case "m", "r":
			_, _ = io.WriteString(stdout, "Profile alias (q to cancel): ")
			request.Alias, ok = readFirstProfileChoice(input)
			if !ok || strings.EqualFold(request.Alias, "q") {
				return false, exitSuccess
			}
			if err := profile.ValidateAlias(request.Alias); err != nil {
				_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: choose a valid profile alias\n", apperrors.ProfileAliasInvalid)
				continue
			}
			for _, item := range inventory.Profiles {
				if strings.EqualFold(item.Alias, request.Alias) {
					_, _ = io.WriteString(stderr, "codex-folio: that alias already exists; resume its Pending entry instead\n")
					request.Alias = ""
					break
				}
			}
			if request.Alias == "" {
				continue
			}
			_, _ = io.WriteString(stdout, "Display name (Enter uses alias): ")
			request.DisplayName, ok = readFirstProfileChoice(input)
			if !ok {
				return false, exitSuccess
			}
			if len([]rune(request.DisplayName)) > 128 {
				_, _ = io.WriteString(stderr, "codex-folio: display name must be at most 128 characters\n")
				continue
			}
			if strings.EqualFold(choice, "r") {
				_, _ = io.WriteString(stdout, "A Referenced Identity Home remains owned by Codex. Direct Codex and this profile share its authentication and files; removing this profile only removes its reference. Registration does not consent to history import.\n")
				defaultHome := defaultExistingCodexHome()
				if defaultHome != "" {
					_, _ = io.WriteString(stdout, "Existing Codex home path (Enter uses the current Codex home): ")
				} else {
					_, _ = io.WriteString(stdout, "Existing Codex home absolute path: ")
				}
				request.ReferencedHomePath, ok = readFirstProfileChoice(input)
				if !ok {
					return false, exitSuccess
				}
				if request.ReferencedHomePath == "" {
					request.ReferencedHomePath = defaultHome
				}
				if !filepath.IsAbs(request.ReferencedHomePath) {
					_, _ = io.WriteString(stderr, "codex-folio: choose an absolute existing Codex home path\n")
					continue
				}
			} else {
				_, _ = io.WriteString(stdout, "A separate Managed Identity Home will be created. Codex owns its sign-in; credentials are not copied.\n")
			}
		default:
			matched := false
			for index, item := range inventory.Profiles {
				if choice == fmt.Sprint(index+1) && item.Status != profile.StatusReady {
					request.Alias = item.Alias
					matched = true
					if item.Status != profile.StatusPending {
						request.Action = "reauthenticate"
						_, _ = fmt.Fprintf(stdout, "Reauthenticating profile %s through Codex.\n", item.Alias)
					} else {
						_, _ = fmt.Fprintf(stdout, "Resuming Pending profile %s.\n", item.Alias)
					}
					if item.Status == profile.StatusPending && item.IdentityHomeID == "" {
						_, _ = io.WriteString(stdout, "Home was not prepared. Choose [m] isolated Managed or [r] existing Referenced home: ")
						homeChoice, readOK := readFirstProfileChoice(input)
						if !readOK {
							return false, exitSuccess
						}
						if strings.EqualFold(homeChoice, "r") {
							_, _ = io.WriteString(stdout, "Existing Codex home absolute path: ")
							request.ReferencedHomePath, ok = readFirstProfileChoice(input)
							if !ok {
								return false, exitSuccess
							}
							if !filepath.IsAbs(request.ReferencedHomePath) {
								_, _ = io.WriteString(stderr, "codex-folio: choose an absolute existing Codex home path\n")
								request.Alias = ""
							}
						} else if !strings.EqualFold(homeChoice, "m") {
							_, _ = io.WriteString(stderr, "codex-folio: choose m or r\n")
							request.Alias = ""
						}
					}
					break
				}
			}
			if !matched || request.Alias == "" {
				_, _ = io.WriteString(stderr, "codex-folio: choose a listed setup option\n")
				continue
			}
		}
		_, _ = io.WriteString(stdout, "Authentication: Enter uses automatic Codex sign-in (and reuses valid referenced sign-in), b runs browser login, d runs device login: ")
		authChoice, ok := readFirstProfileChoice(input)
		if !ok {
			return false, exitSuccess
		}
		switch strings.ToLower(authChoice) {
		case "":
		case "b":
			request.AuthMethod = profile.AuthMethodBrowser
		case "d":
			request.AuthMethod = profile.AuthMethodDeviceCode
		default:
			_, _ = io.WriteString(stderr, "codex-folio: choose Enter, b, or d\n")
			continue
		}
		_, _ = io.WriteString(stdout, "Checking Codex authentication and completing profile setup...\n")
		result, err := client.AuthenticateProfile(context.Background(), request, stderr)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codex-folio [%s]: setup remains Pending; choose its Resume entry to retry, or q to stop\n", apperrors.Code(err))
			continue
		}
		if request.Action == "reauthenticate" {
			if result.Reauthentication == nil || result.Reauthentication.Profile.Status != profile.StatusReady {
				return false, writeServiceError(stderr, apperrors.New(apperrors.ProfileAuthenticationFailed, profile.ErrAuthenticationFailed))
			}
			writeReauthenticationResult(stdout, *result.Reauthentication)
			offerOnboardingHistory(client, input, stdout, stderr)
			_, _ = io.WriteString(stdout, "Profile ready. Choose it in the picker to launch Codex.\n")
			return true, exitSuccess
		}
		if result.Setup == nil || result.Setup.Profile.Status != profile.StatusReady {
			return false, writeServiceError(stderr, apperrors.New(apperrors.ProfileSetupInvalid, profile.ErrProfileStateInvalid))
		}
		writeProfileResult(stdout, *result.Setup)
		offerOnboardingHistory(client, input, stdout, stderr)
		_, _ = io.WriteString(stdout, "Profile ready. Choose it in the picker to launch Codex.\n")
		return true, exitSuccess
	}
}

// History import is optional and never changes the readiness of an authenticated
// profile. Every source needs its own affirmative answer after source review.
func offerOnboardingHistory(client *httpapi.CommandClient, input io.Reader, stdout, stderr io.Writer) {
	review, err := client.Activity(context.Background(), httpapi.CommandActivityRequest{Action: "review_sources"})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codex-folio: history source review unavailable (%s); continue to launch and retry in dashboard Sessions\n", apperrors.Code(err))
		return
	}
	if len(review.Sources) == 0 {
		_, _ = io.WriteString(stdout, "No local history sources found. You can review sources later in dashboard Sessions.\n")
		return
	}
	_, _ = io.WriteString(stdout, "Available local history sources (profile registration did not consent to import):\n")
	for _, source := range review.Sources {
		_, _ = fmt.Fprintf(stdout, "- %s: %s (%d sessions)\n", source.Label, source.Status, source.SessionCount)
		if source.Status != activity.SourceStatusSupported {
			continue
		}
		_, _ = fmt.Fprintf(stdout, "Import supported history from %s as Unassigned History? [y/N]: ", source.Label)
		choice, ok := readFirstProfileChoice(input)
		if !ok || !strings.EqualFold(choice, "y") {
			_, _ = io.WriteString(stdout, "History import declined; continuing to launch.\n")
			continue
		}
		result, err := client.Activity(context.Background(), httpapi.CommandActivityRequest{Action: "import_source", SourceID: source.SourceID, Consent: true})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codex-folio: history import failed (%s); continue to launch and retry in dashboard Sessions\n", apperrors.Code(err))
			continue
		}
		if result.Import != nil {
			_, _ = fmt.Fprintf(stdout, "Imported %d sessions into history; %d already present. Unknown ownership remains Unassigned History.\n", result.Import.ImportedCount, result.Import.AlreadyPresentCount)
		}
	}
}

func readFirstProfileChoice(input io.Reader) (string, bool) {
	line, err := readCompanionSetupLine(input)
	return strings.TrimSpace(line), err == nil || (errors.Is(err, io.EOF) && line != "")
}

func defaultExistingCodexHome() string {
	path := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, ".codex")
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(path)
}
