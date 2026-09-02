package launch

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

type launchRepositoryStub struct {
	request PrepareRequest
	plan    Plan
}

func (repository *launchRepositoryStub) PrepareLaunch(_ context.Context, request PrepareRequest) (Plan, error) {
	repository.request = request
	return repository.plan, nil
}

func (*launchRepositoryStub) MarkManagedLaunchStarted(context.Context, string, int) error {
	return nil
}

func (*launchRepositoryStub) MarkManagedLaunchExited(context.Context, string, int) error {
	return nil
}

func (*launchRepositoryStub) MarkManagedLaunchAbandoned(context.Context, string) error {
	return nil
}

func (*launchRepositoryStub) ReconcileManagedLaunches(context.Context, ProcessInspector) error {
	return nil
}

func TestWorkflowPreparePreservesValidatedLaunchInputs(t *testing.T) {
	repository := &launchRepositoryStub{plan: Plan{LeaseID: "lease-1"}}
	workflow, err := NewWorkflow(WorkflowOptions{Repository: repository})
	if err != nil {
		t.Fatalf("NewWorkflow() error = %v", err)
	}

	request := PrepareRequest{
		Alias:            "Work",
		Executable:       filepath.Join(string(filepath.Separator), "opt", "codex"),
		WorkingDirectory: filepath.Join(string(filepath.Separator), "workspace"),
		Arguments:        []string{"--model", "value with spaces"},
	}
	plan, err := workflow.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if plan.LeaseID != "lease-1" {
		t.Fatalf("plan = %#v, want repository plan", plan)
	}
	if !reflect.DeepEqual(repository.request, request) {
		t.Fatalf("repository request = %#v, want %#v", repository.request, request)
	}
}

func TestWorkflowPrepareRejectsRelativeExecutableAndWorkingDirectory(t *testing.T) {
	workflow, err := NewWorkflow(WorkflowOptions{Repository: &launchRepositoryStub{}})
	if err != nil {
		t.Fatalf("NewWorkflow() error = %v", err)
	}

	for name, request := range map[string]PrepareRequest{
		"relative executable": {
			Alias:            "work",
			Executable:       "codex",
			WorkingDirectory: filepath.Join(string(filepath.Separator), "workspace"),
		},
		"relative working directory": {
			Alias:            "work",
			Executable:       filepath.Join(string(filepath.Separator), "opt", "codex"),
			WorkingDirectory: "workspace",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := workflow.Prepare(context.Background(), request); err == nil {
				t.Fatal("Prepare() error = nil, want validation error")
			} else if !errors.Is(err, ErrPlanInvalid) {
				t.Fatalf("Prepare() error = %v, want ErrPlanInvalid", err)
			}
		})
	}
}
