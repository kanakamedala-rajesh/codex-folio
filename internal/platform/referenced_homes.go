package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ReferencedHomeResolver canonicalizes an existing external Identity Home
// without changing its contents or permissions.
type ReferencedHomeResolver struct {
	managedRoots []string
}

func NewReferencedHomeResolver(managedRoots ...string) *ReferencedHomeResolver {
	resolver := &ReferencedHomeResolver{managedRoots: make([]string, 0, len(managedRoots))}
	for _, root := range managedRoots {
		if root = strings.TrimSpace(root); root != "" {
			resolver.managedRoots = append(resolver.managedRoots, root)
		}
	}
	return resolver
}

func (resolver *ReferencedHomeResolver) Resolve(ctx context.Context, path string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || isFilesystemRoot(filepath.Clean(path)) {
		return "", errors.New("referenced Identity Home path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", errors.New("referenced Identity Home could not be resolved")
	}
	resolved = filepath.Clean(resolved)
	if isFilesystemRoot(resolved) {
		return "", errors.New("referenced Identity Home path is invalid")
	}
	if resolver != nil {
		for _, root := range resolver.managedRoots {
			if pathWithin(resolved, canonicalExistingPath(root)) {
				return "", errors.New("referenced Identity Home path is managed by CodexFolio")
			}
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("referenced Identity Home is not a directory")
	}
	return resolved, nil
}

func canonicalExistingPath(path string) string {
	path = filepath.Clean(path)
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}
