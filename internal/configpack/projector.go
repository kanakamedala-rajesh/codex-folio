package configpack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const projectionManifest = ".codex-folio-projection.json"

// FileSystem is the small filesystem seam needed to prove projection failure
// does not modify an existing Identity Home.
type FileSystem interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, os.FileMode) error
	MkdirAll(string, os.FileMode) error
	MkdirTemp(string, string) (string, error)
	Rename(string, string) error
	Remove(string) error
	RemoveAll(string) error
	Lstat(string) (os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (osFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (osFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}
func (osFileSystem) MkdirTemp(dir, pattern string) (string, error) {
	return os.MkdirTemp(dir, pattern)
}
func (osFileSystem) Rename(oldPath, newPath string) error   { return os.Rename(oldPath, newPath) }
func (osFileSystem) Remove(name string) error               { return os.Remove(name) }
func (osFileSystem) RemoveAll(path string) error            { return os.RemoveAll(path) }
func (osFileSystem) Lstat(name string) (os.FileInfo, error) { return os.Lstat(name) }
func (osFileSystem) Stat(name string) (os.FileInfo, error)  { return os.Stat(name) }

type filesystemProjector struct {
	fs FileSystem
}

func NewProjector(fs FileSystem) Projector {
	if fs == nil {
		fs = osFileSystem{}
	}
	return &filesystemProjector{fs: fs}
}

type priorFile struct {
	path      string
	backup    string
	exists    bool
	install   bool
	moved     bool
	installed bool
	preserve  bool
}

func (projector *filesystemProjector) Preview(ctx context.Context, home string, files map[string]string) (ProjectionPlan, error) {
	if projector == nil || projector.fs == nil {
		return ProjectionPlan{}, projectionError(errors.New("projector is unavailable"))
	}
	if err := contextError(ctx); err != nil {
		return ProjectionPlan{}, projectionError(err)
	}
	if err := validateHome(home); err != nil {
		return ProjectionPlan{}, err
	}
	if err := validateFiles(files); err != nil {
		return ProjectionPlan{}, err
	}
	info, err := projector.fs.Stat(home)
	if err != nil || !info.IsDir() {
		return ProjectionPlan{}, projectionError(errors.Join(err, errors.New("Identity Home is not a directory")))
	}
	homeInfo, err := projector.fs.Lstat(home)
	if err != nil || homeInfo.Mode()&os.ModeSymlink != 0 {
		return ProjectionPlan{}, projectionError(errors.New("Identity Home must not be a symbolic link"))
	}
	previous, err := projector.previousProjection(home)
	if err != nil {
		return ProjectionPlan{}, err
	}
	affected := make(map[string]struct{}, len(files)+len(previous))
	for path := range files {
		affected[path] = struct{}{}
	}
	for path := range previous {
		affected[path] = struct{}{}
	}
	paths := make([]string, 0, len(affected))
	for path := range affected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	conflicts := make([]Change, 0)
	for _, path := range paths {
		if err := contextError(ctx); err != nil {
			return ProjectionPlan{}, projectionError(err)
		}
		if err := projector.rejectSymlinkParents(home, path); err != nil {
			return ProjectionPlan{}, err
		}
		target := filepath.Join(home, filepath.FromSlash(path))
		info, statErr := projector.fs.Stat(target)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return ProjectionPlan{}, projectionError(statErr)
		}
		if info.IsDir() {
			return ProjectionPlan{}, projectionError(errors.New("projection target is a directory"))
		}
		content, err := projector.fs.ReadFile(target)
		if err != nil {
			return ProjectionPlan{}, projectionError(err)
		}
		desired, installing := files[path]
		previousDigest, tracked := previous[path]
		if installing && string(content) == desired || tracked && previousDigest != "" && contentDigest(string(content)) == previousDigest {
			continue
		}
		kind := ChangeModified
		if !installing {
			kind = ChangeRemoved
		}
		conflicts = append(conflicts, Change{Path: path, Kind: kind})
	}
	return ProjectionPlan{Digest: DigestFiles(files), Files: sortedPaths(files), Conflicts: conflicts}, nil
}

func (projector *filesystemProjector) Project(ctx context.Context, home string, files map[string]string) (ProjectionResult, error) {
	return projector.project(ctx, home, files, nil, false)
}

func (projector *filesystemProjector) ProjectReviewed(ctx context.Context, home string, files map[string]string, expectedConflicts []Change) (ProjectionResult, error) {
	return projector.project(ctx, home, files, expectedConflicts, true)
}

func (projector *filesystemProjector) project(ctx context.Context, home string, files map[string]string, expectedConflicts []Change, enforceReview bool) (ProjectionResult, error) {
	if projector == nil || projector.fs == nil {
		return ProjectionResult{}, projectionError(errors.New("projector is unavailable"))
	}
	if err := contextError(ctx); err != nil {
		return ProjectionResult{}, projectionError(err)
	}
	if err := validateHome(home); err != nil {
		return ProjectionResult{}, err
	}
	if err := validateFiles(files); err != nil {
		return ProjectionResult{}, err
	}
	info, err := projector.fs.Stat(home)
	if err != nil {
		return ProjectionResult{}, projectionError(err)
	}
	if !info.IsDir() {
		return ProjectionResult{}, projectionError(errors.New("Identity Home is not a directory"))
	}
	homeInfo, err := projector.fs.Lstat(home)
	if err != nil || homeInfo.Mode()&os.ModeSymlink != 0 {
		return ProjectionResult{}, projectionError(errors.New("Identity Home must not be a symbolic link"))
	}

	paths := sortedPaths(files)
	previous, err := projector.previousProjection(home)
	if err != nil {
		return ProjectionResult{}, err
	}
	affected := make(map[string]bool, len(paths)+len(previous)+1)
	for path := range previous {
		affected[path] = false
	}
	for _, path := range paths {
		affected[path] = true
	}
	affected[projectionManifest] = true
	affectedPaths := make([]string, 0, len(affected))
	for path := range affected {
		affectedPaths = append(affectedPaths, path)
	}
	sort.Strings(affectedPaths)

	prior := make([]priorFile, len(affectedPaths))
	actualConflicts := make([]Change, 0)
	manifestEntries := make(map[string]string, len(paths))
	for path, content := range files {
		manifestEntries[path] = contentDigest(content)
	}
	for index, path := range affectedPaths {
		target := filepath.Join(home, filepath.FromSlash(path))
		if err := projector.rejectSymlinkParents(home, path); err != nil {
			return ProjectionResult{}, err
		}
		prior[index].path = target
		prior[index].install = affected[path]
		info, statErr := projector.fs.Stat(target)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
		case statErr != nil:
			return ProjectionResult{}, projectionError(statErr)
		case info.IsDir():
			return ProjectionResult{}, projectionError(errors.New("projection target is a directory"))
		default:
			prior[index].exists = true
			content, readErr := projector.fs.ReadFile(target)
			if readErr != nil {
				return ProjectionResult{}, projectionError(readErr)
			}
			previousDigest, tracked := previous[path]
			desired, installing := files[path]
			if path != projectionManifest && (!tracked || previousDigest == "" || contentDigest(string(content)) != previousDigest) && (!installing || string(content) != desired) {
				prior[index].preserve = true
				delete(manifestEntries, path)
				kind := ChangeModified
				if !installing {
					kind = ChangeRemoved
				}
				actualConflicts = append(actualConflicts, Change{Path: path, Kind: kind})
			}
		}
	}
	if enforceReview && !sameChanges(actualConflicts, expectedConflicts) {
		return ProjectionResult{}, apperrors.New(apperrors.ConfigurationPackInvalid, ErrInvalid)
	}

	stage, err := projector.fs.MkdirTemp(home, ".codex-folio-projection-")
	if err != nil {
		return ProjectionResult{}, projectionError(err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = projector.fs.RemoveAll(stage)
		}
	}()
	for index := range prior {
		prior[index].backup = filepath.Join(stage, "prior", filepath.FromSlash(affectedPaths[index]))
	}
	for _, path := range paths {
		if err := contextError(ctx); err != nil {
			return ProjectionResult{}, projectionError(err)
		}
		stagedPath := filepath.Join(stage, "next", filepath.FromSlash(path))
		if err := projector.fs.MkdirAll(filepath.Dir(stagedPath), 0o700); err != nil {
			return ProjectionResult{}, projectionError(err)
		}
		if err := projector.fs.WriteFile(stagedPath, []byte(files[path]), 0o600); err != nil {
			return ProjectionResult{}, projectionError(err)
		}
	}
	manifest, err := json.Marshal(manifestEntries)
	if err != nil {
		return ProjectionResult{}, projectionError(err)
	}
	manifestStage := filepath.Join(stage, "next", projectionManifest)
	if err := projector.fs.MkdirAll(filepath.Dir(manifestStage), 0o700); err != nil {
		return ProjectionResult{}, projectionError(err)
	}
	if err := projector.fs.WriteFile(manifestStage, manifest, 0o600); err != nil {
		return ProjectionResult{}, projectionError(err)
	}

	applied := make([]int, 0, len(affectedPaths))
	for index, path := range affectedPaths {
		if prior[index].preserve {
			continue
		}
		applied = append(applied, index)
		if err := contextError(ctx); err != nil {
			keepStage = !projector.rollback(applied, prior)
			return ProjectionResult{}, projectionError(err)
		}
		target := prior[index].path
		if prior[index].exists {
			if err := projector.fs.MkdirAll(filepath.Dir(prior[index].backup), 0o700); err != nil {
				keepStage = !projector.rollback(applied, prior)
				return ProjectionResult{}, projectionError(err)
			}
			if err := projector.fs.Rename(target, prior[index].backup); err != nil {
				keepStage = !projector.rollback(applied, prior)
				return ProjectionResult{}, projectionError(err)
			}
			prior[index].moved = true
		}
		if prior[index].install {
			if err := projector.fs.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				keepStage = !projector.rollback(applied, prior)
				return ProjectionResult{}, projectionError(err)
			}
			stagedPath := filepath.Join(stage, "next", filepath.FromSlash(path))
			if err := projector.fs.Rename(stagedPath, target); err != nil {
				keepStage = !projector.rollback(applied, prior)
				return ProjectionResult{}, projectionError(err)
			}
			prior[index].installed = true
		}
	}

	return ProjectionResult{Digest: DigestFiles(files), Files: paths}, nil
}

