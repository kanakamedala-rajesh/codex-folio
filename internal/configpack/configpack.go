// Package configpack owns reviewed, immutable configuration intent shared by
// isolated Identity Profiles. It never interprets Codex configuration.
package configpack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	StateDraft      State = "draft"
	StateApproved   State = "approved"
	StateSuperseded State = "superseded"

	TargetStatusReady          = "ready"
	TargetHomeOwnershipManaged = "managed"

	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeRemoved  ChangeKind = "removed"

	maxPackID      = 64
	maxPackVersion = 64
	maxFiles       = 256
	maxPath        = 256
	maxFileBytes   = 1 << 20
	maxPackBytes   = 8 << 20
)

var tomlAssignment = regexp.MustCompile(`(?m)(^|[,{}])[[:space:]]*["']?([A-Za-z0-9_.-]+)["']?[[:space:]]*=`)

var (
	ErrInvalid               = errors.New("configuration pack is invalid")
	ErrNotFound              = errors.New("configuration pack was not found")
	ErrNotApproved           = errors.New("configuration pack is not approved")
	ErrAssignmentInvalid     = errors.New("configuration pack assignment is invalid")
	ErrNoAssignment          = errors.New("profile has no configuration pack assignment")
	ErrProjectionFailed      = errors.New("configuration pack projection failed")
	ErrPromotionReviewNeeded = errors.New("configuration pack promotion requires explicit review")
)

type State string

type ChangeKind string

