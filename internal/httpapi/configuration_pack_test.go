package httpapi

import (
	"context"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
)

func TestCommandConfigurationPackRequiresTokenAndReturnsReviewedPack(t *testing.T) {
	repository := &commandConfigurationPackRepository{packs: make(map[string]configpack.Pack), overrides: make(map[string]map[string]string)}
	service, err := configpack.NewService(repository, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server, _, _ := startTestServer(t, Options{ConfigurationPacks: service, CommandToken: "command-token"})
	request := CommandConfigurationPackRequest{
		Action: "create", PackID: "shared", Version: "1",
		Files: map[string]string{"config/base.toml": "model = \"gpt-5\"\n"},
	}
	if _, err := NewCommandClient(server.Origin(), "wrong-token", nil).ConfigurationPack(context.Background(), request); apperrors.Code(err) != apperrors.HTTPAPISessionInvalid {
		t.Fatalf("unauthorized ConfigurationPack() error = %v", err)
	}
	response, err := NewCommandClient(server.Origin(), "command-token", nil).ConfigurationPack(context.Background(), request)
	if err != nil {
		t.Fatalf("ConfigurationPack() error = %v", err)
	}
	if response.Pack == nil || response.Pack.ID != "shared" || response.Pack.Version != "1" || response.Pack.Digest == "" {
		t.Fatalf("configuration pack response = %#v, want reviewed pack metadata", response)
	}
}

type commandConfigurationPackRepository struct {
	packs     map[string]configpack.Pack
	assign    configpack.Assignment
	overrides map[string]map[string]string
}

func (repository *commandConfigurationPackRepository) CreateConfigurationPack(_ context.Context, pack configpack.Pack) error {
	repository.packs[packKey(pack.ID, pack.Version)] = pack
	return nil
}

func (repository *commandConfigurationPackRepository) GetConfigurationPack(_ context.Context, id, version string) (configpack.Pack, error) {
	pack, ok := repository.packs[packKey(id, version)]
	if !ok {
		return configpack.Pack{}, configpack.ErrNotFound
	}
	return pack, nil
}

func (repository *commandConfigurationPackRepository) ApproveConfigurationPack(ctx context.Context, id, version string) (configpack.Pack, error) {
	pack, err := repository.GetConfigurationPack(ctx, id, version)
	if err != nil {
		return configpack.Pack{}, err
	}
	pack.State = configpack.StateApproved
	repository.packs[packKey(id, version)] = pack
	return pack, nil
}

func (repository *commandConfigurationPackRepository) PromoteConfigurationPack(_ context.Context, pack configpack.Pack, _ string) error {
	repository.packs[packKey(pack.ID, pack.Version)] = pack
	return nil
}

func (repository *commandConfigurationPackRepository) AssignConfigurationPack(_ context.Context, alias, id, version string) (configpack.Assignment, error) {
	repository.assign = configpack.Assignment{ProfileID: "profile-1", Alias: alias, PackID: id, Version: version}
	return repository.assign, nil
}

func (repository *commandConfigurationPackRepository) GetConfigurationPackAssignment(context.Context, string) (configpack.Assignment, error) {
	if repository.assign.PackID == "" {
		return configpack.Assignment{}, configpack.ErrNoAssignment
	}
	return repository.assign, nil
}

func (repository *commandConfigurationPackRepository) GetConfigurationOverrides(_ context.Context, alias string) (map[string]string, error) {
	return repository.overrides[alias], nil
}

func (repository *commandConfigurationPackRepository) SetConfigurationOverride(_ context.Context, alias, path, content string) error {
	if repository.overrides[alias] == nil {
		repository.overrides[alias] = make(map[string]string)
	}
	repository.overrides[alias][path] = content
	return nil
}

func (repository *commandConfigurationPackRepository) GetConfigurationProfile(context.Context, string) (configpack.ProfileTarget, error) {
	return configpack.ProfileTarget{ID: "profile-1", Alias: "Work", Status: configpack.TargetStatusReady, HomeOwnership: configpack.TargetHomeOwnershipManaged, IdentityHome: tTempHome}, nil
}

func (repository *commandConfigurationPackRepository) WithStoppedConfigurationProfile(ctx context.Context, alias string, project func(configpack.ProfileTarget) error) error {
	target, err := repository.GetConfigurationProfile(ctx, alias)
	if err != nil {
		return err
	}
	return project(target)
}

var tTempHome = "/tmp/codex-folio-test-home"

func packKey(id, version string) string { return id + "@" + version }
