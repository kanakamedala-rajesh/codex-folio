package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"runtime"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (server *Server) browserProfiles(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		result, err := server.profileInventory(request)
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPut:
		var input ProfileEditRequest
		if !server.decodeProfileRequest(response, request, &input) {
			return
		}
		if strings.TrimSpace(input.Alias) == "" {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
			return
		}
		updated, err := server.profiles.Edit(request.Context(), input.Alias, profile.ProfileEdits{
			Alias: input.NewAlias, DisplayName: input.DisplayName, Email: input.LoginIdentity, Workspace: input.Workspace,
		})
		if err != nil {
			server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProfileSetupInvalid))
			return
		}
		projected, err := server.browserProfile(request, updated)
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		writeJSON(response, http.StatusOK, ProfilesResponse{Profiles: []ProfileSummary{}, Updated: &projected})
	case http.MethodPost:
		server.browserProfileAuthentication(response, request)
	default:
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPut+", "+http.MethodPost)
	}
}

func (server *Server) decodeProfileRequest(response http.ResponseWriter, request *http.Request, input any) bool {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return false
	}
	return true
}

func (server *Server) browserProfileAuthentication(response http.ResponseWriter, request *http.Request) {
	if server.profileAuthentication == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	var input ProfileAuthenticationRequest
	if !server.decodeProfileRequest(response, request, &input) {
		return
	}
	if !validBrowserProfileAuthentication(input) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	if input.Action != "reauthenticate" && input.IdentityHomeMode == string(profile.HomeOwnershipReferenced) && strings.TrimSpace(valueOrEmpty(input.ReferencedHomePath)) == "" {
		item, err := server.profileByAlias(request, input.Alias)
		if err != nil {
			status := http.StatusInternalServerError
			code := diagnostics.CodeFor(err, apperrors.StoreReadFailed)
			if errors.Is(err, profile.ErrNotFound) {
				status, code = http.StatusBadRequest, apperrors.ProfileHomeInvalid
			}
			server.writeAPIError(response, status, code)
			return
		}
		if item.Status != string(profile.StatusPending) || item.IdentityHomeMode != string(profile.HomeOwnershipReferenced) {
			server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileHomeInvalid)
			return
		}
	}
	if input.AuthMethod == string(profile.AuthMethodDeviceCode) && input.Action == "reauthenticate" {
		item, err := server.profileByAlias(request, input.Alias)
		if err != nil {
			server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProfileNotSelectable))
			return
		}
		writeJSON(response, http.StatusOK, ProfileAuthenticationResponse{
			Profile: item, Warnings: []string{}, Outcome: "terminal_required",
			TerminalCommand: profileTerminalCommand("reauthenticate", item.Alias, valueOrEmpty(input.CodexOverride)),
		})
		return
	}

	action := input.Action
	method := profile.AuthMethod(input.AuthMethod)
	nonInteractive := false
	if input.Action == "prepare" || input.AuthMethod == string(profile.AuthMethodDeviceCode) {
		action, method, nonInteractive = "add", "", true
	}
	_ = http.NewResponseController(response).SetWriteDeadline(time.Time{})
	referencedHome := ""
	if input.IdentityHomeMode == string(profile.HomeOwnershipReferenced) {
		referencedHome = valueOrEmpty(input.ReferencedHomePath)
	}
	result, err := server.profileAuthentication.Authenticate(request.Context(), CommandProfileAuthenticationRequest{
		Action: action, Alias: input.Alias, DisplayName: valueOrEmpty(input.DisplayName), CodexOverride: valueOrEmpty(input.CodexOverride),
		ReferencedHomePath: referencedHome, AuthMethod: method, NonInteractive: nonInteractive,
	}, io.Discard)
	choiceRequired := diagnostics.CodeFor(err, "") == apperrors.ProfileSetupChoiceRequired
	if err != nil && !choiceRequired {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProfileAuthenticationFailed))
		return
	}

	setup := result.Setup
	var item profile.IdentityProfile
	var discovery profile.Discovery
	var stages profile.SetupStages
	warnings := []string{}
	if setup != nil {
		item, discovery, stages = setup.Profile, setup.Discovery, setup.Stages
		warnings = append(warnings, setup.Warnings...)
	} else if result.Reauthentication != nil {
		item, discovery = result.Reauthentication.Profile, result.Reauthentication.Discovery
	}
	projected, projectErr := server.browserProfile(request, item)
	if projectErr != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(projectErr, apperrors.StoreReadFailed))
		return
	}
	outcome := "ready"
	terminalCommand := ""
	if projected.Status == string(profile.StatusPending) {
		outcome = "pending"
	}
	if input.AuthMethod == string(profile.AuthMethodDeviceCode) && projected.Status == string(profile.StatusPending) {
		outcome = "terminal_required"
		terminalCommand = profileTerminalCommand("add", projected.Alias, valueOrEmpty(input.CodexOverride))
	}
	writeJSON(response, http.StatusOK, ProfileAuthenticationResponse{
		Profile: projected, Stages: profileStages(stages), CodexFound: discovery.Version != "", CodexVersion: discovery.Version,
		Outcome: outcome, TerminalCommand: terminalCommand, Warnings: warnings,
	})
}

