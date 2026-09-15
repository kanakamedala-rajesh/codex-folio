// Package continuation owns repository-first checkpoint creation and review state.
package continuation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"venkatasudha.com/codex-folio/internal/activity"
)

const (
	MaxCheckpointBytes       = 16 * 1024
	StatusDraft              = "draft"
	StatusApproved           = "approved"
	StatusLaunching          = "launching"
	StatusCompleted          = "completed"
	StatusExpired            = "expired"
	SourceRepositoryFirst    = "repository-first"
	SourceTranscriptAssisted = "transcript-assisted"
	ProvenanceLocalObserved  = "local-observed"
	ProvenanceUserConfirmed  = "user-confirmed"
	ProvenanceModelDerived   = "model-derived"
	ProvenanceUnknown        = "unknown"
	CompletenessComplete     = "complete"
	CompletenessPartial      = "partial"
	CompletenessUnknown      = "unknown"
	FreshnessFresh           = "fresh"
	FreshnessStale           = "stale"
	FreshnessUnknown         = "unknown"
	RedactedValue            = "[REDACTED]"
)

var (
	ErrCheckpointInvalid         = errors.New("checkpoint request is invalid")
	ErrCheckpointNotFound        = errors.New("checkpoint was not found")
	ErrCheckpointOversize        = errors.New("checkpoint exceeds the review size limit")
	ErrCheckpointRevisionChanged = errors.New("checkpoint changed after review")
	ErrHandoffNotReady           = errors.New("Safe Continuation is not ready")
	ErrRepositoryInspection      = errors.New("repository inspection failed")
	ErrHistoryUnavailable        = errors.New("stored history is unavailable")
)

type SourceState string

const (
	SourceExited    SourceState = "exited"
	SourceRunning   SourceState = "running"
	SourceUncertain SourceState = "uncertain"
)

type SourceLaunch struct {
	ProfileID string
	State     SourceState
}

type HistorySource struct {
	SourceProfileID string `json:"source_profile_id"`
	IdentityHome    string `json:"identity_home"`
}

type PreparedHandoff struct {
	CheckpointID     string
	Revision         string
	ProjectID        string
	SourceProfileID  string
	WorkingDirectory string
	Context          string
}

type Evidence[T any] struct {
	Value        T      `json:"value"`
	Provenance   string `json:"provenance"`
	Completeness string `json:"completeness"`
}

type UpstreamDivergence struct {
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
}

type DiffStatistics struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
	BinaryFiles  int `json:"binary_files,omitempty"`
}

type RepositoryInventory struct {
	Branch             string
	Head               string
	Upstream           *UpstreamDivergence
	Staged             []string
	Modified           []string
	Untracked          []string
	Diff               DiffStatistics
	ConfiguredCommands []string
}

type RepositoryState struct {
	Branch             Evidence[string]              `json:"branch"`
	Head               Evidence[string]              `json:"head"`
	Upstream           Evidence[*UpstreamDivergence] `json:"upstream"`
	Staged             Evidence[[]string]            `json:"staged"`
	Modified           Evidence[[]string]            `json:"modified"`
	Untracked          Evidence[[]string]            `json:"untracked"`
	Diff               Evidence[DiffStatistics]      `json:"diff_statistics"`
	ConfiguredCommands Evidence[[]string]            `json:"configured_commands"`
}

type ValidationEvidence struct {
	Command    string     `json:"command"`
	Timestamp  *time.Time `json:"timestamp"`
	ExitStatus *int       `json:"exit_status"`
	Source     string     `json:"source"`
	Freshness  string     `json:"freshness"`
}

type CheckpointFields struct {
	Goal          Evidence[string]               `json:"goal"`
	CompletedWork Evidence[string]               `json:"completed_work"`
	PendingWork   Evidence[string]               `json:"pending_work"`
	Validation    Evidence[[]ValidationEvidence] `json:"known_validation"`
	Risks         Evidence[string]               `json:"risks"`
	NextAction    Evidence[string]               `json:"next_action"`
}

type Project struct {
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Basename string `json:"basename"`
}

type Checkpoint struct {
	ID         string           `json:"id"`
	Status     string           `json:"status"`
	Project    Project          `json:"project"`
	Repository RepositoryState  `json:"repository"`
	Fields     CheckpointFields `json:"fields"`
	Source     string           `json:"source"`
	Retention  string           `json:"retention"`
	CreatedAt  time.Time        `json:"created_at"`
	ExpiresAt  *time.Time       `json:"expires_at"`
	SizeBytes  int              `json:"size_bytes"`
	Revision   string           `json:"revision"`
}

