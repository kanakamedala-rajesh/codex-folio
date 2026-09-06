// Package activity owns app-local Project Identities and activity chronology.
package activity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

var (
	ErrProjectInvalid  = errors.New("Project Identity is invalid")
	ErrProjectNotFound = errors.New("Project Identity was not found")
	ErrPathInvalid     = errors.New("repository location is invalid")
	ErrPathCollision   = errors.New("repository location belongs to another Project Identity")
)

// ProjectRecord is the private persistence shape. CanonicalPath must never be
// returned from an ordinary projection.
type ProjectRecord struct {
	ID            string
	Alias         string
	Basename      string
	CanonicalPath string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProjectIdentity is the safe CLI and API projection.
type ProjectIdentity struct {
	ID        string    `json:"id"`
	Alias     string    `json:"alias"`
	Basename  string    `json:"basename"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProjectRepository interface {
	SaveProjectRecord(context.Context, ProjectRecord) error
	ListProjectRecords(context.Context) ([]ProjectRecord, error)
	ListProjectIdentities(context.Context) ([]ProjectIdentity, error)
	UpdateProjectAlias(context.Context, string, string, time.Time) error
}

// ProjectPaths resolves and compares repository locations through the native
// filesystem adapter.
type ProjectPaths interface {
	CanonicalRepository(string) (string, error)
	SameRepository(string, string) bool
	Basename(string) string
}

type ProjectServiceOptions struct {
	Repository ProjectRepository
	Paths      ProjectPaths
	Now        func() time.Time
	Random     io.Reader
}

type ProjectService struct {
	repository ProjectRepository
	paths      ProjectPaths
	now        func() time.Time
	random     io.Reader
	mu         sync.Mutex
}

func NewProjectService(options ProjectServiceOptions) (*ProjectService, error) {
	if options.Repository == nil || options.Paths == nil {
		return nil, apperrors.New(apperrors.ProjectIdentityInvalid, ErrProjectInvalid)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &ProjectService{repository: options.Repository, paths: options.Paths, now: options.Now, random: options.Random}, nil
}

func (service *ProjectService) Resolve(ctx context.Context, path, alias string) (ProjectIdentity, error) {
	canonical, err := service.canonicalRepository(path)
	if err != nil {
		return ProjectIdentity{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	records, err := service.repository.ListProjectRecords(contextOrBackground(ctx))
	if err != nil {
		return ProjectIdentity{}, err
	}
	for _, record := range records {
		if service.paths.SameRepository(record.CanonicalPath, canonical) {
			if record.Basename == "" {
				record.Basename = service.paths.Basename(canonical)
				if err := service.repository.SaveProjectRecord(contextOrBackground(ctx), record); err != nil {
					return ProjectIdentity{}, err
				}
			}
			return projectIdentity(record), nil
		}
	}
	if strings.TrimSpace(alias) == "" {
		alias = service.paths.Basename(canonical)
	}
	alias, err = validAlias(alias)
	if err != nil {
		return ProjectIdentity{}, err
	}
	id, err := newProjectID(service.random)
	if err != nil {
		return ProjectIdentity{}, apperrors.New(apperrors.ProjectIdentityInvalid, err)
	}
	now := service.now().UTC()
	record := ProjectRecord{ID: id, Alias: alias, Basename: service.paths.Basename(canonical), CanonicalPath: canonical, CreatedAt: now, UpdatedAt: now}
	if err := service.repository.SaveProjectRecord(contextOrBackground(ctx), record); err != nil {
		return ProjectIdentity{}, err
	}
	return projectIdentity(record), nil
}

func (service *ProjectService) List(ctx context.Context) ([]ProjectIdentity, error) {
	return service.repository.ListProjectIdentities(contextOrBackground(ctx))
}

func (service *ProjectService) EditAlias(ctx context.Context, id, alias string) (ProjectIdentity, error) {
	alias, err := validAlias(alias)
	if err != nil {
		return ProjectIdentity{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, err := service.findSafe(contextOrBackground(ctx), id)
	if err != nil {
		return ProjectIdentity{}, err
	}
	record.Alias = alias
	record.UpdatedAt = service.now().UTC()
	if err := service.repository.UpdateProjectAlias(contextOrBackground(ctx), id, alias, record.UpdatedAt); err != nil {
		return ProjectIdentity{}, err
	}
	return record, nil
}

func (service *ProjectService) Reconcile(ctx context.Context, id, path string) (ProjectIdentity, error) {
	canonical, err := service.canonicalRepository(path)
	if err != nil {
		return ProjectIdentity{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, records, err := service.find(contextOrBackground(ctx), id)
	if err != nil {
		return ProjectIdentity{}, err
	}
	for _, candidate := range records {
		if candidate.ID != id && service.paths.SameRepository(candidate.CanonicalPath, canonical) {
			return ProjectIdentity{}, apperrors.New(apperrors.ProjectPathCollision, ErrPathCollision)
		}
	}
	if service.paths.SameRepository(record.CanonicalPath, canonical) {
		return projectIdentity(record), nil
	}
	record.CanonicalPath = canonical
	record.Basename = service.paths.Basename(canonical)
	record.UpdatedAt = service.now().UTC()
	if err := service.repository.SaveProjectRecord(contextOrBackground(ctx), record); err != nil {
		return ProjectIdentity{}, err
	}
	return projectIdentity(record), nil
}

// CanonicalLocation is the explicit private-path boundary for workflows that
// need repository filesystem access.
func (service *ProjectService) CanonicalLocation(ctx context.Context, id string) (string, error) {
	record, _, err := service.find(contextOrBackground(ctx), id)
	return record.CanonicalPath, err
}

func (service *ProjectService) find(ctx context.Context, id string) (ProjectRecord, []ProjectRecord, error) {
	records, err := service.repository.ListProjectRecords(ctx)
	if err != nil {
		return ProjectRecord{}, nil, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, records, nil
		}
	}
	return ProjectRecord{}, records, apperrors.New(apperrors.ProjectIdentityNotFound, ErrProjectNotFound)
}

func (service *ProjectService) findSafe(ctx context.Context, id string) (ProjectIdentity, error) {
	projects, err := service.repository.ListProjectIdentities(ctx)
	if err != nil {
		return ProjectIdentity{}, err
	}
	for _, project := range projects {
		if project.ID == id {
			return project, nil
		}
	}
	return ProjectIdentity{}, apperrors.New(apperrors.ProjectIdentityNotFound, ErrProjectNotFound)
}

func (service *ProjectService) canonicalRepository(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", apperrors.New(apperrors.ProjectPathInvalid, ErrPathInvalid)
	}
	canonical, err := service.paths.CanonicalRepository(path)
	if err != nil {
		return "", apperrors.New(apperrors.ProjectPathInvalid, ErrPathInvalid)
	}
	return canonical, nil
}

func validAlias(alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" || len(alias) > 120 || strings.IndexFunc(alias, unicode.IsControl) >= 0 {
		return "", apperrors.New(apperrors.ProjectIdentityInvalid, ErrProjectInvalid)
	}
	return alias, nil
}

func projectIdentity(record ProjectRecord) ProjectIdentity {
	return ProjectIdentity{ID: record.ID, Alias: record.Alias, Basename: record.Basename, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func newProjectID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return hex.EncodeToString(value[0:4]) + "-" + hex.EncodeToString(value[4:6]) + "-" + hex.EncodeToString(value[6:8]) + "-" + hex.EncodeToString(value[8:10]) + "-" + hex.EncodeToString(value[10:16]), nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