func sameChanges(left, right []Change) bool {
	left = sortedChanges(left)
	right = sortedChanges(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (projector *filesystemProjector) previousProjection(home string) (map[string]string, error) {
	encoded, err := projector.fs.ReadFile(filepath.Join(home, projectionManifest))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, projectionError(err)
	}
	paths := make(map[string]string)
	if json.Unmarshal(encoded, &paths) != nil {
		var legacy []string
		if json.Unmarshal(encoded, &legacy) != nil || len(legacy) > maxFiles {
			return nil, projectionError(ErrProjectionFailed)
		}
		for _, path := range legacy {
			paths[path] = ""
		}
	}
	if len(paths) > maxFiles {
		return nil, projectionError(ErrProjectionFailed)
	}
	seen := make(map[string]struct{}, len(paths))
	for path, digest := range paths {
		if validatePath(path) != nil {
			return nil, projectionError(ErrProjectionFailed)
		}
		if digest != "" && (len(digest) != 64 || !isHex(digest)) {
			return nil, projectionError(ErrProjectionFailed)
		}
		normalized := strings.ToLower(filepath.ToSlash(path))
		if _, exists := seen[normalized]; exists {
			return nil, projectionError(ErrProjectionFailed)
		}
		seen[normalized] = struct{}{}
	}
	return paths, nil
}

func contentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func (projector *filesystemProjector) rejectSymlinkParents(home, path string) error {
	current := home
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, err := projector.fs.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return projectionError(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return projectionError(errors.New("projection path must not contain a symbolic link"))
		}
	}
	return nil
}

func (projector *filesystemProjector) rollback(applied []int, prior []priorFile) bool {
	restored := true
	for index := len(applied) - 1; index >= 0; index-- {
		item := &prior[applied[index]]
		if item.installed && projector.fs.Remove(item.path) != nil {
			restored = false
		}
		if item.moved && projector.fs.Rename(item.backup, item.path) != nil {
			restored = false
		}
	}
	return restored
}

func validateHome(home string) error {
	home = strings.TrimSpace(home)
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) == string(filepath.Separator) {
		return apperrors.New(apperrors.ConfigurationPackProjectionFailed, ErrProjectionFailed)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func projectionError(err error) error {
	return apperrors.New(apperrors.ConfigurationPackProjectionFailed, errors.Join(ErrProjectionFailed, err))
}