type CaptureRequest struct {
	Path, Alias, Goal, CompletedWork, PendingWork, Risks, NextAction string
	Validation                                                       *ValidationEvidence
	ConfiguredCommands, RedactPaths, RedactText                      []string
}

type HistoryReadRequest struct {
	Executable, IdentityHome, ThreadID string
}

type EditRequest struct {
	Fields                  CheckpointFields
	RedactPaths, RedactText []string
}

// MergeHistoryCandidates overlays only populated, bounded history candidates
// onto a repository-first draft. Callers still must preview, edit, redact, and
// explicitly approve the resulting assisted revision.
func MergeHistoryCandidates(base, candidates CheckpointFields) CheckpointFields {
	pairs := [][2]*Evidence[string]{
		{&base.Goal, &candidates.Goal}, {&base.CompletedWork, &candidates.CompletedWork},
		{&base.PendingWork, &candidates.PendingWork}, {&base.Risks, &candidates.Risks}, {&base.NextAction, &candidates.NextAction},
	}
	for _, pair := range pairs {
		if strings.TrimSpace(pair[1].Value) != "" {
			*pair[0] = *pair[1]
		}
	}
	return base
}

// RedactHistoryIdentityHome removes the internally resolved Identity Home from
// bounded history candidates while preserving their evidence metadata.
func RedactHistoryIdentityHome(fields CheckpointFields, identityHome string) CheckpointFields {
	values := []*Evidence[string]{&fields.Goal, &fields.CompletedWork, &fields.PendingWork, &fields.Risks, &fields.NextAction}
	for _, field := range values {
		field.Value = sanitizeText(field.Value, identityHome, nil)
	}
	for index := range fields.Validation.Value {
		fields.Validation.Value[index].Command = sanitizeText(fields.Validation.Value[index].Command, identityHome, nil)
		fields.Validation.Value[index].Source = sanitizeText(fields.Validation.Value[index].Source, identityHome, nil)
	}
	return fields
}

type CheckpointRecord struct {
	ID, ProjectIdentityID, Status, Metadata                         string
	ExpectedStatus, ExpectedRevision                                string
	Goal, CompletedWork, PendingWork, Validation, Risks, NextAction *string
	CreatedAt                                                       time.Time
	ExpiresAt                                                       *time.Time
}

type Repository interface {
	SaveCheckpoint(context.Context, CheckpointRecord) error
	LoadCheckpoint(context.Context, string) (CheckpointRecord, error)
	ListCheckpoints(context.Context) ([]CheckpointRecord, error)
	PurgeCheckpoint(context.Context, string, string) error
	LatestSourceLaunch(context.Context, string) (SourceLaunch, error)
	SourceIdentityHome(context.Context, string) (string, error)
	CheckpointRetention(context.Context) (RetentionPolicy, error)
	SetCheckpointRetention(context.Context, string, string) (RetentionPolicy, error)
}

type Projects interface {
	Resolve(context.Context, string, string) (activity.ProjectIdentity, error)
	CanonicalLocation(context.Context, string) (string, error)
}

type Inspector interface {
	Inspect(context.Context, string) (RepositoryInventory, error)
}

type ServiceOptions struct {
	Repository    Repository
	Projects      Projects
	Inspector     Inspector
	Now           func() time.Time
	Random        io.Reader
	HomeDirectory string
}

