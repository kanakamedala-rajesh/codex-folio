package activity

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	SourceLocalMetadata       = "local_metadata"
	ProvenanceManagedLaunch   = "Managed Launch"
	ProvenanceObservedSession = "Observed during session"
	RecordTypeManagedLaunch   = "managed_launch"
	RecordTypeObservedSession = "observed_session"
	CorrelationCorrelated     = "correlated"
	CorrelationUncorrelated   = "uncorrelated"
	CorrelationAmbiguous      = "ambiguous"
	CorrelationContradictory  = "contradictory"
)

var (
	ErrActivityInvalid           = errors.New("activity request is invalid")
	ErrActivityUnavailable       = errors.New("activity source is unavailable")
	ErrActivityUnsupportedSource = errors.New("activity source is unsupported")
	ErrActivityInvalidSchema     = errors.New("activity source schema is invalid")
)

const (
	SourceStatusSupported     = "supported"
	SourceStatusMissing       = "missing"
	SourceStatusUnsupported   = "unsupported"
	SourceStatusSchemaInvalid = "schema_invalid"
	SourceStatusUnavailable   = "unavailable"
)

type SourceInspection struct {
	Status       string `json:"status"`
	SessionCount int    `json:"session_count"`
}

type SourceTarget struct {
	ID           string
	Label        string
	IdentityHome string
}

type SourceReview struct {
	SourceID     string `json:"source_id"`
	Label        string `json:"label"`
	Status       string `json:"status"`
	SessionCount int    `json:"session_count"`
}

// SourceSession is the allowlisted transient shape returned by a supported
// Codex metadata reader. WorkingDirectory is resolved to a Project Identity
// and is never persisted or projected by the activity workflow.
type SourceSession struct {
	SourceSessionID  string
	Source           string
	StartedAt        time.Time
	LastObservedAt   time.Time
	WorkingDirectory string
	Model            string
	TokensUsed       *int64
}

type ReadRequest struct {
	IdentityHome  string
	SourceVersion string
}

type Reader interface {
	Read(context.Context, ReadRequest) ([]SourceSession, error)
	Probe(context.Context, string) (SourceInspection, error)
}

type ProfileTarget struct {
	ID           string
	Alias        string
	IdentityHome string
}

// ObservedSessionRecord is the normalized persistence input. It deliberately
// has no source path, working directory, title, preview, or content field.
type ObservedSessionRecord struct {
	SourceSessionID string
	ProfileID       string
	ProfileAlias    string
	ProjectID       string
	ProjectAlias    string
	ProjectBasename string
	Source          string
	SourceVersion   string
	StartedAt       time.Time
	LastObservedAt  time.Time
	Model           string
	TokensUsed      *int64
}

type Correlation struct {
	State           string `json:"state"`
	ManagedLaunchID string `json:"managed_launch_id,omitempty"`
	EvidenceType    string `json:"evidence_type,omitempty"`
	Confidence      string `json:"confidence,omitempty"`
}

type TimelineRecord struct {
	RecordType                    string      `json:"record_type"`
	ID                            string      `json:"id"`
	SourceSessionID               string      `json:"source_session_id,omitempty"`
	ProfileID                     string      `json:"profile_id"`
	ProfileAlias                  string      `json:"profile_alias"`
	ProjectID                     string      `json:"project_id,omitempty"`
	ProjectAlias                  string      `json:"project_alias,omitempty"`
	ProjectBasename               string      `json:"project_basename,omitempty"`
	Source                        string      `json:"source"`
	SourceVersion                 string      `json:"source_version,omitempty"`
	Provenance                    string      `json:"provenance"`
	AttributionProvenance         string      `json:"attribution_provenance,omitempty"`
	OriginalProfileID             string      `json:"original_profile_id,omitempty"`
	OriginalAttributionProvenance string      `json:"original_attribution_provenance,omitempty"`
	StartedAt                     time.Time   `json:"started_at"`
	LastObservedAt                time.Time   `json:"last_observed_at"`
	Lifecycle                     string      `json:"lifecycle,omitempty"`
	ContinuationCheckpointID      string      `json:"continuation_checkpoint_id,omitempty"`
	ContinuationRevision          string      `json:"continuation_revision,omitempty"`
	ExitStatus                    *int        `json:"exit_status,omitempty"`
	Model                         string      `json:"model,omitempty"`
	TokensUsed                    *int64      `json:"tokens_used,omitempty"`
	Correlation                   Correlation `json:"correlation"`
}

