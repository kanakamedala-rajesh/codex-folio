package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/activity"
)

type projectRepository struct{ records []activity.ProjectRecord }

type projectTestPaths struct{}

func (projectTestPaths) CanonicalRepository(path string) (string, error) {
	return filepath.Clean(path), nil
}

func (projectTestPaths) SameRepository(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func (projectTestPaths) Basename(path string) string { return filepath.Base(path) }

func (repository *projectRepository) SaveProjectRecord(_ context.Context, record activity.ProjectRecord) error {
	for index := range repository.records {
		if repository.records[index].ID == record.ID {
			repository.records[index] = record
			return nil
		}
	}
	repository.records = append(repository.records, record)
	return nil
}

func (repository *projectRepository) ListProjectRecords(context.Context) ([]activity.ProjectRecord, error) {
	return append([]activity.ProjectRecord(nil), repository.records...), nil
}

func (repository *projectRepository) ListProjectIdentities(context.Context) ([]activity.ProjectIdentity, error) {
	projects := make([]activity.ProjectIdentity, 0, len(repository.records))
	for _, record := range repository.records {
		projects = append(projects, activity.ProjectIdentity{ID: record.ID, Alias: record.Alias, Basename: record.Basename, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt})
	}
	return projects, nil
}

func (repository *projectRepository) UpdateProjectAlias(_ context.Context, id, alias string, updatedAt time.Time) error {
	for index := range repository.records {
		if repository.records[index].ID == id {
			repository.records[index].Alias = alias
			repository.records[index].UpdatedAt = updatedAt
			return nil
		}
	}
	return activity.ErrProjectNotFound
}

func TestProjectAPIsReturnOnlySafeProjections(t *testing.T) {
	repository := &projectRepository{}
	projects, err := activity.NewProjectService(activity.ProjectServiceOptions{Repository: repository, Paths: projectTestPaths{}, Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))})
	if err != nil {
		t.Fatal(err)
	}
	server, _, _ := startTestServer(t, Options{Projects: projects, CommandToken: "project-token"})
	root := t.TempDir()
	first := filepath.Join(root, "private", "first")
	second := filepath.Join(root, "private", "second")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(path, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	command := NewCommandClient(server.Origin(), "project-token", nil)
	created, err := command.Project(context.Background(), CommandProjectRequest{Action: "resolve", Path: first, Alias: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.Project(context.Background(), CommandProjectRequest{Action: "edit", ID: created.Project.ID, Alias: "Renamed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Project(context.Background(), CommandProjectRequest{Action: "reconcile", ID: created.Project.ID, Path: second}); err != nil {
		t.Fatal(err)
	}
	located, err := command.Project(context.Background(), CommandProjectRequest{Action: "locate", ID: created.Project.ID})
	if err != nil || located.Path != second {
		t.Fatalf("located project = %#v, %v", located, err)
	}
	listed, err := command.Project(context.Background(), CommandProjectRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(listed)
	if len(listed.Projects) != 1 || listed.Projects[0].Alias != "Renamed" || listed.Projects[0].Basename != "second" || strings.Contains(string(encoded), root) {
		t.Fatalf("command projection = %s", encoded)
	}

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
	result, response, err := NewClient(origin, client).GetProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	generated, _ := json.Marshal(result)
	if len(result.Projects) != 1 || result.Projects[0].ProjectId != created.Project.ID || strings.Contains(string(generated), root) {
		t.Fatalf("generated API projection = %s", generated)
	}
}
