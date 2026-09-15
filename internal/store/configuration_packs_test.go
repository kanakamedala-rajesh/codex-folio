package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/launch"
)

func TestConfigurationPackStoreKeepsImmutableVersionsAndProfileLocalOverrides(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	home := addReadyProfile(t, stateStore, "profile-1", "Work")
	ctx := context.Background()
	base, err := configpack.NewDraft("shared", "1", map[string]string{
		"config/base.toml": "model = \"gpt-5\"\n",
	})
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	if err := stateStore.CreateConfigurationPack(ctx, base); err != nil {
		t.Fatalf("CreateConfigurationPack() error = %v", err)
	}
	approved, err := stateStore.ApproveConfigurationPack(ctx, base.ID, base.Version)
	if err != nil {
		t.Fatalf("ApproveConfigurationPack() error = %v", err)
	}
	if approved.State != configpack.StateApproved || approved.Digest != base.Digest {
		t.Fatalf("approved = %#v, want immutable approved version", approved)
	}
	assignment, err := stateStore.AssignConfigurationPack(ctx, "work", base.ID, base.Version)
	if err != nil {
		t.Fatalf("AssignConfigurationPack() error = %v", err)
	}
	if assignment.ProfileID != "profile-1" || assignment.Digest != base.Digest {
		t.Fatalf("assignment = %#v, want profile and digest", assignment)
	}
	if err := stateStore.SetConfigurationOverride(ctx, "WORK", "config/base.toml", "model = \"gpt-5-mini\"\n"); err != nil {
		t.Fatalf("SetConfigurationOverride() error = %v", err)
	}
	overrides, err := stateStore.GetConfigurationOverrides(ctx, "Work")
	if err != nil {
		t.Fatalf("GetConfigurationOverrides() error = %v", err)
	}
	if overrides["config/base.toml"] != "model = \"gpt-5-mini\"\n" {
		t.Fatalf("overrides = %#v, want local override", overrides)
	}
	stored, err := stateStore.GetConfigurationPack(ctx, base.ID, base.Version)
	if err != nil {
		t.Fatalf("GetConfigurationPack() error = %v", err)
	}
	if stored.State != configpack.StateApproved || stored.Files["config/base.toml"] != base.Files["config/base.toml"] {
		t.Fatalf("stored prior version = %#v, want unchanged", stored)
	}

	next, err := configpack.Promote(stored, overrides, "2", true)
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if err := stateStore.PromoteConfigurationPack(ctx, next, base.Version); err != nil {
		t.Fatalf("PromoteConfigurationPack() error = %v", err)
	}
	prior, err := stateStore.GetConfigurationPack(ctx, base.ID, base.Version)
	if err != nil {
		t.Fatalf("GetConfigurationPack(prior) error = %v", err)
	}
	if prior.State != configpack.StateApproved || prior.Files["config/base.toml"] != base.Files["config/base.toml"] {
		t.Fatalf("prior after promotion = %#v, want approved and unchanged", prior)
	}
	latest, err := stateStore.GetConfigurationPack(ctx, base.ID, "2")
	if err != nil {
		t.Fatalf("GetConfigurationPack(latest) error = %v", err)
	}
	if latest.State != configpack.StateApproved || latest.Files["config/base.toml"] != overrides["config/base.toml"] {
		t.Fatalf("latest = %#v, want approved promoted content", latest)
	}
	if got, err := stateStore.GetConfigurationPackAssignment(ctx, "work"); err != nil || got.Version != base.Version {
		t.Fatalf("assignment after promotion = %#v, error = %v, want explicit unchanged assignment", got, err)
	}
	later, err := configpack.NewDraft("shared", "3", map[string]string{"config/base.toml": "model = \"gpt-5\"\n", "guidance/AGENTS.md": "reviewed\n"})
	if err != nil {
		t.Fatalf("NewDraft(later) error = %v", err)
	}
	if err := stateStore.CreateConfigurationPack(ctx, later); err != nil {
		t.Fatalf("CreateConfigurationPack(later) error = %v", err)
	}
	if _, err := stateStore.ApproveConfigurationPack(ctx, later.ID, later.Version); err != nil {
		t.Fatalf("ApproveConfigurationPack(later) error = %v", err)
	}
	prior, err = stateStore.GetConfigurationPack(ctx, base.ID, base.Version)
	if err != nil || prior.State != configpack.StateApproved {
		t.Fatalf("prior after later approval = %#v/%v, want still approved", prior, err)
	}
	service, err := configpack.NewService(stateStore, configpack.NewProjector(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Project(ctx, "work"); err != nil {
		t.Fatalf("Project(existing assignment after promotion) error = %v", err)
	}
	if profileTarget, err := stateStore.GetConfigurationProfile(ctx, "WORK"); err != nil || profileTarget.IdentityHome != home || profileTarget.HomeOwnership != "managed" {
		t.Fatalf("profile target = %#v, error = %v, want managed home", profileTarget, err)
	}
}

func TestConfigurationPackStoreListsVersionMetadataWithoutLosingImmutableHistory(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	ctx := context.Background()
	for _, item := range []struct {
		id, version string
	}{
		{id: "shared", version: "1"},
		{id: "shared", version: "2"},
		{id: "team", version: "1"},
	} {
		pack, err := configpack.NewDraft(item.id, item.version, map[string]string{"config.toml": "model = \"gpt-5\"\n"})
		if err != nil {
			t.Fatal(err)
		}
		if err := stateStore.CreateConfigurationPack(ctx, pack); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stateStore.ApproveConfigurationPack(ctx, "shared", "1"); err != nil {
		t.Fatal(err)
	}

	packs, err := stateStore.ListConfigurationPacks(ctx)
	if err != nil {
		t.Fatalf("ListConfigurationPacks() error = %v", err)
	}
	if len(packs) != 3 {
		t.Fatalf("ListConfigurationPacks() = %#v, want all three immutable versions", packs)
	}
	if packs[0].ID != "shared" || packs[0].Version != "1" || packs[0].State != configpack.StateApproved || packs[1].Version != "2" || packs[2].ID != "team" {
		t.Fatalf("ListConfigurationPacks() = %#v, want stable id/version order and state", packs)
	}
}

type blockingProjector struct {
	entered chan struct{}
	release chan struct{}
}

func (projector blockingProjector) Preview(_ context.Context, _ string, files map[string]string) (configpack.ProjectionPlan, error) {
	return configpack.ProjectionPlan{Digest: configpack.DigestFiles(files), Files: []string{"config/base.toml"}, Conflicts: []configpack.Change{}}, nil
}

func (projector blockingProjector) Project(_ context.Context, _ string, files map[string]string) (configpack.ProjectionResult, error) {
	close(projector.entered)
	<-projector.release
	return configpack.ProjectionResult{Digest: configpack.DigestFiles(files)}, nil
}

func (projector blockingProjector) ProjectReviewed(ctx context.Context, home string, files map[string]string, _ []configpack.Change) (configpack.ProjectionResult, error) {
	return projector.Project(ctx, home, files)
}

func TestConfigurationProjectionExcludesConcurrentLaunchPreparation(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateStore.Close() }()
	home := addReadyProfile(t, stateStore, "profile-1", "Work")
	projector := blockingProjector{entered: make(chan struct{}), release: make(chan struct{})}
	service, err := configpack.NewService(stateStore, projector)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := service.CreateDraft(ctx, "shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, "shared", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Assign(ctx, "Work", "shared", "1"); err != nil {
		t.Fatal(err)
	}
	projected := make(chan error, 1)
	go func() { _, err := service.Project(ctx, "Work"); projected <- err }()
	<-projector.entered
	launched := make(chan error, 1)
	go func() {
		_, err := stateStore.PrepareLaunch(ctx, launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(home, "codex"), WorkingDirectory: home})
		launched <- err
	}()
	select {
	case err := <-launched:
		t.Fatalf("launch preparation completed during projection: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(projector.release)
	if err := <-projected; err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if err := <-launched; err != nil {
		t.Fatalf("PrepareLaunch() error = %v", err)
	}
}

func TestConfigurationPackStoreReportsMissingAssignment(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	addReadyProfile(t, stateStore, "profile-1", "Work")
	_, err = stateStore.GetConfigurationPackAssignment(context.Background(), "Work")
	if !errors.Is(err, configpack.ErrNoAssignment) {
		t.Fatalf("GetConfigurationPackAssignment() error = %v, want no assignment", err)
	}
}

func TestConfigurationPackVersionIDsDoNotConcatenateFields(t *testing.T) {
	first := configurationPackVersionID(configpack.Pack{ID: "ab", Version: "c"})
	second := configurationPackVersionID(configpack.Pack{ID: "a", Version: "bc"})
	if first == second {
		t.Fatalf("version IDs collide: %q", first)
	}
}

func TestConfigurationPackServiceProjectsIntoAssignedManagedHome(t *testing.T) {
	stateStore, err := openProfileTestStore(t)
	if err != nil {
		t.Fatalf("openProfileTestStore() error = %v", err)
	}
	defer func() { _ = stateStore.Close() }()
	home := addReadyProfile(t, stateStore, "profile-1", "Work")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	service, err := configpack.NewService(stateStore, configpack.NewProjector(nil))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	ctx := context.Background()
	if _, err := service.CreateDraft(ctx, "shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"}); err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if _, err := service.Approve(ctx, "shared", "1"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := service.Assign(ctx, "Work", "shared", "1"); err != nil {
		t.Fatalf("Assign() error = %v", err)
	}
	if err := service.SetOverride(ctx, "Work", "config/BASE.toml", "model = \"collision\"\n"); err == nil {
		t.Fatal("SetOverride(case-colliding path) error = nil, want rejection before persistence")
	}
	if err := service.SetOverride(ctx, "Work", "config/base.toml", "model = \"gpt-5-mini\"\n"); err != nil {
		t.Fatalf("SetOverride() error = %v", err)
	}
	plan, err := service.Preview(ctx, "Work")
	if err != nil || plan.Digest == "" || len(plan.Files) != 1 {
		t.Fatalf("Preview() = %#v, error = %v, want one redacted file entry", plan, err)
	}
	if _, err := service.Project(ctx, "Work"); err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(home, "config", "base.toml"))
	if err != nil || string(content) != "model = \"gpt-5-mini\"\n" {
		t.Fatalf("projected content = %q, error = %v, want local override precedence", content, err)
	}
}