type Filters struct {
	ProfileAlias string
	ProjectID    string
}

type Repository interface {
	ResolveActivityProfile(context.Context, string) (ProfileTarget, error)
	ListActivitySources(context.Context) ([]SourceTarget, error)
	SaveObservedSessions(context.Context, []ObservedSessionRecord) error
	ListActivity(context.Context, Filters) ([]TimelineRecord, error)
	AssignSessions(context.Context, []string, string) error
}

type Assignment struct {
	SessionIDs []string
	ProfileID  string
}

func (service *Service) Assign(ctx context.Context, assignment Assignment) error {
	if service == nil || len(assignment.SessionIDs) == 0 || len(assignment.SessionIDs) > 100 || len(assignment.ProfileID) > 128 {
		return ErrActivityInvalid
	}
	seen := make(map[string]bool, len(assignment.SessionIDs))
	for _, id := range assignment.SessionIDs {
		if strings.TrimSpace(id) == "" || len(id) > 128 || seen[id] {
			return ErrActivityInvalid
		}
		seen[id] = true
	}
	return service.repository.AssignSessions(contextOrBackground(ctx), assignment.SessionIDs, assignment.ProfileID)
}

type ProjectResolver interface {
	Resolve(context.Context, string, string) (ProjectIdentity, error)
}

type ServiceOptions struct {
	Repository Repository
	Reader     Reader
	Projects   ProjectResolver
}

type Service struct {
	repository Repository
	reader     Reader
	projects   ProjectResolver
}

type ImportResult struct {
	ImportedCount       int `json:"imported_count"`
	AlreadyPresentCount int `json:"already_present_count"`
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Repository == nil || options.Reader == nil || options.Projects == nil {
		return nil, ErrActivityInvalid
	}
	return &Service{repository: options.Repository, reader: options.Reader, projects: options.Projects}, nil
}

func (service *Service) Refresh(ctx context.Context, alias, sourceVersion string) ([]TimelineRecord, error) {
	if service == nil || strings.TrimSpace(alias) == "" || strings.TrimSpace(sourceVersion) == "" {
		return nil, ErrActivityInvalid
	}
	target, err := service.repository.ResolveActivityProfile(contextOrBackground(ctx), alias)
	if err != nil {
		return nil, err
	}
	sessions, err := service.reader.Read(contextOrBackground(ctx), ReadRequest{IdentityHome: target.IdentityHome, SourceVersion: sourceVersion})
	if err != nil {
		return nil, err
	}
	records := make([]ObservedSessionRecord, 0, len(sessions))
	for _, session := range sessions {
		if !validSourceSession(session) {
			return nil, ErrActivityInvalid
		}
		record := ObservedSessionRecord{
			SourceSessionID: session.SourceSessionID,
			Source:          session.Source, SourceVersion: sourceVersion, StartedAt: session.StartedAt.UTC(),
			LastObservedAt: session.LastObservedAt.UTC(), Model: session.Model, TokensUsed: session.TokensUsed,
		}
		if strings.TrimSpace(session.WorkingDirectory) != "" {
			project, resolveErr := service.projects.Resolve(contextOrBackground(ctx), session.WorkingDirectory, "")
			if resolveErr != nil && !errors.Is(resolveErr, ErrPathInvalid) {
				return nil, resolveErr
			}
			if resolveErr == nil {
				record.ProjectID, record.ProjectAlias, record.ProjectBasename = project.ID, project.Alias, project.Basename
			}
		}
		records = append(records, record)
	}
	if err := service.repository.SaveObservedSessions(contextOrBackground(ctx), records); err != nil {
		return nil, err
	}
	return service.repository.ListActivity(contextOrBackground(ctx), Filters{})
}

