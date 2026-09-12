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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
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

var literalSecretText = regexp.MustCompile(`(?im)(?:^|[\s{,])["']?(?:token|password|secret|credential|authorization|api[-_ ]?key|private[-_ ]?key)["']?\s*[:=]\s*["']?[A-Za-z0-9+/_.=-]{4,}|authorization\s*:\s*bearer\s+\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

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
	ActiveLaunch  bool
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
	Conflicts  []Change   `json:"conflicts"`
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
	ListConfigurationPacks(context.Context) ([]Pack, error)
	ApproveConfigurationPack(context.Context, string, string) (Pack, error)
	PromoteConfigurationPack(context.Context, Pack, string) error
	AssignConfigurationPack(context.Context, string, string, string) (Assignment, error)
	GetConfigurationPackAssignment(context.Context, string) (Assignment, error)
	GetConfigurationOverrides(context.Context, string) (map[string]string, error)
	SetConfigurationOverride(context.Context, string, string, string) error
	GetConfigurationProfile(context.Context, string) (ProfileTarget, error)
	WithStoppedConfigurationProfile(context.Context, string, func(ProfileTarget) error) error
}

type Projector interface {
	Preview(context.Context, string, map[string]string) (ProjectionPlan, error)
	Project(context.Context, string, map[string]string) (ProjectionResult, error)
	ProjectReviewed(context.Context, string, map[string]string, []Change) (ProjectionResult, error)
}

type Service struct {
	repository  Repository
	projector   Projector
	operationMu sync.Mutex
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
	service.operationMu.Lock()
	defer service.operationMu.Unlock()

	ctx = contextOrBackground(ctx)
	if err := service.ValidateAssignment(ctx, id, version); err != nil {
		return Assignment{}, err
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

func (service *Service) ValidateAssignment(ctx context.Context, id, version string) error {
	pack, err := service.repository.GetConfigurationPack(contextOrBackground(ctx), id, version)
	if err != nil {
		return err
	}
	if pack.State != StateApproved {
		return apperrors.New(apperrors.ConfigurationPackNotApproved, ErrNotApproved)
	}
	return nil
}

func (service *Service) Assignment(ctx context.Context, alias string) (Assignment, error) {
	return service.repository.GetConfigurationPackAssignment(contextOrBackground(ctx), alias)
}

func (service *Service) Packs(ctx context.Context) ([]Pack, error) {
	packs, err := service.repository.ListConfigurationPacks(contextOrBackground(ctx))
	if err != nil {
		return nil, err
	}
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].ID == packs[j].ID {
			return packs[i].Version < packs[j].Version
		}
		return packs[i].ID < packs[j].ID
	})
	return packs, nil
}

func (service *Service) SetOverride(ctx context.Context, alias, path, content string) error {
	service.operationMu.Lock()
	defer service.operationMu.Unlock()

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
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.preview(ctx, alias)
}

func (service *Service) preview(ctx context.Context, alias string) (ProjectionPlan, error) {
	if service.projector == nil {
		return ProjectionPlan{}, apperrors.New(apperrors.ConfigurationPackProjectionFailed, ErrProjectionFailed)
	}
	ctx = contextOrBackground(ctx)
	assignment, _, merged, err := service.effective(ctx, alias)
	if err != nil {
		return ProjectionPlan{}, err
	}
	target, err := service.repository.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return ProjectionPlan{}, err
	}
	if target.Status != TargetStatusReady || target.HomeOwnership != TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" {
		return ProjectionPlan{}, apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, ErrAssignmentInvalid)
	}
	plan, err := service.projector.Preview(ctx, target.IdentityHome, merged)
	if err != nil {
		return ProjectionPlan{}, err
	}
	plan.Assignment = assignment
	plan.Files = sortedPaths(merged)
	if plan.Conflicts == nil {
		plan.Conflicts = []Change{}
	}
	plan.Digest = projectionReviewDigest(assignment, target, DigestFiles(merged), plan.Conflicts)
	return plan, nil
}

func (service *Service) Project(ctx context.Context, alias string) (ProjectionResult, error) {
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.project(ctx, alias)
}

func (service *Service) project(ctx context.Context, alias string) (ProjectionResult, error) {
	if service.projector == nil {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackProjectionFailed, ErrProjectionFailed)
	}
	ctx = contextOrBackground(ctx)
	assignment, pack, merged, err := service.effective(ctx, alias)
	if err != nil {
		return ProjectionResult{}, err
	}
	var result ProjectionResult
	err = service.repository.WithStoppedConfigurationProfile(ctx, alias, func(target ProfileTarget) error {
		if target.Status != TargetStatusReady || target.HomeOwnership != TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" || target.ActiveLaunch {
			return apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, ErrAssignmentInvalid)
		}
		var projectErr error
		result, projectErr = service.projector.Project(ctx, target.IdentityHome, merged)
		return projectErr
	})
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

// ApplyReviewed projects only the effective configuration and conflict state
// represented by the reviewed preview digest.
func (service *Service) ApplyReviewed(ctx context.Context, alias, expectedDigest string, reviewed bool) (ProjectionResult, error) {
	if !reviewed || strings.TrimSpace(expectedDigest) == "" {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()

	if service.projector == nil {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackProjectionFailed, ErrProjectionFailed)
	}
	ctx = contextOrBackground(ctx)
	assignment, pack, merged, err := service.effective(ctx, alias)
	if err != nil {
		return ProjectionResult{}, err
	}
	var result ProjectionResult
	err = service.repository.WithStoppedConfigurationProfile(ctx, alias, func(target ProfileTarget) error {
		if target.Status != TargetStatusReady || target.HomeOwnership != TargetHomeOwnershipManaged || strings.TrimSpace(target.IdentityHome) == "" || target.ActiveLaunch {
			return apperrors.New(apperrors.ConfigurationPackAssignmentInvalid, ErrAssignmentInvalid)
		}
		plan, previewErr := service.projector.Preview(ctx, target.IdentityHome, merged)
		if previewErr != nil {
			return previewErr
		}
		if projectionReviewDigest(assignment, target, DigestFiles(merged), plan.Conflicts) != expectedDigest {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		result, previewErr = service.projector.ProjectReviewed(ctx, target.IdentityHome, merged, plan.Conflicts)
		return previewErr
	})
	if err != nil {
		if apperrors.Code(err) == apperrors.ConfigurationPackInvalid {
			return ProjectionResult{}, err
		}
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
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.previewPromotion(ctx, alias, version)
}

func (service *Service) previewPromotion(ctx context.Context, alias, version string) (PromotionPreview, error) {
	ctx = contextOrBackground(ctx)
	assignment, pack, merged, err := service.effective(ctx, alias)
	if err != nil {
		return PromotionPreview{}, err
	}
	preview, err := previewPromotionFromMerged(pack, merged, version)
	if err != nil {
		return PromotionPreview{}, err
	}
	preview.ProfileAlias = assignment.Alias
	preview.Digest = promotionReviewDigest(assignment, pack, version, DigestFiles(merged), preview.Changes)
	return preview, nil
}

func (service *Service) Promote(ctx context.Context, alias, version string, reviewed bool) (Pack, error) {
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.promote(ctx, alias, version, reviewed)
}

func (service *Service) promote(ctx context.Context, alias, version string, reviewed bool) (Pack, error) {
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

// PromoteReviewed publishes only the assignment, source version, and local
// overrides represented by the reviewed promotion digest.
func (service *Service) PromoteReviewed(ctx context.Context, alias, version, expectedDigest string, reviewed bool) (Pack, error) {
	if !reviewed || strings.TrimSpace(expectedDigest) == "" {
		return Pack{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()

	ctx = contextOrBackground(ctx)
	assignment, pack, merged, err := service.effective(ctx, alias)
	if err != nil {
		return Pack{}, err
	}
	preview, err := previewPromotionFromMerged(pack, merged, version)
	if err != nil {
		return Pack{}, err
	}
	if promotionReviewDigest(assignment, pack, version, DigestFiles(merged), preview.Changes) != expectedDigest {
		return Pack{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	next := Pack{ID: pack.ID, Version: version, State: StateApproved, Digest: DigestFiles(merged), Files: cloneFiles(merged)}
	if err := service.repository.PromoteConfigurationPack(ctx, next, pack.Version); err != nil {
		return Pack{}, err
	}
	return next, nil
}

func previewPromotionFromMerged(pack Pack, merged map[string]string, version string) (PromotionPreview, error) {
	if err := pack.validate(true); err != nil {
		return PromotionPreview{}, err
	}
	if pack.State != StateApproved {
		return PromotionPreview{}, apperrors.New(apperrors.ConfigurationPackNotApproved, ErrNotApproved)
	}
	if !validIdentifier(version, maxPackVersion) || version == pack.Version {
		return PromotionPreview{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}
	if err := validateFiles(merged); err != nil {
		return PromotionPreview{}, err
	}
	return PromotionPreview{PackID: pack.ID, FromVersion: pack.Version, ToVersion: version, Changes: changes(pack.Files, merged)}, nil
}

func projectionReviewDigest(assignment Assignment, target ProfileTarget, filesDigest string, conflicts []Change) string {
	return reviewedRevisionDigest(struct {
		Assignment  Assignment `json:"assignment"`
		TargetID    string     `json:"target_id"`
		TargetHome  string     `json:"target_home"`
		FilesDigest string     `json:"files_digest"`
		Conflicts   []Change   `json:"conflicts"`
	}{assignment, target.ID, target.IdentityHome, filesDigest, sortedChanges(conflicts)})
}

func promotionReviewDigest(assignment Assignment, pack Pack, version, filesDigest string, changes []Change) string {
	return reviewedRevisionDigest(struct {
		Assignment  Assignment `json:"assignment"`
		PackDigest  string     `json:"pack_digest"`
		ToVersion   string     `json:"to_version"`
		FilesDigest string     `json:"files_digest"`
		Changes     []Change   `json:"changes"`
	}{assignment, pack.Digest, version, filesDigest, sortedChanges(changes)})
}

func reviewedRevisionDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func sortedChanges(input []Change) []Change {
	result := append([]Change(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Path < result[j].Path
	})
	return result
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
		if !supportedPackFile(path) {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		if literalSecretText.MatchString(content) {
			return apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
		}
		if isTOMLPath(path) && !validNonSecretTOML(content) {
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

func supportedPackFile(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return isTOMLPath(path) || extension == ".md" || extension == ".rules"
}

func validNonSecretTOML(content string) bool {
	var document map[string]any
	return toml.Unmarshal([]byte(content), &document) == nil && !containsLiteralSecret(document)
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

func containsLiteralSecret(document map[string]any) bool {
	for name, value := range document {
		key := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(name))
		if strings.HasSuffix(key, "_env") || strings.HasSuffix(key, "_env_var") || strings.HasSuffix(key, "_variable") || strings.HasSuffix(key, "_ref") || strings.HasSuffix(key, "_reference") || strings.HasSuffix(key, "_name") {
			// Reference names are declarative; their values are not credentials.
		} else {
			for _, part := range strings.Split(key, "_") {
				switch part {
				case "pat", "pats", "token", "tokens", "password", "passwords", "secret", "secrets", "authorization", "authorizations", "credential", "credentials", "oauth":
					return true
				}
			}
			if strings.Contains(key, "api_key") || strings.Contains(key, "private_key") {
				return true
			}
		}
		if nested, ok := value.(map[string]any); ok && containsLiteralSecret(nested) {
			return true
		}
		if entries, ok := value.([]map[string]any); ok {
			for _, nested := range entries {
				if containsLiteralSecret(nested) {
					return true
				}
			}
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