type Pack struct {
	ID        string            `json:"id"`
	Version   string            `json:"version"`
	State     State             `json:"state"`
	Digest    string            `json:"digest"`
	Files     map[string]string `json:"files"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
}

type Assignment struct {
	ProfileID string `json:"profile_id"`
	Alias     string `json:"alias"`
	PackID    string `json:"pack_id"`
	Version   string `json:"version"`
	Digest    string `json:"digest,omitempty"`
}

type ProfileTarget struct {
	ID            string
	Alias         string
	Status        string
	HomeOwnership string
	IdentityHome  string
}

type Change struct {
	Path string     `json:"path"`
	Kind ChangeKind `json:"kind"`
}

type PromotionPreview struct {
	ProfileAlias string   `json:"profile_alias"`
	PackID       string   `json:"pack_id"`
	FromVersion  string   `json:"from_version"`
	ToVersion    string   `json:"to_version"`
	Changes      []Change `json:"changes"`
	Digest       string   `json:"digest"`
}

type ProjectionPlan struct {
	Assignment Assignment `json:"assignment"`
	Digest     string     `json:"digest"`
	Files      []string   `json:"files"`
}

type ProjectionResult struct {
	PackID  string   `json:"pack_id"`
	Version string   `json:"version"`
	Digest  string   `json:"digest"`
	Files   []string `json:"files"`
}

type Repository interface {
	CreateConfigurationPack(context.Context, Pack) error
	GetConfigurationPack(context.Context, string, string) (Pack, error)
	ApproveConfigurationPack(context.Context, string, string) (Pack, error)
	PromoteConfigurationPack(context.Context, Pack, string) error
	AssignConfigurationPack(context.Context, string, string, string) (Assignment, error)
	GetConfigurationPackAssignment(context.Context, string) (Assignment, error)
	GetConfigurationOverrides(context.Context, string) (map[string]string, error)
	SetConfigurationOverride(context.Context, string, string, string) error
	GetConfigurationProfile(context.Context, string) (ProfileTarget, error)
}

type Projector interface {
	Project(context.Context, string, map[string]string) (ProjectionResult, error)
}

type Service struct {
	repository Repository
	projector  Projector
}

func NewService(repository Repository, projector Projector) (*Service, error) {
	if repository == nil {
		return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	return &Service{repository: repository, projector: projector}, nil
}

func NewDraft(id, version string, files map[string]string) (Pack, error) {
	pack := Pack{ID: id, Version: version, State: StateDraft, Files: cloneFiles(files)}
	if err := pack.validate(false); err != nil {
		return Pack{}, err
	}
	pack.Digest = DigestFiles(pack.Files)
	return pack, nil
}

func (pack Pack) Approve() (Pack, error) {
	if err := pack.validate(true); err != nil {
		return Pack{}, err
	}
	if pack.State != StateDraft {
		return Pack{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	pack.Files = cloneFiles(pack.Files)
	pack.State = StateApproved
	return pack, nil
}

// Validate verifies the persisted form of a pack, including its digest.
func (pack Pack) Validate() error { return pack.validate(true) }

// ValidateFiles verifies a reviewed pack or local override file set.
func ValidateFiles(files map[string]string) error { return validateFiles(files) }

func (pack Pack) validate(requireDigest bool) error {
	if !validIdentifier(pack.ID, maxPackID) || !validIdentifier(pack.Version, maxPackVersion) {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	if pack.State != StateDraft && pack.State != StateApproved && pack.State != StateSuperseded {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	if err := validateFiles(pack.Files); err != nil {
		return err
	}
	if requireDigest || pack.Digest != "" {
		if pack.Digest != DigestFiles(pack.Files) {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
	}
	return nil
}

func MergeFiles(pack Pack, overrides map[string]string) (map[string]string, error) {
	if err := pack.validate(true); err != nil {
		return nil, err
	}
	if len(overrides) > 0 {
		if err := validateFiles(overrides); err != nil {
			return nil, err
		}
	}
	merged := cloneFiles(pack.Files)
	for path, content := range overrides {
		merged[path] = content
	}
	if err := validateFiles(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func PreviewPromotion(pack Pack, overrides map[string]string, version string) (PromotionPreview, error) {
	if err := pack.validate(true); err != nil {
		return PromotionPreview{}, err
	}
	if pack.State != StateApproved {
		return PromotionPreview{}, apperrors.New(apperrors.ConfigurationPackNotApproved, ErrNotApproved)
	}
	if !validIdentifier(version, maxPackVersion) || version == pack.Version {
		return PromotionPreview{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	merged, err := MergeFiles(pack, overrides)
	if err != nil {
		return PromotionPreview{}, err
	}
	return PromotionPreview{
		PackID:      pack.ID,
		FromVersion: pack.Version,
		ToVersion:   version,
		Changes:     changes(pack.Files, merged),
		Digest:      DigestFiles(merged),
	}, nil
}

func Promote(pack Pack, overrides map[string]string, version string, reviewed bool) (Pack, error) {
	if !reviewed {
		return Pack{}, apperrors.New(apperrors.ConfigurationPackPromotionReviewRequired, ErrPromotionReviewNeeded)
	}
	if _, err := PreviewPromotion(pack, overrides, version); err != nil {
		return Pack{}, err
	}
	merged, err := MergeFiles(pack, overrides)
	if err != nil {
		return Pack{}, err
	}
	return Pack{ID: pack.ID, Version: version, State: StateApproved, Digest: DigestFiles(merged), Files: merged}, nil
}

func DigestFiles(files map[string]string) string {
	encoded, err := canonicalFiles(files)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func MarshalFiles(files map[string]string) ([]byte, error) {
	return canonicalFiles(files)
}

func UnmarshalFiles(encoded []byte) (map[string]string, error) {
	var content canonicalContent
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	files := make(map[string]string, len(content.Files))
	for _, file := range content.Files {
		if _, exists := files[file.Path]; exists {
			return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		files[file.Path] = file.Content
	}
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	return files, nil
}

func (service *Service) CreateDraft(ctx context.Context, id, version string, files map[string]string) (Pack, error) {
	pack, err := NewDraft(id, version, files)
	if err != nil {
		return Pack{}, err
	}
	if err := service.repository.CreateConfigurationPack(contextOrBackground(ctx), pack); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

func (service *Service) Approve(ctx context.Context, id, version string) (Pack, error) {
	pack, err := service.repository.GetConfigurationPack(contextOrBackground(ctx), id, version)
	if err != nil {
		return Pack{}, err
	}
	approved, err := pack.Approve()
	if err != nil {
		return Pack{}, err
	}
	if _, err := service.repository.ApproveConfigurationPack(contextOrBackground(ctx), id, version); err != nil {
		return Pack{}, err
	}
	return approved, nil
}

func (service *Service) Assign(ctx context.Context, alias, id, version string) (Assignment, error) {
	ctx = contextOrBackground(ctx)
	pack, err := service.repository.GetConfigurationPack(ctx, id, version)
	if err != nil {
		return Assignment{}, err
	}
	if pack.State != StateApproved {
		return Assignment{}, apperrors.New(apperrors.ConfigurationPackNotApproved, ErrNotApproved)
	}
	target, err := service.repository.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return Assignment{}, err
	}
	if target.Status != TargetStatusReady || target.HomeOwnership != TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" {
		return Assignment{}, apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, ErrAssignmentInvalid)
	}
	return service.repository.AssignConfigurationPack(ctx, alias, id, version)
}

func (service *Service) SetOverride(ctx context.Context, alias, path, content string) error {
	if err := validateFiles(map[string]string{path: content}); err != nil {
		return err
	}
	ctx = contextOrBackground(ctx)
	_, _, merged, err := service.effective(ctx, alias)
	if err != nil {
		return err
	}
	merged[path] = content
	if err := validateFiles(merged); err != nil {
		return err
	}
	return service.repository.SetConfigurationOverride(ctx, alias, path, content)
}

func (service *Service) Preview(ctx context.Context, alias string) (ProjectionPlan, error) {
	ctx = contextOrBackground(ctx)
	assignment, _, merged, err := service.effective(ctx, alias)
	if err != nil {
		return ProjectionPlan{}, err
	}
	return ProjectionPlan{Assignment: assignment, Digest: DigestFiles(merged), Files: sortedPaths(merged)}, nil
}

func (service *Service) Project(ctx context.Context, alias string) (ProjectionResult, error) {
	if service.projector == nil {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackProjectionFailed, ErrProjectionFailed)
	}
	ctx = contextOrBackground(ctx)
	assignment, pack, merged, err := service.effective(ctx, alias)
	if err != nil {
		return ProjectionResult{}, err
	}
	target, err := service.repository.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return ProjectionResult{}, err
	}
	if target.Status != TargetStatusReady || target.HomeOwnership != TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, ErrAssignmentInvalid)
	}
	result, err := service.projector.Project(ctx, target.IdentityHome, merged)
	if err != nil {
		if apperrors.Code(err) == apperrors.ConfigurationPackProjectionFailed {
			return ProjectionResult{}, err
		}
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackProjectionFailed, errors.Join(ErrProjectionFailed, err))
	}
	result.PackID = assignment.PackID
	result.Version = pack.Version
	result.Digest = DigestFiles(merged)
	result.Files = sortedPaths(merged)
	return result, nil
}

func (service *Service) PreviewPromotion(ctx context.Context, alias, version string) (PromotionPreview, error) {
	ctx = contextOrBackground(ctx)
	assignment, pack, _, err := service.effective(ctx, alias)
	if err != nil {
		return PromotionPreview{}, err
	}
	overrides, err := service.repository.GetConfigurationOverrides(ctx, alias)
	if err != nil {
		return PromotionPreview{}, err
	}
	preview, err := PreviewPromotion(pack, overrides, version)
	if err != nil {
		return PromotionPreview{}, err
	}
	preview.ProfileAlias = assignment.Alias
	return preview, nil
}

func (service *Service) Promote(ctx context.Context, alias, version string, reviewed bool) (Pack, error) {
	ctx = contextOrBackground(ctx)
	_, pack, _, err := service.effective(ctx, alias)
	if err != nil {
		return Pack{}, err
	}
	overrides, err := service.repository.GetConfigurationOverrides(ctx, alias)
	if err != nil {
		return Pack{}, err
	}
	next, err := Promote(pack, overrides, version, reviewed)
	if err != nil {
		return Pack{}, err
	}
	if err := service.repository.PromoteConfigurationPack(ctx, next, pack.Version); err != nil {
		return Pack{}, err
	}
	return next, nil
}

func (service *Service) effective(ctx context.Context, alias string) (Assignment, Pack, map[string]string, error) {
	assignment, err := service.repository.GetConfigurationPackAssignment(ctx, alias)
	if err != nil {
		return Assignment{}, Pack{}, nil, err
	}
	pack, err := service.repository.GetConfigurationPack(ctx, assignment.PackID, assignment.Version)
	if err != nil {
		return Assignment{}, Pack{}, nil, err
	}
	if pack.State != StateApproved {
		return Assignment{}, Pack{}, nil, apperrors.New(apperrors.ConfigurationPackNotApproved, ErrNotApproved)
	}
	overrides, err := service.repository.GetConfigurationOverrides(ctx, alias)
	if err != nil {
		return Assignment{}, Pack{}, nil, err
	}
	merged, err := MergeFiles(pack, overrides)
	if err != nil {
		return Assignment{}, Pack{}, nil, err
	}
	return assignment, pack, merged, nil
}

type canonicalContent struct {
	Files []canonicalFile `json:"files"`
}

type canonicalFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func canonicalFiles(files map[string]string) ([]byte, error) {
	if err := validateFiles(files); err != nil {
		return nil, err
	}
	paths := sortedPaths(files)
	content := canonicalContent{Files: make([]canonicalFile, 0, len(paths))}
	for _, path := range paths {
		content.Files = append(content.Files, canonicalFile{Path: path, Content: files[path]})
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	if len(encoded) > maxPackBytes {
		return nil, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	return encoded, nil
}

func validateFiles(files map[string]string) error {
	if len(files) == 0 || len(files) > maxFiles {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	total := 0
	projectionPaths := make(map[string]struct{}, len(files))
	for path, content := range files {
		if err := validatePath(path); err != nil {
			return err
		}
		projectionPath := strings.ToLower(filepath.ToSlash(path))
		if _, exists := projectionPaths[projectionPath]; exists {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		projectionPaths[projectionPath] = struct{}{}
		if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') || len(content) > maxFileBytes {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		if isTOMLPath(path) && (!validTOMLStructure(content) || containsLiteralSecret(content)) {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		total += len(content)
		if total > maxPackBytes {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
	}
	return nil
}

func isTOMLPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".toml") || strings.EqualFold(path, "plugins.lock")
}

func validTOMLStructure(content string) bool {
	var stack []rune
	var quote rune
	escaped, comment := false, false
	runes := []rune(content)
	for _, character := range runes {
		if comment {
			if character == '\n' {
				comment = false
			}
			continue
		}
		if quote != 0 {
			if quote == '"' && character == '\\' && !escaped {
				escaped = true
				continue
			}
			if character == quote && !escaped {
				quote = 0
			}
			escaped = false
			continue
		}
		switch character {
		case '#':
			comment = true
		case '\'', '"':
			quote = character
		case '[', '{':
			stack = append(stack, character)
		case ']', '}':
			if len(stack) == 0 || (character == ']' && stack[len(stack)-1] != '[') || (character == '}' && stack[len(stack)-1] != '{') {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return quote == 0 && len(stack) == 0
}

func validatePath(path string) error {
	if path == "" || path != strings.TrimSpace(path) || len(path) > maxPath || strings.Contains(path, `\`) || strings.HasPrefix(path, "/") || filepath.IsAbs(filepath.FromSlash(path)) {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	parts := strings.Split(path, "/")
	if path == "." || path == ".." {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	allowed := map[string]bool{"config": true, "agents": true, "guidance": true, "rules": true, "skills": true, "mcp": true, "platform": true}
	if len(parts) == 1 && parts[0] != "manifest.toml" && parts[0] != "plugins.lock" && parts[0] != "AGENTS.md" && parts[0] != "config.toml" {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	if len(parts) > 1 && !allowed[strings.ToLower(parts[0])] {
		return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || (path != "plugins.lock" && forbiddenPart(part)) {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
	}
	return nil
}

func forbiddenPart(part string) bool {
	lower := strings.ToLower(part)
	for _, forbidden := range []string{"auth", "credential", "credentials", "oauth", "session", "sessions", "rollout", "rollouts", "history", "histories", "log", "logs", "cache", "caches", "sqlite", "database", "databases", "socket", "sockets", "runtime", "runtimes", "state", "lock"} {
		if lower == forbidden || strings.HasPrefix(lower, forbidden+".") || strings.HasSuffix(lower, "."+forbidden) {
			return true
		}
	}
	return strings.HasSuffix(lower, ".sqlite3") || strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".sock")
}

func containsLiteralSecret(content string) bool {
	for _, match := range tomlAssignment.FindAllStringSubmatch(content, -1) {
		key := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(match[2]))
		if strings.HasSuffix(key, "_env") || strings.HasSuffix(key, "_env_var") || strings.HasSuffix(key, "_variable") || strings.HasSuffix(key, "_ref") || strings.HasSuffix(key, "_reference") || strings.HasSuffix(key, "_name") {
			continue
		}
		for _, part := range strings.Split(key, "_") {
			if part == "pat" || part == "token" || part == "password" || part == "secret" || part == "authorization" || part == "credential" {
				return true
			}
		}
		if strings.Contains(key, "api_key") || strings.Contains(key, "private_key") {
			return true
		}
	}
	return false
}

func changes(before, after map[string]string) []Change {
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	result := make([]Change, 0, len(ordered))
	for _, path := range ordered {
		old, hadOld := before[path]
		current, hadCurrent := after[path]
		switch {
		case !hadOld && hadCurrent:
			result = append(result, Change{Path: path, Kind: ChangeAdded})
		case hadOld && !hadCurrent:
			result = append(result, Change{Path: path, Kind: ChangeRemoved})
		case old != current:
			result = append(result, Change{Path: path, Kind: ChangeModified})
		}
	}
	return result
}

func sortedPaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func cloneFiles(files map[string]string) map[string]string {
	copy := make(map[string]string, len(files))
	for path, content := range files {
		copy[path] = content
	}
	return copy
}

func validIdentifier(value string, max int) bool {
	if value == "" || len(value) > max || value != strings.TrimSpace(value) {
		return false
	}
	for index, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_' || character == '.')) {
			continue
		}
		return false
	}
	return true
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
