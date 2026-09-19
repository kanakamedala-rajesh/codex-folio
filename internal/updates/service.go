// Package updates owns the opt-in update-notification policy and its safe,
// persisted projection. It never downloads, opens, executes, or installs an
// update artifact.
package updates

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	NormalCheckInterval  = 24 * time.Hour
	FailureRetryInterval = time.Hour

	MaxVersionLength           = 64
	MaxReleaseNotesLength      = 8 * 1024
	MaxDownloadURLLength       = 2 * 1024
	MaxInstallerGuidanceLength = 4 * 1024
)

type Status string

const (
	StatusDisabled        Status = "disabled"
	StatusNeverChecked    Status = "never_checked"
	StatusUnconfigured    Status = "unconfigured"
	StatusOffline         Status = "offline"
	StatusMalformed       Status = "malformed"
	StatusUnavailable     Status = "unavailable"
	StatusUpToDate        Status = "up_to_date"
	StatusUpdateAvailable Status = "update_available"
)

const (
	ErrorSourceUnconfigured = "UPDATE_SOURCE_UNCONFIGURED"
	ErrorSourceOffline      = "UPDATE_SOURCE_OFFLINE"
	ErrorSourceMalformed    = "UPDATE_SOURCE_MALFORMED"
	ErrorSourceUnavailable  = "UPDATE_SOURCE_UNAVAILABLE"
)

var (
	ErrInvalid            = errors.New("update request is invalid")
	ErrSourceUnconfigured = errors.New("update source is unconfigured")
	ErrSourceOffline      = errors.New("update source is offline")
	ErrSourceMalformed    = errors.New("update source response is malformed")
	ErrSourceUnavailable  = errors.New("update source is unavailable")
)

type Settings struct {
	AutomaticChecks bool `json:"automatic_checks"`
}

type Evidence struct {
	Version           string `json:"version"`
	ReleaseNotes      string `json:"release_notes"`
	DownloadURL       string `json:"download_url"`
	InstallerGuidance string `json:"installer_guidance"`
}

type CheckState struct {
	Status            Status    `json:"status"`
	CurrentVersion    string    `json:"current_version"`
	AvailableVersion  string    `json:"available_version,omitempty"`
	ReleaseNotes      string    `json:"release_notes,omitempty"`
	DownloadURL       string    `json:"download_url,omitempty"`
	InstallerGuidance string    `json:"installer_guidance,omitempty"`
	CheckedAt         time.Time `json:"checked_at,omitempty"`
	NextCheckAt       time.Time `json:"next_check_at,omitempty"`
	ErrorCode         string    `json:"error_code,omitempty"`
}

type Snapshot struct {
	Settings Settings   `json:"settings"`
	State    CheckState `json:"state"`
}

type Repository interface {
	UpdateSettings(context.Context) (Settings, error)
	SetUpdateSettings(context.Context, Settings) (Settings, error)
	UpdateCheckState(context.Context) (CheckState, error)
	SetUpdateCheckState(context.Context, CheckState) (CheckState, error)
}

type Source interface {
	Configured() bool
	Check(context.Context) (Evidence, error)
}

type Clock interface{ Now() time.Time }

type ServiceOptions struct {
	Repository     Repository
	Source         Source
	Clock          Clock
	CurrentVersion string
}

type Service struct {
	repository     Repository
	source         Source
	clock          Clock
	currentVersion string
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil || options.Source == nil || options.Clock == nil {
		return nil, ErrInvalid
	}
	if err := ValidateVersion(options.CurrentVersion); err != nil {
		return nil, ErrInvalid
	}
	return &Service{repository: options.Repository, source: options.Source, clock: options.Clock, currentVersion: options.CurrentVersion}, nil
}

