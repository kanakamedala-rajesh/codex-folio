package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// ProjectPaths is the native repository-location adapter.
type ProjectPaths struct{}

func NewProjectPaths() ProjectPaths { return ProjectPaths{} }

func (ProjectPaths) Basename(path string) string { return filepath.Base(path) }

func (ProjectPaths) CanonicalRepository(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("repository location is not a directory")
	}
	for current := filepath.Clean(canonical); ; current = filepath.Dir(current) {
		marker, markerErr := os.Stat(filepath.Join(current, ".git"))
		if markerErr == nil && (marker.IsDir() || marker.Mode().IsRegular()) {
			return current, nil
		}
		if markerErr != nil && !os.IsNotExist(markerErr) {
			return "", markerErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository marker was not found")
		}
	}
}

func (ProjectPaths) SameRepository(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
