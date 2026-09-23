package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

func (server *Server) browserProfileLifecycle(response http.ResponseWriter, request *http.Request) {
	if server.profileLifecycle == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		records, err := server.profileLifecycle.ListQuarantined(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		result := ProfileLifecycleListResponse{Quarantined: make([]ProfileLifecycleRecord, 0, len(records))}
		for _, record := range records {
			projected, err := server.browserLifecycleRecord(request, record)
			if err != nil {
				server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
				return
			}
			result.Quarantined = append(result.Quarantined, projected)
		}
		writeJSON(response, http.StatusOK, result)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	var input ProfileLifecycleRequest
	if !server.decodeProfileRequest(response, request, &input) {
		return
	}
	if strings.TrimSpace(input.Alias) == "" {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProfileSetupInvalid)
		return
	}
	var record profile.RemovalRecord
	var err error
	switch input.Action {
	case "preview":
		record, err = server.profileLifecycle.PreviewRemoval(request.Context(), input.Alias)
	case "remove":
		record, err = server.profileLifecycle.PreviewRemoval(request.Context(), input.Alias)
		if err == nil && valueOrEmpty(input.Confirmation) != record.Profile.Alias {
			err = apperrors.New(apperrors.ProfileConfirmationInvalid, errors.New("profile confirmation did not match the exact CLI Alias"))
		}
		if err == nil {
			record, err = server.profileLifecycle.Remove(request.Context(), input.Alias, valueOrEmpty(input.Replacement))
		}
	case "restore":
		record, err = server.profileLifecycle.Restore(request.Context(), input.Alias)
	case "purge":
		record, err = server.profileLifecycle.PreviewQuarantined(request.Context(), input.Alias)
		if err == nil && valueOrEmpty(input.Confirmation) != record.Profile.Alias {
			err = apperrors.New(apperrors.ProfileConfirmationInvalid, errors.New("profile confirmation did not match the exact CLI Alias"))
		}
		if err == nil {
			record, err = server.profileLifecycle.Purge(request.Context(), input.Alias)
		}
	default:
		err = profile.ErrProfileStateInvalid
	}
	if err != nil {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProfileQuarantineInvalid))
		return
	}
	projected, err := server.browserLifecycleRecord(request, record)
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	writeJSON(response, http.StatusOK, projected)
}

func (server *Server) browserLifecycleRecord(request *http.Request, record profile.RemovalRecord) (ProfileLifecycleRecord, error) {
	projected := browserProfileSummary(record.Profile)
	if record.Action == profile.RemovalRestored {
		var err error
		projected, err = server.browserProfile(request, record.Profile)
		if err != nil {
			return ProfileLifecycleRecord{}, err
		}
	}
	return ProfileLifecycleRecord{
		Profile: projected, Action: string(record.Action), State: string(record.State),
		QuarantinedAt: lifecycleTime(record.QuarantinedAt), PurgeAfter: lifecycleTime(record.PurgeAfter),
		RemoteIdentityAffected: record.RemoteIdentityAffected,
	}, nil
}

func lifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

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
	if input.Action == "reauthenticate" {
		if server.getProfileOperation(input.Alias).State == "running" {
			server.writeAPIError(response, http.StatusConflict, apperrors.ProfileSetupInvalid)
			return
		}
		item, err := server.profileByAlias(request, input.Alias)
		if err != nil {
			server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProfileNotSelectable))
			return
		}
		if item.Status == string(profile.StatusPending) {
			server.writeAPIError(response, http.StatusConflict, apperrors.ProfileNotSelectable)
			return
		}
		server.setProfileOperation(item.Alias, profileOperation{State: "waiting"})
		writeJSON(response, http.StatusOK, ProfileAuthenticationResponse{
			Profile: item, Warnings: []string{}, Outcome: "terminal_required",
			TerminalCommand: server.profileTerminalCommand("reauthenticate", item.Alias, input.AuthMethod, valueOrEmpty(input.CodexOverride)),
		})
		return
	}

	server.profileOperationMu.Lock()
	if server.profileOperations == nil {
		server.profileOperations = make(map[string]profileOperation)
	}
	key := strings.ToLower(input.Alias)
	previous := server.profileOperations[key]
	if previous.State != "" {
		server.profileOperationMu.Unlock()
		item, err := server.profileByAlias(request, input.Alias)
		if errors.Is(err, profile.ErrNotFound) && previous.State == "failed" {
			server.profileOperationMu.Lock()
			if server.profileOperations[key] != previous {
				server.profileOperationMu.Unlock()
				server.writeAPIError(response, http.StatusConflict, apperrors.ProfileSetupInvalid)
				return
			}
			server.profileOperations[key] = profileOperation{State: "running"}
			server.profileOperationMu.Unlock()
			previous = profileOperation{}
		} else {
			if err != nil {
				server.writeAPIError(response, http.StatusConflict, apperrors.ProfileSetupInvalid)
				return
			}
			outcome := "terminal_required"
			terminalCommand := server.profileTerminalCommand("add", item.Alias, input.AuthMethod, valueOrEmpty(input.CodexOverride))
			if previous.State == "ready" && item.Status == string(profile.StatusReady) {
				outcome, terminalCommand = "ready", ""
			}
			writeJSON(response, http.StatusOK, ProfileAuthenticationResponse{
				Profile: item, Warnings: []string{}, Outcome: outcome, TerminalCommand: terminalCommand,
			})
			return
		}
	} else {
		server.profileOperations[key] = profileOperation{State: "running"}
		server.profileOperationMu.Unlock()
	}
	prepared := false
	defer func() {
		if !prepared {
			server.setProfileOperation(input.Alias, previous)
		}
	}()
	referencedHome := ""
	if input.IdentityHomeMode == string(profile.HomeOwnershipReferenced) {
		referencedHome = valueOrEmpty(input.ReferencedHomePath)
	}
	result, err := server.profileAuthentication.Authenticate(request.Context(), CommandProfileAuthenticationRequest{
		Action: "add", Alias: input.Alias, DisplayName: valueOrEmpty(input.DisplayName), CodexOverride: valueOrEmpty(input.CodexOverride),
		ReferencedHomePath: referencedHome, AuthMethod: "", NonInteractive: true,
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
		if item.Status == profile.StatusReady {
			stages = profile.SetupStages{Discovery: true, Home: item.IdentityHomeID != "", Authentication: true, Validation: true, Selection: item.Selected}
		}
	}
	projected, projectErr := server.browserProfile(request, item)
	if projectErr != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(projectErr, apperrors.StoreReadFailed))
		return
	}
	outcome := "ready"
	terminalCommand := ""
	if projected.Status == string(profile.StatusPending) {
		outcome = "terminal_required"
		terminalCommand = server.profileTerminalCommand("add", projected.Alias, input.AuthMethod, valueOrEmpty(input.CodexOverride))
		server.setProfileOperation(projected.Alias, profileOperation{State: "waiting"})
	} else {
		server.setProfileOperation(projected.Alias, profileOperation{})
	}
	prepared = true
	writeJSON(response, http.StatusOK, ProfileAuthenticationResponse{
		Profile: projected, Stages: profileStages(stages), CodexFound: discovery.Version != "", CodexVersion: discovery.Version,
		Outcome: outcome, TerminalCommand: terminalCommand, Warnings: warnings,
	})
}

func (server *Server) profileTerminalCommand(action, alias, method, codexOverride string) string {
	arguments := []string{"profile", action, alias, "--" + method}
	if codexOverride != "" {
		arguments = append(arguments, "--codex-bin="+codexOverride)
	}
	return server.terminalCommand(arguments...)
}

func terminalArgumentForOS(goos, value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n\"'`$%&|<>()^!") {
		return value
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func terminalCommandForOS(goos string, arguments []string, invoke bool) string {
	parts := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		parts = append(parts, terminalArgumentForOS(goos, argument))
	}
	command := strings.Join(parts, " ")
	if goos == "windows" && invoke {
		return "& " + command
	}
	return command
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
		operation := server.getProfileOperation(item.Alias)
		if operation.State != "" {
			projected.SetupOperation = &operation.State
		}
		if operation.Code != "" {
			projected.SetupErrorCode = &operation.Code
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
	result := browserProfileSummary(item)
	if server.configurationPacks != nil {
		assignment, err := server.configurationPacks.Assignment(request.Context(), item.Alias)
		if err == nil {
			result.ConfigurationPack = assignment.PackID + " " + assignment.Version
		} else if !errors.Is(err, configpack.ErrNoAssignment) {
			return ProfileSummary{}, err
		}
	}
	if server.usage != nil {
		latest, err := server.usage.LatestSuccessfulRefresh(request.Context(), usage.ProfileTarget{ID: item.ID, Alias: item.Alias})
		if err == nil {
			result.LastSuccessfulRefresh = formatUsageTime(latest)
		} else if !errors.Is(err, usage.ErrProfileUnavailable) && !errors.Is(err, usage.ErrSourceInvalid) {
			return ProfileSummary{}, err
		}
	}
	return result, nil
}

func browserProfileSummary(item profile.IdentityProfile) ProfileSummary {
	mode := item.IdentityHomeOwnership
	if mode == "" {
		mode = profile.HomeOwnershipManaged
	}
	return ProfileSummary{
		ProfileId: item.ID, Alias: item.Alias, DisplayName: item.DisplayName, LoginIdentity: item.Email, Workspace: item.Workspace,
		Status: string(item.Status), IdentityHomeMode: string(mode), AuthenticationMethod: string(item.AuthenticationMethod), Selected: item.Selected,
	}
}

func profileStages(stages profile.SetupStages) ProfileSetupStages {
	return ProfileSetupStages{
		Discovery: stages.Discovery, Home: stages.Home, Authentication: stages.Authentication,
		Validation: stages.Validation, Selection: stages.Selection,
	}
}