func profileTerminalCommand(action, alias, codexOverride string) string {
	command := "codex-folio profile " + action + " " + terminalArgument(alias) + " --device-code"
	if codexOverride != "" {
		command += " " + terminalArgument("--codex-bin="+codexOverride)
	}
	return command
}

func terminalArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n\"'`$%&|<>()^!") {
		return value
	}
	if runtime.GOOS == "windows" {
		value = strings.NewReplacer("^", "^^", "%", "%%", `"`, `^"`).Replace(value)
		return `"` + value + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func validBrowserProfileAuthentication(input ProfileAuthenticationRequest) bool {
	if strings.TrimSpace(input.Alias) == "" || (input.Action != "add" && input.Action != "prepare" && input.Action != "reauthenticate") {
		return false
	}
	if input.AuthMethod != string(profile.AuthMethodBrowser) && input.AuthMethod != string(profile.AuthMethodDeviceCode) {
		return false
	}
	if input.Action == "reauthenticate" {
		return true
	}
	if input.IdentityHomeMode != string(profile.HomeOwnershipManaged) && input.IdentityHomeMode != string(profile.HomeOwnershipReferenced) {
		return false
	}
	return true
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (server *Server) profileInventory(request *http.Request) (ProfilesResponse, error) {
	if server.profiles == nil {
		return ProfilesResponse{}, apperrors.New(apperrors.HTTPAPIServiceUnavailable, profile.ErrProfileStateInvalid)
	}
	inventory, err := server.profiles.Inventory(request.Context())
	if err != nil {
		return ProfilesResponse{}, err
	}
	result := ProfilesResponse{Profiles: make([]ProfileSummary, 0, len(inventory.Profiles))}
	for _, item := range inventory.Profiles {
		projected, err := server.browserProfile(request, item)
		if err != nil {
			return ProfilesResponse{}, err
		}
		result.Profiles = append(result.Profiles, projected)
	}
	return result, nil
}

func (server *Server) profileByAlias(request *http.Request, alias string) (ProfileSummary, error) {
	inventory, err := server.profileInventory(request)
	if err != nil {
		return ProfileSummary{}, err
	}
	for _, item := range inventory.Profiles {
		if strings.EqualFold(item.Alias, alias) {
			return item, nil
		}
	}
	return ProfileSummary{}, profile.ErrNotFound
}

func (server *Server) browserProfile(request *http.Request, item profile.IdentityProfile) (ProfileSummary, error) {
	mode := item.IdentityHomeOwnership
	if mode == "" {
		mode = profile.HomeOwnershipManaged
	}
	result := ProfileSummary{
		ProfileId: item.ID, Alias: item.Alias, DisplayName: item.DisplayName, LoginIdentity: item.Email, Workspace: item.Workspace,
		Status: string(item.Status), IdentityHomeMode: string(mode), AuthenticationMethod: string(item.AuthenticationMethod), Selected: item.Selected,
	}
	if server.configurationPacks != nil {
		assignment, err := server.configurationPacks.Assignment(request.Context(), item.Alias)
		if err == nil {
			result.ConfigurationPack = assignment.PackID + " " + assignment.Version
		} else if !errors.Is(err, configpack.ErrNoAssignment) {
			return ProfileSummary{}, err
		}
	}
	if server.usage != nil {
		snapshot, err := server.usage.Latest(request.Context(), item.Alias)
		if err == nil {
			var latest time.Time
			for _, observation := range snapshot.Observations {
				if observation.CapturedAt.After(latest) {
					latest = observation.CapturedAt
				}
			}
			result.LastSuccessfulRefresh = formatUsageTime(latest)
		} else if !errors.Is(err, usage.ErrProfileUnavailable) {
			return ProfileSummary{}, err
		}
	}
	return result, nil
}

func profileStages(stages profile.SetupStages) ProfileSetupStages {
	return ProfileSetupStages{
		Discovery: stages.Discovery, Home: stages.Home, Authentication: stages.Authentication,
		Validation: stages.Validation, Selection: stages.Selection,
	}
}