type Service struct {
	repository Repository
	projects   Projects
	inspector  Inspector
	now        func() time.Time
	random     io.Reader
	home       string
	mutationMu sync.Mutex
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil || options.Projects == nil || options.Inspector == nil {
		return nil, ErrCheckpointInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Service{repository: options.Repository, projects: options.Projects, inspector: options.Inspector, now: options.Now, random: options.Random, home: options.HomeDirectory}, nil
}

func (service *Service) Capture(ctx context.Context, request CaptureRequest) (Checkpoint, error) {
	if strings.TrimSpace(request.Path) == "" || invalidCaptureText(request) || invalidRedactions(request.RedactPaths, request.RedactText) || invalidValidation(request.Validation) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	project, err := service.projects.Resolve(ctx, request.Path, request.Alias)
	if err != nil {
		return Checkpoint{}, err
	}
	project.Alias = sanitizeText(project.Alias, service.home, request.RedactText)
	project.Basename = sanitizeText(project.Basename, service.home, request.RedactText)
	path, err := service.projects.CanonicalLocation(ctx, project.ID)
	if err != nil {
		return Checkpoint{}, err
	}
	inventory, err := service.inspector.Inspect(ctx, path)
	if err != nil {
		return Checkpoint{}, errors.Join(ErrRepositoryInspection, err)
	}
	inventory.Branch = sanitizeText(inventory.Branch, service.home, request.RedactText)
	redactedPaths := normalizedRedactions(request.RedactPaths)
	if inventory.Staged, err = sanitizePaths(inventory.Staged, redactedPaths); err != nil {
		return Checkpoint{}, err
	}
	if inventory.Modified, err = sanitizePaths(inventory.Modified, redactedPaths); err != nil {
		return Checkpoint{}, err
	}
	if inventory.Untracked, err = sanitizePaths(inventory.Untracked, redactedPaths); err != nil {
		return Checkpoint{}, err
	}
	for _, command := range request.ConfiguredCommands {
		inventory.ConfiguredCommands = append(inventory.ConfiguredCommands, sanitizeText(command, service.home, request.RedactText))
	}

	now := service.now().UTC()
	policy, err := service.repository.CheckpointRetention(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	setting, err := policy.setting(SourceRepositoryFirst)
	if err != nil {
		return Checkpoint{}, err
	}
	expiresAt, err := retentionExpiry(now, setting)
	if err != nil {
		return Checkpoint{}, err
	}
	id, err := newID(service.random)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	checkpoint := Checkpoint{
		ID: id, Status: StatusDraft, Project: Project{ID: project.ID, Alias: project.Alias, Basename: project.Basename}, Source: SourceRepositoryFirst,
		Repository: repositoryState(inventory), Retention: setting, CreatedAt: now, ExpiresAt: expiresAt,
	}
	checkpoint.Fields.Goal = userField(sanitizeText(request.Goal, service.home, request.RedactText))
	checkpoint.Fields.CompletedWork = userField(sanitizeText(request.CompletedWork, service.home, request.RedactText))
	checkpoint.Fields.PendingWork = userField(sanitizeText(request.PendingWork, service.home, request.RedactText))
	checkpoint.Fields.Risks = userField(sanitizeText(request.Risks, service.home, request.RedactText))
	checkpoint.Fields.NextAction = userField(sanitizeText(request.NextAction, service.home, request.RedactText))
	checkpoint.Fields.Validation = validationField(request.Validation, service.home, request.RedactText)
	checkpoint, metadata, err := prepareCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	if checkpoint.SizeBytes > MaxCheckpointBytes {
		return Checkpoint{}, ErrCheckpointOversize
	}
	if err := service.save(ctx, checkpoint, metadata, "", ""); err != nil {
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

// CaptureProject is the browser-safe capture seam: callers provide an
// app-local Project Identity and never receive or submit its canonical path.
func (service *Service) CaptureProject(ctx context.Context, projectID string, request CaptureRequest) (Checkpoint, error) {
	if strings.TrimSpace(projectID) == "" || invalidText(projectID) || strings.TrimSpace(request.Path) != "" || strings.TrimSpace(request.Alias) != "" {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	path, err := service.projects.CanonicalLocation(ctx, projectID)
	if err != nil {
		return Checkpoint{}, err
	}
	request.Path = path
	return service.Capture(ctx, request)
}

// Source reports the conservative source-process state used by Safe
// Continuation. Missing exit evidence remains uncertain.
func (service *Service) Source(ctx context.Context, projectID string) (SourceLaunch, error) {
	if strings.TrimSpace(projectID) == "" || invalidText(projectID) {
		return SourceLaunch{}, ErrCheckpointInvalid
	}
	return service.repository.LatestSourceLaunch(ctx, projectID)
}

func (service *Service) Retention(ctx context.Context, source, setting string) (RetentionPolicy, error) {
	if source == "" && setting == "" {
		policy, err := service.repository.CheckpointRetention(ctx)
		if err != nil {
			return RetentionPolicy{}, err
		}
		policy.RepositoryFirst, err = policy.setting(SourceRepositoryFirst)
		if err == nil {
			policy.TranscriptAssisted, err = policy.setting(SourceTranscriptAssisted)
		}
		return policy, err
	}
	if source == "" || setting == "" {
		return RetentionPolicy{}, ErrCheckpointInvalid
	}
	if _, _, err := ParseRetention(setting); err != nil {
		return RetentionPolicy{}, err
	}
	return service.repository.SetCheckpointRetention(ctx, source, setting)
}

func (service *Service) Show(ctx context.Context, id string) (Checkpoint, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.show(ctx, id)
}

// List returns retained checkpoints newest first while applying the same
// expiry and sensitive-field scrubbing used by Show.
func (service *Service) List(ctx context.Context) ([]Checkpoint, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	records, err := service.repository.ListCheckpoints(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Checkpoint, 0, len(records))
	for _, record := range records {
		checkpoint, showErr := service.show(ctx, record.ID)
		if showErr != nil {
			return nil, showErr
		}
		result = append(result, checkpoint)
	}
	return result, nil
}

// Purge deletes one exact retained checkpoint revision. A checkpoint whose
// launch outcome is still uncertain remains protected for recovery.
func (service *Service) Purge(ctx context.Context, id, revision string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(revision) == "" || invalidText(id) || invalidText(revision) {
		return ErrCheckpointInvalid
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	checkpoint, err := service.show(ctx, id)
	if err != nil {
		return err
	}
	if checkpoint.Revision != revision {
		return ErrCheckpointRevisionChanged
	}
	if checkpoint.Status == StatusLaunching {
		return ErrHandoffNotReady
	}
	return service.repository.PurgeCheckpoint(ctx, id, revision)
}

func (service *Service) show(ctx context.Context, id string) (Checkpoint, error) {
	if strings.TrimSpace(id) == "" || invalidText(id) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	record, err := service.repository.LoadCheckpoint(ctx, id)
	if err != nil {
		return Checkpoint{}, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(record.Metadata), &checkpoint); err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	checkpoint.Status = record.Status
	expectedStatus, expectedRevision := checkpoint.Status, checkpoint.Revision
	if checkpoint.Retention == "" {
		if checkpoint.Source == SourceTranscriptAssisted {
			checkpoint.Retention = DefaultTranscriptRetention
		} else {
			checkpoint.Retention = DefaultRepositoryRetention
		}
	}
	if checkpoint.ExpiresAt != nil && !service.now().UTC().Before(checkpoint.ExpiresAt.UTC()) && checkpoint.Status != StatusLaunching {
		checkpoint = Checkpoint{
			ID: checkpoint.ID, Status: StatusExpired, Project: Project{ID: record.ProjectIdentityID},
			Source: checkpoint.Source, Retention: checkpoint.Retention, CreatedAt: checkpoint.CreatedAt, ExpiresAt: checkpoint.ExpiresAt, Revision: checkpoint.Revision,
		}
		metadata, encodeErr := json.Marshal(struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
			Source    string     `json:"source"`
			Retention string     `json:"retention"`
			CreatedAt time.Time  `json:"created_at"`
			ExpiresAt *time.Time `json:"expires_at"`
			Revision  string     `json:"revision"`
		}{checkpoint.ID, checkpoint.Status, struct {
			ID string `json:"id"`
		}{checkpoint.Project.ID}, checkpoint.Source, checkpoint.Retention, checkpoint.CreatedAt, checkpoint.ExpiresAt, checkpoint.Revision})
		if encodeErr != nil {
			return Checkpoint{}, ErrCheckpointInvalid
		}
		sensitivePersisted := record.Goal != nil || record.CompletedWork != nil || record.PendingWork != nil || record.Validation != nil || record.Risks != nil || record.NextAction != nil
		if record.Status != StatusExpired || record.Metadata != string(metadata) || sensitivePersisted {
			if err := service.repository.SaveCheckpoint(ctx, CheckpointRecord{
				ID: checkpoint.ID, ProjectIdentityID: checkpoint.Project.ID, Status: checkpoint.Status, Metadata: string(metadata),
				ExpectedStatus: expectedStatus, ExpectedRevision: expectedRevision, CreatedAt: checkpoint.CreatedAt, ExpiresAt: checkpoint.ExpiresAt,
			}); err != nil {
				return Checkpoint{}, err
			}
		}
	}
	return checkpoint, nil
}

func (service *Service) PrepareHistory(ctx context.Context, id, revision string) (HistorySource, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(revision) == "" || invalidText(id) || invalidText(revision) {
		return HistorySource{}, ErrCheckpointInvalid
	}
	checkpoint, err := service.Show(ctx, id)
	if err != nil {
		return HistorySource{}, err
	}
	if checkpoint.Status != StatusDraft || checkpoint.Source != SourceRepositoryFirst || checkpoint.Revision != revision {
		return HistorySource{}, ErrHistoryUnavailable
	}
	source, err := service.repository.LatestSourceLaunch(ctx, checkpoint.Project.ID)
	if err != nil || source.State != SourceExited || source.ProfileID == "" {
		return HistorySource{}, ErrHistoryUnavailable
	}
	home, err := service.repository.SourceIdentityHome(ctx, source.ProfileID)
	if err != nil || !filepath.IsAbs(home) {
		return HistorySource{}, ErrHistoryUnavailable
	}
	return HistorySource{SourceProfileID: source.ProfileID, IdentityHome: filepath.Clean(home)}, nil
}

func (service *Service) Edit(ctx context.Context, id string, request EditRequest) (Checkpoint, error) {
	if invalidRedactions(request.RedactPaths, request.RedactText) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	checkpoint, err := service.show(ctx, id)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Status == StatusLaunching || checkpoint.Status == StatusCompleted || checkpoint.Status == StatusExpired {
		return Checkpoint{}, ErrHandoffNotReady
	}
	expectedStatus, expectedRevision := checkpoint.Status, checkpoint.Revision
	if err := service.applyEdit(&checkpoint, request); err != nil {
		return Checkpoint{}, err
	}
	checkpoint.Status = StatusDraft
	checkpoint, metadata, err := prepareCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	if checkpoint.SizeBytes > MaxCheckpointBytes {
		return Checkpoint{}, ErrCheckpointOversize
	}
	if err := service.save(ctx, checkpoint, metadata, expectedStatus, expectedRevision); err != nil {
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

func (service *Service) PreviewAssisted(ctx context.Context, id, revision string, request EditRequest) (Checkpoint, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	return service.previewAssisted(ctx, id, revision, request)
}

func (service *Service) ApproveAssisted(ctx context.Context, id, revision, previewRevision string, request EditRequest) (Checkpoint, error) {
	if strings.TrimSpace(previewRevision) == "" || invalidText(previewRevision) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	checkpoint, err := service.previewAssisted(ctx, id, revision, request)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Revision != previewRevision {
		return Checkpoint{}, ErrCheckpointRevisionChanged
	}
	checkpoint.Status = StatusApproved
	checkpoint, metadata, err := finalizeCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	if checkpoint.SizeBytes > MaxCheckpointBytes {
		return Checkpoint{}, ErrCheckpointOversize
	}
	if err := service.save(ctx, checkpoint, metadata, StatusDraft, revision); err != nil {
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

func (service *Service) previewAssisted(ctx context.Context, id, revision string, request EditRequest) (Checkpoint, error) {
	if strings.TrimSpace(revision) == "" || invalidText(revision) || invalidRedactions(request.RedactPaths, request.RedactText) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	checkpoint, err := service.show(ctx, id)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Status != StatusDraft || checkpoint.Source != SourceRepositoryFirst || checkpoint.Revision != revision {
		return Checkpoint{}, ErrCheckpointRevisionChanged
	}
	if err := service.applyEdit(&checkpoint, request); err != nil {
		return Checkpoint{}, err
	}
	policy, err := service.repository.CheckpointRetention(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint.Source = SourceTranscriptAssisted
	checkpoint.Retention, err = policy.setting(SourceTranscriptAssisted)
	if err == nil {
		checkpoint.ExpiresAt, err = retentionExpiry(checkpoint.CreatedAt, checkpoint.Retention)
	}
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.ExpiresAt != nil && !service.now().UTC().Before(checkpoint.ExpiresAt.UTC()) {
		return Checkpoint{}, ErrHandoffNotReady
	}
	checkpoint, _, err = prepareCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	if checkpoint.SizeBytes > MaxCheckpointBytes {
		return Checkpoint{}, ErrCheckpointOversize
	}
	return checkpoint, nil
}

func (service *Service) applyEdit(checkpoint *Checkpoint, request EditRequest) error {
	fields, err := sanitizeFields(request.Fields, service.home, request.RedactText)
	if err != nil {
		return err
	}
	checkpoint.Fields = fields
	checkpoint.Project.Alias = sanitizeText(checkpoint.Project.Alias, service.home, request.RedactText)
	checkpoint.Project.Basename = sanitizeText(checkpoint.Project.Basename, service.home, request.RedactText)
	checkpoint.Repository.Branch.Value = sanitizeText(checkpoint.Repository.Branch.Value, service.home, request.RedactText)
	redactedPaths := normalizedRedactions(request.RedactPaths)
	if checkpoint.Repository.Staged.Value, err = sanitizePaths(checkpoint.Repository.Staged.Value, redactedPaths); err != nil {
		return err
	}
	if checkpoint.Repository.Modified.Value, err = sanitizePaths(checkpoint.Repository.Modified.Value, redactedPaths); err != nil {
		return err
	}
	if checkpoint.Repository.Untracked.Value, err = sanitizePaths(checkpoint.Repository.Untracked.Value, redactedPaths); err != nil {
		return err
	}
	for index, command := range checkpoint.Repository.ConfiguredCommands.Value {
		checkpoint.Repository.ConfiguredCommands.Value[index] = sanitizeText(command, service.home, request.RedactText)
	}
	return nil
}

func (service *Service) Approve(ctx context.Context, id, revision string) (Checkpoint, error) {
	if revision == "" || invalidText(revision) {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	checkpoint, err := service.show(ctx, id)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Status == StatusLaunching || checkpoint.Status == StatusCompleted || checkpoint.Status == StatusExpired {
		return Checkpoint{}, ErrHandoffNotReady
	}
	if checkpoint.Revision != revision {
		return Checkpoint{}, ErrCheckpointRevisionChanged
	}
	expectedStatus, expectedRevision := checkpoint.Status, checkpoint.Revision
	checkpoint.Status = StatusApproved
	checkpoint, metadata, err := finalizeCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, ErrCheckpointInvalid
	}
	if checkpoint.SizeBytes > MaxCheckpointBytes {
		return Checkpoint{}, ErrCheckpointOversize
	}
	if err := service.save(ctx, checkpoint, metadata, expectedStatus, expectedRevision); err != nil {
		return Checkpoint{}, err
	}
	return checkpoint, nil
}

func (service *Service) PrepareHandoff(ctx context.Context, id, revision, targetProfileID string) (PreparedHandoff, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(revision) == "" || strings.TrimSpace(targetProfileID) == "" || invalidText(id) || invalidText(revision) || invalidText(targetProfileID) {
		return PreparedHandoff{}, ErrCheckpointInvalid
	}
	record, err := service.repository.LoadCheckpoint(ctx, id)
	if err != nil {
		return PreparedHandoff{}, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(record.Metadata), &checkpoint); err != nil {
		return PreparedHandoff{}, ErrCheckpointInvalid
	}
	if record.Status != StatusApproved || checkpoint.Status != StatusApproved || checkpoint.Revision != revision || record.ProjectIdentityID == "" || checkpoint.Project.ID != record.ProjectIdentityID || (record.ExpiresAt != nil && !service.now().UTC().Before(record.ExpiresAt.UTC())) || !sameExpiry(checkpoint.ExpiresAt, record.ExpiresAt) {
		return PreparedHandoff{}, ErrHandoffNotReady
	}
	source, err := service.repository.LatestSourceLaunch(ctx, record.ProjectIdentityID)
	if err != nil {
		return PreparedHandoff{}, err
	}
	if source.State != SourceExited || source.ProfileID == "" || source.ProfileID == targetProfileID {
		return PreparedHandoff{}, ErrHandoffNotReady
	}
	workingDirectory, err := service.projects.CanonicalLocation(ctx, record.ProjectIdentityID)
	if err != nil {
		return PreparedHandoff{}, err
	}
	if _, err := service.inspector.Inspect(ctx, workingDirectory); err != nil {
		return PreparedHandoff{}, errors.Join(ErrRepositoryInspection, err)
	}
	return PreparedHandoff{
		CheckpointID: id, Revision: revision, ProjectID: record.ProjectIdentityID, SourceProfileID: source.ProfileID,
		WorkingDirectory: workingDirectory, Context: record.Metadata,
	}, nil
}

func (service *Service) save(ctx context.Context, checkpoint Checkpoint, metadata, expectedStatus, expectedRevision string) error {
	validation, _ := json.Marshal(checkpoint.Fields.Validation.Value)
	return service.repository.SaveCheckpoint(ctx, CheckpointRecord{
		ID: checkpoint.ID, ProjectIdentityID: checkpoint.Project.ID, Status: checkpoint.Status, Metadata: metadata,
		ExpectedStatus: expectedStatus, ExpectedRevision: expectedRevision,
		Goal: pointerOrNil(checkpoint.Fields.Goal.Value), CompletedWork: pointerOrNil(checkpoint.Fields.CompletedWork.Value), PendingWork: pointerOrNil(checkpoint.Fields.PendingWork.Value),
		Validation: pointerOrNil(string(validation)), Risks: pointerOrNil(checkpoint.Fields.Risks.Value), NextAction: pointerOrNil(checkpoint.Fields.NextAction.Value), CreatedAt: checkpoint.CreatedAt, ExpiresAt: checkpoint.ExpiresAt,
	})
}

func sanitizeFields(fields CheckpointFields, home string, redactions []string) (CheckpointFields, error) {
	values := []*Evidence[string]{&fields.Goal, &fields.CompletedWork, &fields.PendingWork, &fields.Risks, &fields.NextAction}
	for _, field := range values {
		if invalidText(field.Value) {
			return CheckpointFields{}, ErrCheckpointInvalid
		}
		*field = userField(sanitizeText(field.Value, home, redactions))
	}
	validations := make([]ValidationEvidence, 0, len(fields.Validation.Value))
	completeness := CompletenessComplete
	for _, validation := range fields.Validation.Value {
		if invalidValidation(&validation) {
			return CheckpointFields{}, ErrCheckpointInvalid
		}
		value := validationField(&validation, home, redactions)
		validations = append(validations, value.Value[0])
		if value.Completeness != CompletenessComplete {
			completeness = CompletenessPartial
		}
	}
	if len(validations) == 0 {
		fields.Validation = Evidence[[]ValidationEvidence]{Value: []ValidationEvidence{}, Provenance: ProvenanceUnknown, Completeness: CompletenessUnknown}
	} else {
		fields.Validation = Evidence[[]ValidationEvidence]{Value: validations, Provenance: ProvenanceUserConfirmed, Completeness: completeness}
	}
	return fields, nil
}

func repositoryState(inventory RepositoryInventory) RepositoryState {
	upstreamCompleteness := CompletenessComplete
	if inventory.Upstream == nil {
		upstreamCompleteness = CompletenessUnknown
	}
	return RepositoryState{
		Branch: observed(inventory.Branch, completeness(inventory.Branch != "")), Head: observed(inventory.Head, completeness(inventory.Head != "")),
		Upstream: observed(inventory.Upstream, upstreamCompleteness), Staged: observed(nonNil(inventory.Staged), CompletenessComplete),
		Modified: observed(nonNil(inventory.Modified), CompletenessComplete), Untracked: observed(nonNil(inventory.Untracked), CompletenessComplete),
		Diff: observed(inventory.Diff, CompletenessComplete), ConfiguredCommands: userValues(inventory.ConfiguredCommands),
	}
}

func observed[T any](value T, complete string) Evidence[T] {
	return Evidence[T]{Value: value, Provenance: ProvenanceLocalObserved, Completeness: complete}
}
func completeness(ok bool) string {
	if ok {
		return CompletenessComplete
	}
	return CompletenessUnknown
}
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func userField(value string) Evidence[string] {
	if strings.TrimSpace(value) == "" {
		return Evidence[string]{Value: value, Provenance: ProvenanceUnknown, Completeness: CompletenessUnknown}
	}
	return Evidence[string]{Value: value, Provenance: ProvenanceUserConfirmed, Completeness: CompletenessComplete}
}

func userValues(values []string) Evidence[[]string] {
	if len(values) == 0 {
		return Evidence[[]string]{Value: []string{}, Provenance: ProvenanceUnknown, Completeness: CompletenessUnknown}
	}
	return Evidence[[]string]{Value: values, Provenance: ProvenanceUserConfirmed, Completeness: CompletenessComplete}
}

func validationField(value *ValidationEvidence, home string, redactions []string) Evidence[[]ValidationEvidence] {
	if value == nil {
		return Evidence[[]ValidationEvidence]{Value: []ValidationEvidence{}, Provenance: ProvenanceUnknown, Completeness: CompletenessUnknown}
	}
	copy := *value
	complete := CompletenessComplete
	if copy.Timestamp == nil || copy.ExitStatus == nil || copy.Source == "" || copy.Source == ProvenanceUnknown || copy.Freshness == "" || copy.Freshness == FreshnessUnknown {
		complete = CompletenessPartial
	}
	if copy.Source == "" {
		copy.Source = ProvenanceUnknown
	}
	if copy.Freshness == "" {
		copy.Freshness = FreshnessUnknown
	}
	copy.Command, copy.Source = sanitizeText(copy.Command, home, redactions), sanitizeText(copy.Source, home, redactions)
	return Evidence[[]ValidationEvidence]{Value: []ValidationEvidence{copy}, Provenance: ProvenanceUserConfirmed, Completeness: complete}
}

func sanitizeText(value, home string, redactions []string) string {
	if home != "" {
		for _, candidate := range []string{home, strings.ReplaceAll(home, `\`, "/"), strings.ReplaceAll(home, "/", `\`)} {
			if isWindowsPath(home) {
				value = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(candidate)).ReplaceAllLiteralString(value, "[HOME]")
			} else {
				value = strings.ReplaceAll(value, candidate, "[HOME]")
			}
		}
	}
	for _, redaction := range redactions {
		value = strings.ReplaceAll(value, redaction, RedactedValue)
	}
	return value
}

func isWindowsPath(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//")
}

func sanitizePaths(paths, redactions []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		normalized := filepath.ToSlash(filepath.Clean(path))
		if invalidRelativePath(path, normalized) {
			return nil, ErrCheckpointInvalid
		}
		if slices.Contains(redactions, normalized) {
			normalized = RedactedValue
		}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizedRedactions(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, filepath.ToSlash(filepath.Clean(value)))
	}
	return result
}

func invalidRedactions(paths, text []string) bool {
	for _, value := range paths {
		normalized := filepath.ToSlash(filepath.Clean(value))
		if invalidRelativePath(value, normalized) {
			return true
		}
	}
	for _, value := range append(slices.Clone(paths), text...) {
		if value == "" || invalidText(value) {
			return true
		}
	}
	return false
}

func invalidRelativePath(value, normalized string) bool {
	return filepath.IsAbs(value) || normalized == ".." || strings.HasPrefix(normalized, "../") || strings.IndexFunc(value, unicode.IsControl) >= 0
}

func invalidCaptureText(request CaptureRequest) bool {
	values := append([]string{request.Alias, request.Goal, request.CompletedWork, request.PendingWork, request.Risks, request.NextAction}, request.ConfiguredCommands...)
	for _, value := range values {
		if invalidText(value) {
			return true
		}
	}
	return false
}

func invalidValidation(value *ValidationEvidence) bool {
	if value == nil {
		return false
	}
	if strings.TrimSpace(value.Command) == "" || invalidText(value.Command) || invalidText(value.Source) {
		return true
	}
	return value.Freshness != "" && value.Freshness != FreshnessFresh && value.Freshness != FreshnessStale && value.Freshness != FreshnessUnknown
}

func invalidText(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0
}
func pointerOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func encodeCheckpoint(value Checkpoint) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func finalizeCheckpoint(checkpoint Checkpoint) (Checkpoint, string, error) {
	for {
		metadata, err := encodeCheckpoint(checkpoint)
		if err != nil {
			return Checkpoint{}, "", err
		}
		if checkpoint.SizeBytes == len(metadata) {
			return checkpoint, metadata, nil
		}
		checkpoint.SizeBytes = len(metadata)
	}
}

func prepareCheckpoint(checkpoint Checkpoint) (Checkpoint, string, error) {
	content, err := json.Marshal(struct {
		Project    Project          `json:"project"`
		Repository RepositoryState  `json:"repository"`
		Fields     CheckpointFields `json:"fields"`
		Source     string           `json:"source"`
		Retention  string           `json:"retention"`
		CreatedAt  time.Time        `json:"created_at"`
		ExpiresAt  *time.Time       `json:"expires_at"`
	}{checkpoint.Project, checkpoint.Repository, checkpoint.Fields, checkpoint.Source, checkpoint.Retention, checkpoint.CreatedAt, checkpoint.ExpiresAt})
	if err != nil {
		return Checkpoint{}, "", err
	}
	digest := sha256.Sum256(content)
	checkpoint.Revision = hex.EncodeToString(digest[:])
	return finalizeCheckpoint(checkpoint)
}

func sameExpiry(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func newID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	value[6], value[8] = (value[6]&0x0f)|0x40, (value[8]&0x3f)|0x80
	return hex.EncodeToString(value[0:4]) + "-" + hex.EncodeToString(value[4:6]) + "-" + hex.EncodeToString(value[6:8]) + "-" + hex.EncodeToString(value[8:10]) + "-" + hex.EncodeToString(value[10:16]), nil
}
