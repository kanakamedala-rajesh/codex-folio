// Package configbundle owns the strictly allowlisted portable configuration format.
package configbundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion     = 1
	MaxBundleBytes    = 10 << 20
	MaxProfiles       = 128
	MaxPacks          = 128
	MaxThresholds     = 256
	MaxProjectAliases = 256
	MaxString         = 256

	ResolutionSkip        = "skip"
	ResolutionKeepLocal   = "keep_local"
	ResolutionUseImported = "use_imported"
)

var (
	ErrInvalid        = errors.New("portable configuration is invalid")
	ErrReviewRequired = errors.New("portable configuration import requires exact preview confirmation")
	ErrConflict       = errors.New("portable configuration conflicts require explicit resolution")
	ErrStalePreview   = errors.New("portable configuration preview is stale")
)

var ExcludedFields = []string{
	"authentication", "vault_keys", "identity_homes", "canonical_paths", "telemetry_identity", "telemetry_consent",
	"service_enrollment", "automatic_update_checks", "notification_detail", "experiments", "histories", "raw_content",
	"sessions", "credentials", "configuration_pack_assignments", "configuration_pack_overrides",
}

type Bundle struct {
	SchemaVersion          int            `json:"schema_version"`
	Profiles               []Profile      `json:"profiles"`
	ConfigurationPacks     []Pack         `json:"configuration_packs"`
	AlertThresholds        []Threshold    `json:"alert_thresholds"`
	ProjectAliases         []ProjectAlias `json:"project_aliases,omitempty"`
	OperationalPreferences Preferences    `json:"operational_preferences"`
}

type Profile struct {
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
}
type Pack struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Digest  string            `json:"digest"`
	Files   map[string]string `json:"files"`
}
type Threshold struct {
	ProfileAlias    string  `json:"profile_alias"`
	MetricKey       string  `json:"metric_key"`
	WarningPercent  float64 `json:"warning_percent"`
	CriticalPercent float64 `json:"critical_percent"`
}
type ProjectAlias struct {
	RepositoryBasename string `json:"repository_basename"`
	Alias              string `json:"alias"`
}
type Preferences struct {
	CollectionActiveSeconds int64  `json:"collection_active_seconds"`
	CollectionIdleSeconds   int64  `json:"collection_idle_seconds"`
	Appearance              string `json:"appearance"`
}

type Counts struct {
	Profiles               int `json:"profiles"`
	ConfigurationPacks     int `json:"configuration_packs"`
	AlertThresholds        int `json:"alert_thresholds"`
	ProjectAliases         int `json:"project_aliases"`
	OperationalPreferences int `json:"operational_preferences"`
}
type Conflict struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Detail      string   `json:"detail"`
	Resolutions []string `json:"resolutions"`
}
type Preview struct {
	Direction          string     `json:"direction"`
	SchemaVersion      int        `json:"schema_version"`
	Fields             []string   `json:"fields"`
	Counts             Counts     `json:"counts"`
	ExcludedFields     []string   `json:"excluded_fields"`
	Conflicts          []Conflict `json:"conflicts"`
	ConfirmationDigest string     `json:"confirmation_digest"`
	Bundle             *Bundle    `json:"bundle,omitempty"`
}
type ApplyRequest struct {
	ConfirmationDigest string            `json:"confirmation_digest"`
	Reviewed           bool              `json:"reviewed"`
	Resolutions        map[string]string `json:"resolutions"`
}
type ApplyResult struct {
	Applied bool     `json:"applied"`
	Counts  Counts   `json:"counts"`
	Skipped []string `json:"skipped"`
}

type Repository interface {
	ExportConfiguration(context.Context, bool) (Bundle, error)
	PreviewConfigurationImport(context.Context, Bundle) (Preview, error)
	ApplyConfigurationImport(context.Context, Bundle, ApplyRequest) (ApplyResult, error)
	SetConfigurationAppearance(context.Context, string) error
}

type Service struct{ repository Repository }

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository}, nil
}

func (s *Service) ExportPreview(ctx context.Context, includeProjectAliases bool) (Preview, error) {
	b, err := s.repository.ExportConfiguration(ctx, includeProjectAliases)
	if err != nil {
		return Preview{}, err
	}
	if err := Validate(b); err != nil {
		return Preview{}, err
	}
	digest, err := Digest(b)
	if err != nil {
		return Preview{}, err
	}
	excluded := append([]string(nil), ExcludedFields...)
	if !includeProjectAliases {
		excluded = append(excluded, "project_aliases")
	}
	return Preview{Direction: "export", SchemaVersion: SchemaVersion, Fields: Fields(b), Counts: Count(b), ExcludedFields: excluded, Conflicts: []Conflict{}, ConfirmationDigest: digest, Bundle: &b}, nil
}
func (s *Service) ImportPreview(ctx context.Context, source []byte) (Preview, Bundle, error) {
	b, err := Decode(source)
	if err != nil {
		return Preview{}, Bundle{}, err
	}
	p, err := s.repository.PreviewConfigurationImport(ctx, b)
	return p, b, err
}
func (s *Service) ImportApply(ctx context.Context, source []byte, request ApplyRequest) (ApplyResult, error) {
	b, err := Decode(source)
	if err != nil {
		return ApplyResult{}, err
	}
	if !request.Reviewed || len(request.ConfirmationDigest) != 64 {
		return ApplyResult{}, ErrReviewRequired
	}
	return s.repository.ApplyConfigurationImport(ctx, b, request)
}
func (s *Service) SetAppearance(ctx context.Context, appearance string) (Preview, error) {
	if appearance != "system" && appearance != "light" && appearance != "dark" {
		return Preview{}, ErrInvalid
	}
	if err := s.repository.SetConfigurationAppearance(ctx, appearance); err != nil {
		return Preview{}, err
	}
	return s.ExportPreview(ctx, false)
}

