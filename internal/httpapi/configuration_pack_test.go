package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
)

func TestBrowserConfigurationPacksExposeReviewedMetadataAndRequireCSRF(t *testing.T) {
	repository := &commandConfigurationPackRepository{packs: make(map[string]configpack.Pack), overrides: make(map[string]map[string]string)}
	projector := &browserConfigurationPackProjector{conflicts: []configpack.Change{{Path: "config.toml", Kind: configpack.ChangeModified}}}
	service, err := configpack.NewService(repository, projector)
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{ConfigurationPacks: service})
	client := testClient(t)
	origin := server.Origin()
	token := mustBootstrapToken(t, server.BootstrapURL())
	exchange, err := doRequest(client, http.MethodPost, origin+BootstrapPath, server.Address(), origin, []byte(`{"bootstrap_token":"`+token+`"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap BootstrapResponse
	if err := json.NewDecoder(exchange.Body).Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	_ = exchange.Body.Close()

	withoutCSRF, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"create","pack_id":"shared","version":"1","documents":[{"kind":"config","content":"model = \"gpt-5\"\n"}]}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d, want %d", withoutCSRF.StatusCode, http.StatusForbidden)
	}
	_ = withoutCSRF.Body.Close()

	created, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"create","pack_id":"shared","version":"1","documents":[{"kind":"config","content":"model = \"gpt-5\"\n"}]}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	createdBody := readBody(t, created)
	if created.StatusCode != http.StatusOK || !strings.Contains(createdBody, `"state":"draft"`) || strings.Contains(createdBody, "gpt-5") || strings.Contains(createdBody, `"content"`) {
		t.Fatalf("created status/body = %d/%s, want metadata without declarative content", created.StatusCode, createdBody)
	}

	invalid, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"create","pack_id":"unsafe","version":"1","path":"../../auth.json","documents":[{"kind":"config","content":"model = \"gpt-5\"\n"}]}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	invalidBody := readBody(t, invalid)
	if invalid.StatusCode != http.StatusBadRequest || !strings.Contains(invalidBody, `"code":"CF_CONFIGPACK_INVALID"`) {
		t.Fatalf("arbitrary path status/body = %d/%s", invalid.StatusCode, invalidBody)
	}

	approved, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"approve","pack_id":"shared","version":"1","reviewed":true}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	approvedBody := readBody(t, approved)
	if approved.StatusCode != http.StatusOK || !strings.Contains(approvedBody, `"state":"approved"`) {
		t.Fatalf("approved status/body = %d/%s", approved.StatusCode, approvedBody)
	}

	assigned, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"assign","alias":"Work","pack_id":"shared","version":"1","reviewed":true}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	assignedBody := readBody(t, assigned)
	if assigned.StatusCode != http.StatusOK || !strings.Contains(assignedBody, `"alias":"Work"`) {
		t.Fatalf("assigned status/body = %d/%s", assigned.StatusCode, assignedBody)
	}

	preview, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"preview","alias":"Work"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	previewBody := readBody(t, preview)
	if preview.StatusCode != http.StatusOK || !strings.Contains(previewBody, `"conflicts":[{"path":"config.toml","kind":"modified"}]`) {
		t.Fatalf("preview status/body = %d/%s", preview.StatusCode, previewBody)
	}
	var previewResponse struct {
		Plan *configpack.ProjectionPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(previewBody), &previewResponse); err != nil || previewResponse.Plan == nil {
		t.Fatalf("decode preview = %#v/%v", previewResponse, err)
	}
	projector.conflicts = []configpack.Change{{Path: "AGENTS.md", Kind: configpack.ChangeAdded}}
	conflictChanged, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"apply","alias":"Work","reviewed":true,"expected_digest":"`+previewResponse.Plan.Digest+`"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if conflictChanged.StatusCode != http.StatusBadRequest || projector.projects != 0 {
		t.Fatalf("changed conflict snapshot status/projects = %d/%d, want rejected before projection", conflictChanged.StatusCode, projector.projects)
	}
	_ = conflictChanged.Body.Close()
	projector.conflicts = []configpack.Change{{Path: "config.toml", Kind: configpack.ChangeModified}}
	rejected, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"apply","alias":"Work","reviewed":true,"expected_digest":"stale"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.StatusCode != http.StatusBadRequest {
		t.Fatalf("stale projection status = %d, want %d", rejected.StatusCode, http.StatusBadRequest)
	}
	_ = rejected.Body.Close()

	application, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"apply","alias":"Work","reviewed":true,"expected_digest":"`+previewResponse.Plan.Digest+`"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	applicationBody := readBody(t, application)
	if application.StatusCode != http.StatusOK || !strings.Contains(applicationBody, `"projection"`) {
		t.Fatalf("application status/body = %d/%s", application.StatusCode, applicationBody)
	}

	repository.overrides["Work"] = map[string]string{"config.toml": "model = \"gpt-5-mini\"\n"}
	promotionPreview, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"promotion-preview","alias":"Work","version":"2"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	var promotionResponse struct {
		Promotion *configpack.PromotionPreview `json:"promotion_preview"`
	}
	if err := json.NewDecoder(promotionPreview.Body).Decode(&promotionResponse); err != nil {
		t.Fatal(err)
	}
	_ = promotionPreview.Body.Close()
	if promotionPreview.StatusCode != http.StatusOK || promotionResponse.Promotion == nil || len(promotionResponse.Promotion.Changes) != 1 {
		t.Fatalf("promotion preview status/response = %d/%#v", promotionPreview.StatusCode, promotionResponse)
	}
	repository.overrides["Work"] = map[string]string{"config.toml": "model = \"gpt-5-nano\"\n"}
	changedOverride, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"promote","alias":"Work","version":"2","reviewed":true,"expected_digest":"`+promotionResponse.Promotion.Digest+`"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, published := repository.packs[packKey("shared", "2")]; changedOverride.StatusCode != http.StatusBadRequest || published {
		t.Fatalf("changed override status/published = %d/%t, want rejected before publication", changedOverride.StatusCode, published)
	}
	_ = changedOverride.Body.Close()
	repository.overrides["Work"] = map[string]string{"config.toml": "model = \"gpt-5-mini\"\n"}
	promoted, err := doRequest(client, http.MethodPost, origin+"/api/v1/configuration-packs", server.Address(), origin, []byte(`{"action":"promote","alias":"Work","version":"2","reviewed":true,"expected_digest":"`+promotionResponse.Promotion.Digest+`"}`), bootstrap.CSRFToken)
	if err != nil {
		t.Fatal(err)
	}
	promotedBody := readBody(t, promoted)
	if promoted.StatusCode != http.StatusOK || !strings.Contains(promotedBody, `"version":"2"`) || repository.assign.Version != "1" {
		t.Fatalf("promoted status/body/assignment = %d/%s/%#v, want new immutable version and unchanged assignment", promoted.StatusCode, promotedBody, repository.assign)
	}

	inventory, err := doRequest(client, http.MethodGet, origin+"/api/v1/configuration-packs", server.Address(), origin, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	inventoryBody := readBody(t, inventory)
	if inventory.StatusCode != http.StatusOK || !strings.Contains(inventoryBody, `"packs":[`) || strings.Contains(inventoryBody, "gpt-5") {
		t.Fatalf("inventory status/body = %d/%s", inventory.StatusCode, inventoryBody)
	}
}

type browserConfigurationPackProjector struct {
	conflicts []configpack.Change
	projects  int
}

func (projector *browserConfigurationPackProjector) Preview(context.Context, string, map[string]string) (configpack.ProjectionPlan, error) {
	return configpack.ProjectionPlan{Conflicts: append([]configpack.Change(nil), projector.conflicts...)}, nil
}

func (projector *browserConfigurationPackProjector) Project(_ context.Context, _ string, files map[string]string) (configpack.ProjectionResult, error) {
	projector.projects++
	return configpack.ProjectionResult{Digest: configpack.DigestFiles(files), Files: []string{"config.toml"}}, nil
}

func (projector *browserConfigurationPackProjector) ProjectReviewed(ctx context.Context, home string, files map[string]string, _ []configpack.Change) (configpack.ProjectionResult, error) {
	return projector.Project(ctx, home, files)
}

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

func (repository *commandConfigurationPackRepository) ListConfigurationPacks(context.Context) ([]configpack.Pack, error) {
	packs := make([]configpack.Pack, 0, len(repository.packs))
	for _, pack := range repository.packs {
		packs = append(packs, pack)
	}
	return packs, nil
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
