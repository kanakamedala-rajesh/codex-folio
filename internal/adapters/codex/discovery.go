// Package codex contains adapters for the installed Codex executable.
package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/launch"
)

const versionCommandTimeout = 5 * time.Second

type ResolverOptions struct {
	PathEnvironment string
}

type Resolver struct {
	pathEnvironment string
}

func NewResolver(options ResolverOptions) *Resolver {
	pathEnvironment := options.PathEnvironment
	if pathEnvironment == "" {
		pathEnvironment = os.Getenv("PATH")
	}
	return &Resolver{pathEnvironment: pathEnvironment}
}

func (resolver *Resolver) Resolve(override string) (launch.Candidate, error) {
	if resolver == nil {
		return launch.Candidate{}, apperrors.New(apperrors.LaunchCodexPathInvalid, errors.New("Codex resolver is unavailable"))
	}
	if strings.TrimSpace(override) != "" {
		return resolver.resolveExplicit(strings.TrimSpace(override))
	}
	return resolver.resolvePATH()
}

func (resolver *Resolver) resolveExplicit(path string) (launch.Candidate, error) {
	if !filepath.IsAbs(path) {
		return launch.Candidate{}, invalidPath()
	}
	if !resolver.isExecutableFile(path) {
		return launch.Candidate{}, invalidPath()
	}
	return resolver.validate(path)
}

func (resolver *Resolver) resolvePATH() (launch.Candidate, error) {
	var candidates []string
	seen := make(map[string]struct{})
	invalid := false
	for _, directory := range filepath.SplitList(resolver.pathEnvironment) {
		if directory == "" {
			directory = "."
		}
		absoluteDirectory, err := filepath.Abs(directory)
		if err != nil {
			invalid = true
			continue
		}
		for _, name := range candidateNames() {
			path := filepath.Join(absoluteDirectory, name)
			info, statErr := os.Stat(path)
			if statErr != nil {
				if !errors.Is(statErr, os.ErrNotExist) {
					invalid = true
				}
				continue
			}
			if !isExecutable(info) {
				invalid = true
				continue
			}
			path = filepath.Clean(path)
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			candidates = append(candidates, path)
		}
	}

	switch len(candidates) {
	case 0:
		if invalid {
			return launch.Candidate{}, invalidPath()
		}
		return launch.Candidate{}, apperrors.New(apperrors.LaunchCodexNotFound, errors.New("Codex was not found on PATH"))
	case 1:
		return resolver.validate(candidates[0])
	default:
		return launch.Candidate{}, apperrors.New(apperrors.LaunchCodexAmbiguous, errors.New("multiple Codex executables were found on PATH"))
	}
}

func (resolver *Resolver) isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && isExecutable(info)
}

func (resolver *Resolver) validate(path string) (launch.Candidate, error) {
	output, err := runVersion(path)
	if err != nil {
		return launch.Candidate{}, apperrors.New(apperrors.LaunchCodexVersionInvalid, errors.New("Codex version validation failed"))
	}
	version, err := parseVersion(output)
	if err != nil {
		return launch.Candidate{}, apperrors.New(apperrors.LaunchCodexVersionInvalid, errors.New("Codex returned an invalid version"))
	}
	return launch.Candidate{Path: path, Version: version}, nil
}

func parseVersion(output []byte) (string, error) {
	fields := strings.Fields(string(output))
	var version string
	switch {
	case len(fields) == 1:
		version = fields[0]
	case len(fields) == 2 && (strings.EqualFold(fields[0], "codex-cli") || strings.EqualFold(fields[0], "codex")):
		version = fields[1]
	default:
		return "", errors.New("unsupported Codex version response")
	}
	if err := buildinfo.ValidateProductVersion(version); err != nil {
		return "", errors.New("Codex version is not semantic")
	}
	return version, nil
}

func invalidPath() error {
	return apperrors.New(apperrors.LaunchCodexPathInvalid, errors.New("Codex executable path is invalid"))
}

func candidateNames() []string {
	if runtime.GOOS != "windows" {
		return []string{"codex"}
	}

	names := []string{"codex"}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	seen := map[string]struct{}{"codex": {}}
	for _, extension := range strings.Split(pathext, ";") {
		extension = strings.TrimSpace(extension)
		if extension == "" {
			continue
		}
		name := "codex" + strings.ToLower(extension)
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func isExecutable(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

func runVersion(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return output, err
}
