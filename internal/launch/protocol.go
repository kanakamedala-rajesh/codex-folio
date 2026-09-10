package launch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/profile"
)

type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateExited    State = "exited"
	StateAbandoned State = "abandoned"
)

type PrepareRequest struct {
	Alias              string
	Executable         string
	WorkingDirectory   string
	Arguments          []string
	ProjectID          string
	ExpectedSessionID  string
	CheckpointID       string
	CheckpointRevision string
	SourceProfileID    string
	BootSessionID      string
}

type Plan struct {
	LeaseID          string            `json:"lease_id"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory"`
	Arguments        []string          `json:"arguments"`
	Environment      map[string]string `json:"environment"`
}

type ManagedLaunch struct {
	ID           string     `json:"id"`
	ProfileID    string     `json:"profile_id"`
	ProfileAlias string     `json:"profile_alias,omitempty"`
	LeaseID      string     `json:"lease_id"`
	State        State      `json:"state"`
	ProcessID    int        `json:"process_id,omitempty"`
	ExitStatus   *int       `json:"exit_status,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
}

type SafeContinuationAlternative struct {
	Alias         string `json:"alias"`
	CapacityState string `json:"capacity_state"`
	Provenance    string `json:"provenance"`
	Recommended   bool   `json:"recommended"`
}

type SafeContinuationOffer struct {
	Alternatives []SafeContinuationAlternative `json:"alternatives"`
}

type ProcessInspector interface {
	IsRunning(processID int) (bool, error)
	BootSessionID() (string, error)
}

type Repository interface {
	PrepareLaunch(context.Context, PrepareRequest) (Plan, error)
	MarkManagedLaunchStarted(context.Context, string, int) error
	MarkManagedLaunchExited(context.Context, string, int) error
	MarkManagedLaunchAbandoned(context.Context, string) error
	ReconcileManagedLaunches(context.Context, ProcessInspector) error
}

type WorkflowOptions struct {
	Repository Repository
}

type Workflow struct {
	repository Repository
}

var (
	ErrPlanInvalid          = errors.New("launch plan is invalid")
	ErrProfileNotFound      = errors.New("launch profile was not found")
	ErrProfileUnavailable   = errors.New("launch profile is not ready")
	ErrLeaseInvalid         = errors.New("managed launch lease is invalid")
	ErrProcessStartFailed   = errors.New("Codex process could not be started")
	ErrProcessStatusInvalid = errors.New("Codex process status is invalid")
)

func NewWorkflow(options WorkflowOptions) (*Workflow, error) {
	if options.Repository == nil {
		return nil, apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	return &Workflow{repository: options.Repository}, nil
}

func (workflow *Workflow) Prepare(ctx context.Context, request PrepareRequest) (Plan, error) {
	if workflow == nil || workflow.repository == nil {
		return Plan{}, apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	if err := validatePrepareRequest(request); err != nil {
		return Plan{}, err
	}
	request.Arguments = append([]string(nil), request.Arguments...)
	request.ExpectedSessionID = ResumeSessionID(request.Arguments)
	return workflow.repository.PrepareLaunch(contextOrBackground(ctx), request)
}

// ResumeSessionID returns the only stable session identifier available before
// a transparent foreground launch. Other launches remain uncorrelated.
func ResumeSessionID(arguments []string) string {
	if len(arguments) < 2 || arguments[0] != "resume" || !validSessionID(arguments[1]) {
		return ""
	}
	return strings.ToLower(arguments[1])
}

func validSessionID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func (workflow *Workflow) Reconcile(ctx context.Context, inspector ProcessInspector) error {
	if workflow == nil || workflow.repository == nil {
		return apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	return workflow.repository.ReconcileManagedLaunches(contextOrBackground(ctx), inspector)
}

func (workflow *Workflow) MarkStarted(ctx context.Context, leaseID string, processID int) error {
	if workflow == nil || workflow.repository == nil || strings.TrimSpace(leaseID) == "" || processID <= 0 {
		return apperrors.New(apperrors.LaunchLeaseInvalid, ErrLeaseInvalid)
	}
	return workflow.repository.MarkManagedLaunchStarted(contextOrBackground(ctx), leaseID, processID)
}

func (workflow *Workflow) MarkExited(ctx context.Context, leaseID string, exitStatus int) error {
	if workflow == nil || workflow.repository == nil || strings.TrimSpace(leaseID) == "" {
		return apperrors.New(apperrors.LaunchLeaseInvalid, ErrLeaseInvalid)
	}
	if !ValidProcessStatus(exitStatus) {
		return apperrors.New(apperrors.LaunchProcessStatusInvalid, ErrProcessStatusInvalid)
	}
	return workflow.repository.MarkManagedLaunchExited(contextOrBackground(ctx), leaseID, exitStatus)
}

func ValidProcessStatus(status int) bool {
	return status >= 0 && uint64(status) <= uint64(^uint32(0))
}

func (workflow *Workflow) MarkAbandoned(ctx context.Context, leaseID string) error {
	if workflow == nil || workflow.repository == nil || strings.TrimSpace(leaseID) == "" {
		return apperrors.New(apperrors.LaunchLeaseInvalid, ErrLeaseInvalid)
	}
	return workflow.repository.MarkManagedLaunchAbandoned(contextOrBackground(ctx), leaseID)
}

func validatePrepareRequest(request PrepareRequest) error {
	if err := profile.ValidateAlias(request.Alias); err != nil {
		return err
	}
	if strings.TrimSpace(request.Executable) == "" || !filepath.IsAbs(request.Executable) {
		return apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	if strings.TrimSpace(request.WorkingDirectory) == "" || !filepath.IsAbs(request.WorkingDirectory) {
		return apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	checkpointValues := []string{request.CheckpointID, request.CheckpointRevision, request.SourceProfileID, request.BootSessionID, request.ProjectID}
	checkpointCount := 0
	for _, value := range checkpointValues[:4] {
		if strings.TrimSpace(value) != "" {
			checkpointCount++
		}
	}
	if checkpointCount != 0 && (checkpointCount != 4 || strings.TrimSpace(request.ProjectID) == "") {
		return apperrors.New(apperrors.LaunchPlanInvalid, ErrPlanInvalid)
	}
	return nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
