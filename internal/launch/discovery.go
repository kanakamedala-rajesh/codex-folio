// Package launch owns installed Codex discovery and launch compatibility.
package launch

import (
	"errors"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
)

type CapabilityStatus string

const (
	CapabilitySupported CapabilityStatus = "supported"
	CapabilityDegraded  CapabilityStatus = "degraded"
	CapabilityDisabled  CapabilityStatus = "disabled"
)

type Capabilities struct {
	TransparentLaunch CapabilityStatus `json:"transparent_launch"`
	Metadata          CapabilityStatus `json:"metadata"`
	Experimental      CapabilityStatus `json:"experimental"`
}

type Discovery struct {
	Executable   string       `json:"executable"`
	Version      string       `json:"version"`
	Capabilities Capabilities `json:"capabilities"`
}

type Candidate struct {
	Path    string
	Version string
}

type ExecutableResolver interface {
	Resolve(override string) (Candidate, error)
}

func Discover(resolver ExecutableResolver, override string) (Discovery, error) {
	if resolver == nil {
		return Discovery{}, apperrors.New(apperrors.LaunchCodexPathInvalid, errors.New("Codex resolver is unavailable"))
	}
	candidate, err := resolver.Resolve(override)
	if err != nil {
		return Discovery{}, err
	}
	if strings.TrimSpace(candidate.Path) == "" {
		return Discovery{}, apperrors.New(apperrors.LaunchCodexPathInvalid, errors.New("resolved executable path is empty"))
	}
	if strings.TrimSpace(candidate.Version) == "" || buildinfo.ValidateProductVersion(candidate.Version) != nil {
		return Discovery{}, apperrors.New(apperrors.LaunchCodexVersionInvalid, errors.New("resolved executable version is invalid"))
	}
	return Discovery{
		Executable: candidate.Path,
		Version:    candidate.Version,
		Capabilities: Capabilities{
			TransparentLaunch: CapabilitySupported,
			Metadata:          CapabilityDegraded,
			Experimental:      CapabilityDisabled,
		},
	}, nil
}
