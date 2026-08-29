package buildinfo

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
)

const (
	ProductName = "VenkataSudha CodexFolio"
	CommandName = "codex-folio"

	BuildClassDefault    = "development"
	BuildClassPrerelease = "prerelease"
	BuildClassStable     = "stable"

	DirtyClean   = "clean"
	DirtyDirty   = "dirty"
	DirtyUnknown = "unknown"
	UnknownValue = "unknown"
)

// Version is the canonical product version shared with the frontend build.
// It is kept in version.txt so Go and Vite consume the same source value.
//
//go:embed version.txt
var versionFile string

var Version = strings.TrimSpace(versionFile)

// These variables are intentionally link-time overridable by release tooling.
// A direct `go build` falls back to the VCS metadata that Go records in the
// binary when it is available.
var (
	Revision   = UnknownValue
	BuildClass = BuildClassDefault
	Dirty      = DirtyUnknown
)

type BuildMetadataInput struct {
	SourceRevision string
	BuildClass     string
	Dirty          string
}

type Metadata struct {
	Product        string `json:"product"`
	Command        string `json:"command"`
	Version        string `json:"version"`
	SourceRevision string `json:"source_revision"`
	BuildClass     string `json:"build_class"`
	Dirty          string `json:"dirty"`
}

func NewMetadata(input BuildMetadataInput) Metadata {
	revision := strings.TrimSpace(input.SourceRevision)
	if revision == "" {
		revision = UnknownValue
	}

	buildClass := strings.TrimSpace(input.BuildClass)
	if buildClass == "" {
		buildClass = BuildClassDefault
	}

	dirty := strings.TrimSpace(input.Dirty)
	if dirty == "" {
		dirty = DirtyUnknown
	}

	return Metadata{
		Product:        ProductName,
		Command:        CommandName,
		Version:        Version,
		SourceRevision: revision,
		BuildClass:     buildClass,
		Dirty:          dirty,
	}
}

func Current() Metadata {
	input := BuildMetadataInput{
		SourceRevision: Revision,
		BuildClass:     BuildClass,
		Dirty:          Dirty,
	}

	settings := goBuildSettings()
	if input.SourceRevision == "" || input.SourceRevision == UnknownValue {
		if revision := settings["vcs.revision"]; revision != "" {
			input.SourceRevision = revision
		}
	}
	if input.Dirty == "" || input.Dirty == DirtyUnknown {
		switch settings["vcs.modified"] {
		case "true":
			input.Dirty = DirtyDirty
		case "false":
			input.Dirty = DirtyClean
		}
	}

	return NewMetadata(input)
}

func (metadata Metadata) Human() string {
	return fmt.Sprintf(
		"%s %s\ncommand: %s\nsource revision: %s\nbuild classification: %s\nworking tree: %s\n",
		metadata.Product,
		metadata.Version,
		metadata.Command,
		metadata.SourceRevision,
		metadata.BuildClass,
		metadata.Dirty,
	)
}

func (metadata Metadata) JSON() ([]byte, error) {
	return json.MarshalIndent(metadata, "", "  ")
}

func ValidateProductVersion(version string) error {
	if !semanticVersionPattern.MatchString(version) {
		return fmt.Errorf("%q is not a semantic product version", version)
	}
	return nil
}

func goBuildSettings() map[string]string {
	settings := make(map[string]string)
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return settings
	}
	for _, setting := range buildInfo.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings
}

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