func Fields(b Bundle) []string {
	fields := []string{"profiles", "configuration_packs", "alert_thresholds", "operational_preferences.collection_intervals", "operational_preferences.appearance"}
	if len(b.ProjectAliases) > 0 {
		fields = append(fields, "project_aliases")
	}
	return fields
}
func Count(b Bundle) Counts {
	n := 0
	if b.OperationalPreferences.CollectionActiveSeconds != 0 || b.OperationalPreferences.CollectionIdleSeconds != 0 {
		n = 1
	}
	return Counts{Profiles: len(b.Profiles), ConfigurationPacks: len(b.ConfigurationPacks), AlertThresholds: len(b.AlertThresholds), ProjectAliases: len(b.ProjectAliases), OperationalPreferences: n}
}
func Decode(source []byte) (Bundle, error) {
	if len(source) == 0 || len(source) > MaxBundleBytes {
		return Bundle{}, ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(source))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, errors.Join(ErrInvalid, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Bundle{}, ErrInvalid
	}
	if err := Validate(b); err != nil {
		return Bundle{}, err
	}
	return b, nil
}
func Validate(b Bundle) error {
	if b.SchemaVersion != SchemaVersion || len(b.Profiles) > MaxProfiles || len(b.ConfigurationPacks) > MaxPacks || len(b.AlertThresholds) > MaxThresholds || len(b.ProjectAliases) > MaxProjectAliases {
		return ErrInvalid
	}
	profiles := map[string]bool{}
	packs := map[string]bool{}
	thresholds := map[string]bool{}
	projects := map[string]bool{}
	for _, p := range b.Profiles {
		key := strings.ToLower(strings.TrimSpace(p.Alias))
		if !validString(p.Alias) || !validString(p.DisplayName) || utf8.RuneCountInString(p.DisplayName) > 128 || profiles[key] {
			return ErrInvalid
		}
		profiles[key] = true
	}
	for _, p := range b.ConfigurationPacks {
		key := p.ID + "\x00" + p.Version
		if !validString(p.ID) || !validString(p.Version) || len(p.Digest) != 64 || len(p.Files) == 0 || len(p.Files) > 256 || packs[key] {
			return ErrInvalid
		}
		packs[key] = true
		for path, content := range p.Files {
			if !validString(path) || len(content) > 1<<20 {
				return ErrInvalid
			}
		}
	}
	for _, t := range b.AlertThresholds {
		key := strings.ToLower(t.ProfileAlias) + "\x00" + t.MetricKey
		if !validString(t.ProfileAlias) || !profiles[strings.ToLower(t.ProfileAlias)] || (t.MetricKey != "codex.primary.used_percent" && t.MetricKey != "codex.secondary.used_percent") || t.CriticalPercent < 0 || t.WarningPercent <= t.CriticalPercent || t.WarningPercent > 100 || thresholds[key] {
			return ErrInvalid
		}
		thresholds[key] = true
	}
	for _, project := range b.ProjectAliases {
		key := strings.ToLower(project.RepositoryBasename)
		if !validString(project.RepositoryBasename) || !validProjectAlias(project.Alias) || projects[key] {
			return ErrInvalid
		}
		projects[key] = true
	}
	if b.OperationalPreferences.CollectionActiveSeconds < 300 || b.OperationalPreferences.CollectionActiveSeconds > 86400 || b.OperationalPreferences.CollectionIdleSeconds < 300 || b.OperationalPreferences.CollectionIdleSeconds > 86400 {
		return ErrInvalid
	}
	if b.OperationalPreferences.Appearance != "system" && b.OperationalPreferences.Appearance != "light" && b.OperationalPreferences.Appearance != "dark" {
		return ErrInvalid
	}
	return nil
}
func validString(v string) bool { return strings.TrimSpace(v) == v && v != "" && len(v) <= MaxString }
func validProjectAlias(v string) bool {
	return strings.TrimSpace(v) == v && v != "" && len(v) <= 120 && strings.IndexFunc(v, unicode.IsControl) < 0
}
func CanonicalJSON(value any) ([]byte, error) { return json.Marshal(value) }
func Digest(value any) (string, error) {
	encoded, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	d := sha256.Sum256(encoded)
	return hex.EncodeToString(d[:]), nil
}
func Sort(b *Bundle) {
	sort.Slice(b.Profiles, func(i, j int) bool {
		return strings.ToLower(b.Profiles[i].Alias) < strings.ToLower(b.Profiles[j].Alias)
	})
	sort.Slice(b.ConfigurationPacks, func(i, j int) bool {
		if b.ConfigurationPacks[i].ID == b.ConfigurationPacks[j].ID {
			return b.ConfigurationPacks[i].Version < b.ConfigurationPacks[j].Version
		}
		return b.ConfigurationPacks[i].ID < b.ConfigurationPacks[j].ID
	})
	sort.Slice(b.AlertThresholds, func(i, j int) bool {
		if b.AlertThresholds[i].ProfileAlias == b.AlertThresholds[j].ProfileAlias {
			return b.AlertThresholds[i].MetricKey < b.AlertThresholds[j].MetricKey
		}
		return b.AlertThresholds[i].ProfileAlias < b.AlertThresholds[j].ProfileAlias
	})
	sort.Slice(b.ProjectAliases, func(i, j int) bool {
		return strings.ToLower(b.ProjectAliases[i].RepositoryBasename) < strings.ToLower(b.ProjectAliases[j].RepositoryBasename)
	})
}