func (service *Service) ReviewSources(ctx context.Context) ([]SourceReview, error) {
	if service == nil {
		return nil, ErrActivityInvalid
	}
	targets, err := service.repository.ListActivitySources(contextOrBackground(ctx))
	if err != nil {
		return nil, err
	}
	reviews := make([]SourceReview, 0, len(targets))
	for _, target := range targets {
		inspection, err := service.reader.Probe(contextOrBackground(ctx), target.IdentityHome)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, SourceReview{SourceID: target.ID, Label: target.Label, Status: inspection.Status, SessionCount: inspection.SessionCount})
	}
	return reviews, nil
}

func (service *Service) ImportSource(ctx context.Context, sourceID, sourceVersion string, consent bool) (ImportResult, error) {
	if service == nil || !consent || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(sourceVersion) == "" {
		return ImportResult{}, ErrActivityInvalid
	}
	targets, err := service.repository.ListActivitySources(contextOrBackground(ctx))
	if err != nil {
		return ImportResult{}, err
	}
	var target *SourceTarget
	for i := range targets {
		if targets[i].ID == sourceID {
			target = &targets[i]
			break
		}
	}
	if target == nil {
		return ImportResult{}, ErrActivityInvalid
	}
	inspection, err := service.reader.Probe(contextOrBackground(ctx), target.IdentityHome)
	if err != nil {
		return ImportResult{}, err
	}
	if inspection.Status != SourceStatusSupported {
		return ImportResult{}, ErrActivityUnsupportedSource
	}
	sessions, err := service.reader.Read(contextOrBackground(ctx), ReadRequest{IdentityHome: target.IdentityHome, SourceVersion: sourceVersion})
	if err != nil {
		return ImportResult{}, err
	}
	existing, err := service.repository.ListActivity(contextOrBackground(ctx), Filters{})
	if err != nil {
		return ImportResult{}, err
	}
	seen := make(map[string]bool, len(existing))
	for _, item := range existing {
		if item.RecordType == RecordTypeObservedSession {
			seen[item.Source+":"+item.SourceSessionID] = true
		}
	}
	records := make([]ObservedSessionRecord, 0, len(sessions))
	result := ImportResult{}
	for _, session := range sessions {
		if !validSourceSession(session) {
			return ImportResult{}, ErrActivityInvalid
		}
		record := ObservedSessionRecord{SourceSessionID: session.SourceSessionID, Source: session.Source, SourceVersion: sourceVersion, StartedAt: session.StartedAt.UTC(), LastObservedAt: session.LastObservedAt.UTC(), Model: session.Model, TokensUsed: session.TokensUsed}
		if strings.TrimSpace(session.WorkingDirectory) != "" {
			project, resolveErr := service.projects.Resolve(contextOrBackground(ctx), session.WorkingDirectory, "")
			if resolveErr != nil && !errors.Is(resolveErr, ErrPathInvalid) {
				return ImportResult{}, resolveErr
			}
			if resolveErr == nil {
				record.ProjectID, record.ProjectAlias, record.ProjectBasename = project.ID, project.Alias, project.Basename
			}
		}
		identity := session.Source + ":" + session.SourceSessionID
		if seen[identity] {
			result.AlreadyPresentCount++
		} else {
			result.ImportedCount++
			seen[identity] = true
		}
		records = append(records, record)
	}
	if err := service.repository.SaveObservedSessions(contextOrBackground(ctx), records); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (service *Service) List(ctx context.Context, filters Filters) ([]TimelineRecord, error) {
	if service == nil {
		return nil, ErrActivityInvalid
	}
	return service.repository.ListActivity(contextOrBackground(ctx), filters)
}

func validSourceSession(session SourceSession) bool {
	return strings.TrimSpace(session.SourceSessionID) != "" && session.Source == SourceLocalMetadata &&
		!session.StartedAt.IsZero() && !session.LastObservedAt.IsZero() && !session.LastObservedAt.Before(session.StartedAt) &&
		(session.TokensUsed == nil || *session.TokensUsed >= 0)
}