// Status reads only local state. When automatic checks are off and no explicit
// check evidence exists, disabled is projected without overwriting persisted
// never-checked state.
func (service *Service) Status(ctx context.Context) (Snapshot, error) {
	if !service.valid() {
		return Snapshot{}, ErrInvalid
	}
	settings, err := service.repository.UpdateSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := service.repository.UpdateCheckState(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state = service.currentState(state)
	if err := state.validate(service.currentVersion); err != nil {
		return Snapshot{}, err
	}
	if !settings.AutomaticChecks && state.Status == StatusNeverChecked {
		state.Status = StatusDisabled
	}
	if state.Status == StatusNeverChecked && !service.source.Configured() {
		state.Status = StatusUnconfigured
	}
	return Snapshot{Settings: settings, State: state}, nil
}

func (service *Service) SetAutomatic(ctx context.Context, enabled bool) (Snapshot, error) {
	if !service.valid() {
		return Snapshot{}, ErrInvalid
	}
	if _, err := service.repository.SetUpdateSettings(ctx, Settings{AutomaticChecks: enabled}); err != nil {
		return Snapshot{}, err
	}
	return service.Status(ctx)
}

// Check performs one source attempt. It never changes automatic-check consent.
func (service *Service) Check(ctx context.Context) (Snapshot, error) {
	if !service.valid() {
		return Snapshot{}, ErrInvalid
	}
	settings, err := service.repository.UpdateSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := service.perform(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Settings: settings, State: state}, nil
}

// CheckAutomatic performs no network operation unless the user opted in and
// the persisted schedule is due.
func (service *Service) CheckAutomatic(ctx context.Context) (Snapshot, error) {
	if !service.valid() {
		return Snapshot{}, ErrInvalid
	}
	settings, err := service.repository.UpdateSettings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := service.repository.UpdateCheckState(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state = service.currentState(state)
	if !settings.AutomaticChecks {
		if state.Status == StatusNeverChecked {
			state.Status = StatusDisabled
		}
		return Snapshot{Settings: settings, State: state}, nil
	}
	now := service.clock.Now().UTC()
	if !state.NextCheckAt.IsZero() && now.Before(state.NextCheckAt) {
		return Snapshot{Settings: settings, State: state}, nil
	}
	state, err = service.perform(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Settings: settings, State: state}, nil
}

// currentState invalidates evidence collected by another running version. The
// consent setting remains independent, and reading status never causes network
// activity or lets stale evidence make local workflows unavailable.
func (service *Service) currentState(state CheckState) CheckState {
	if state.CurrentVersion == "" && state.Status == StatusNeverChecked {
		state.CurrentVersion = service.currentVersion
		return state
	}
	if state.CurrentVersion != service.currentVersion {
		return CheckState{Status: StatusNeverChecked, CurrentVersion: service.currentVersion}
	}
	return state
}

func (service *Service) perform(ctx context.Context) (CheckState, error) {
	evidence, sourceErr := service.source.Check(ctx)
	if sourceErr != nil && (errors.Is(sourceErr, context.Canceled) || errors.Is(sourceErr, context.DeadlineExceeded)) {
		return CheckState{}, sourceErr
	}
	now := service.clock.Now().UTC()
	if now.IsZero() {
		return CheckState{}, ErrInvalid
	}
	state := CheckState{CurrentVersion: service.currentVersion, CheckedAt: now}
	if sourceErr != nil {
		state.Status, state.ErrorCode = classifySourceError(sourceErr)
		state.NextCheckAt = now.Add(FailureRetryInterval)
	} else {
		if err := ValidateEvidence(evidence); err != nil {
			state.Status = StatusMalformed
			state.ErrorCode = ErrorSourceMalformed
			state.NextCheckAt = now.Add(FailureRetryInterval)
		} else {
			comparison, err := CompareVersions(evidence.Version, service.currentVersion)
			if err != nil {
				state.Status = StatusMalformed
				state.ErrorCode = ErrorSourceMalformed
				state.NextCheckAt = now.Add(FailureRetryInterval)
			} else {
				state.Status = StatusUpToDate
				if comparison > 0 {
					state.Status = StatusUpdateAvailable
					state.AvailableVersion = evidence.Version
					state.ReleaseNotes = evidence.ReleaseNotes
					state.DownloadURL = evidence.DownloadURL
					state.InstallerGuidance = evidence.InstallerGuidance
				}
				state.NextCheckAt = now.Add(NormalCheckInterval)
			}
		}
	}
	if err := state.validate(service.currentVersion); err != nil {
		return CheckState{}, err
	}
	return service.repository.SetUpdateCheckState(ctx, state)
}

func (service *Service) valid() bool {
	return service != nil && service.repository != nil && service.source != nil && service.clock != nil
}

func classifySourceError(err error) (Status, string) {
	switch {
	case errors.Is(err, ErrSourceUnconfigured):
		return StatusUnconfigured, ErrorSourceUnconfigured
	case errors.Is(err, ErrSourceOffline):
		return StatusOffline, ErrorSourceOffline
	case errors.Is(err, ErrSourceMalformed):
		return StatusMalformed, ErrorSourceMalformed
	default:
		return StatusUnavailable, ErrorSourceUnavailable
	}
}

func (state CheckState) validate(currentVersion string) error {
	if err := ValidateVersion(currentVersion); err != nil || state.CurrentVersion != currentVersion || !validStatus(state.Status) {
		return ErrInvalid
	}
	if state.Status == StatusNeverChecked {
		if !state.CheckedAt.IsZero() || !state.NextCheckAt.IsZero() || state.ErrorCode != "" || state.AvailableVersion != "" || state.ReleaseNotes != "" || state.DownloadURL != "" || state.InstallerGuidance != "" {
			return ErrInvalid
		}
		return nil
	}
	if state.Status == StatusDisabled {
		return ErrInvalid // projected only; never persisted
	}
	if state.CheckedAt.IsZero() || state.NextCheckAt.Before(state.CheckedAt) {
		return ErrInvalid
	}
	if state.Status == StatusUpdateAvailable {
		return ValidateEvidence(Evidence{Version: state.AvailableVersion, ReleaseNotes: state.ReleaseNotes, DownloadURL: state.DownloadURL, InstallerGuidance: state.InstallerGuidance})
	}
	if state.AvailableVersion != "" || state.ReleaseNotes != "" || state.DownloadURL != "" || state.InstallerGuidance != "" {
		return ErrInvalid
	}
	wantCode := map[Status]string{StatusUnconfigured: ErrorSourceUnconfigured, StatusOffline: ErrorSourceOffline, StatusMalformed: ErrorSourceMalformed, StatusUnavailable: ErrorSourceUnavailable}[state.Status]
	if state.ErrorCode != wantCode {
		return ErrInvalid
	}
	return nil
}

func ValidateCheckState(state CheckState) error {
	currentVersion := state.CurrentVersion
	if currentVersion == "" && state.Status == StatusNeverChecked {
		return nil
	}
	return state.validate(currentVersion)
}

func validStatus(status Status) bool {
	switch status {
	case StatusDisabled, StatusNeverChecked, StatusUnconfigured, StatusOffline, StatusMalformed, StatusUnavailable, StatusUpToDate, StatusUpdateAvailable:
		return true
	default:
		return false
	}
}

func ValidateEvidence(evidence Evidence) error {
	if err := ValidateVersion(evidence.Version); err != nil || !boundedText(evidence.ReleaseNotes, MaxReleaseNotesLength) || !boundedText(evidence.InstallerGuidance, MaxInstallerGuidanceLength) {
		return ErrInvalid
	}
	if len(evidence.DownloadURL) == 0 || len(evidence.DownloadURL) > MaxDownloadURLLength || strings.TrimSpace(evidence.DownloadURL) != evidence.DownloadURL {
		return ErrInvalid
	}
	download, err := url.Parse(evidence.DownloadURL)
	if err != nil || download.Scheme != "https" || download.Host == "" || download.User != nil || download.Fragment != "" {
		return ErrInvalid
	}
	return nil
}

func boundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func ValidateVersion(version string) error {
	if len(version) == 0 || len(version) > MaxVersionLength || strings.TrimSpace(version) != version {
		return ErrInvalid
	}
	_, err := parseVersion(version)
	return err
}

func CompareVersions(left, right string) (int, error) {
	l, err := parseVersion(left)
	if err != nil {
		return 0, err
	}
	r, err := parseVersion(right)
	if err != nil {
		return 0, err
	}
	for index := range l.core {
		if l.core[index] < r.core[index] {
			return -1, nil
		}
		if l.core[index] > r.core[index] {
			return 1, nil
		}
	}
	if len(l.pre) == 0 && len(r.pre) > 0 {
		return 1, nil
	}
	if len(l.pre) > 0 && len(r.pre) == 0 {
		return -1, nil
	}
	for index := 0; index < len(l.pre) && index < len(r.pre); index++ {
		if l.pre[index] == r.pre[index] {
			continue
		}
		ln, le := strconv.ParseUint(l.pre[index], 10, 64)
		rn, re := strconv.ParseUint(r.pre[index], 10, 64)
		switch {
		case le == nil && re == nil:
			if ln < rn {
				return -1, nil
			}
			return 1, nil
		case le == nil:
			return -1, nil
		case re == nil:
			return 1, nil
		case l.pre[index] < r.pre[index]:
			return -1, nil
		default:
			return 1, nil
		}
	}
	if len(l.pre) < len(r.pre) {
		return -1, nil
	}
	if len(l.pre) > len(r.pre) {
		return 1, nil
	}
	return 0, nil
}

type semanticVersion struct {
	core [3]uint64
	pre  []string
}

func parseVersion(version string) (semanticVersion, error) {
	if len(version) == 0 || len(version) > MaxVersionLength || strings.TrimSpace(version) != version {
		return semanticVersion{}, ErrInvalid
	}
	withoutBuild := strings.SplitN(version, "+", 2)
	if len(withoutBuild) == 2 && !validIdentifiers(withoutBuild[1], false) {
		return semanticVersion{}, ErrInvalid
	}
	mainAndPre := strings.SplitN(withoutBuild[0], "-", 2)
	parts := strings.Split(mainAndPre[0], ".")
	if len(parts) != 3 {
		return semanticVersion{}, ErrInvalid
	}
	parsed := semanticVersion{}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semanticVersion{}, ErrInvalid
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semanticVersion{}, ErrInvalid
		}
		parsed.core[index] = value
	}
	if len(mainAndPre) == 2 {
		if !validIdentifiers(mainAndPre[1], true) {
			return semanticVersion{}, ErrInvalid
		}
		parsed.pre = strings.Split(mainAndPre[1], ".")
	}
	return parsed, nil
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if part == "" || (rejectNumericLeadingZero && len(part) > 1 && part[0] == '0' && allDigits(part)) {
			return false
		}
		for _, character := range part {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (state CheckState) String() string {
	return fmt.Sprintf("%s at %s", state.Status, state.CheckedAt.UTC().Format(time.RFC3339))
}
