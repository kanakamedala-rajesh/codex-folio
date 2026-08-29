package buildinfo

import "testing"

func TestVersionSourceIsSemanticVersion(t *testing.T) {
	t.Parallel()

	if err := ValidateProductVersion(Version); err != nil {
		t.Fatalf("canonical product version %q is invalid: %v", Version, err)
	}
}

func TestMetadataNormalizesBuildState(t *testing.T) {
	t.Parallel()

	got := NewMetadata(BuildMetadataInput{
		SourceRevision: "abc1234",
		BuildClass:     "development",
		Dirty:          "clean",
	})

	if got.Product != ProductName {
		t.Errorf("Product = %q, want %q", got.Product, ProductName)
	}
	if got.Command != CommandName {
		t.Errorf("Command = %q, want %q", got.Command, CommandName)
	}
	if got.Version != Version {
		t.Errorf("Version = %q, want %q", got.Version, Version)
	}
	if got.SourceRevision != "abc1234" {
		t.Errorf("SourceRevision = %q, want %q", got.SourceRevision, "abc1234")
	}
	if got.BuildClass != "development" {
		t.Errorf("BuildClass = %q, want %q", got.BuildClass, "development")
	}
	if got.Dirty != "clean" {
		t.Errorf("Dirty = %q, want %q", got.Dirty, "clean")
	}
}

func TestMetadataDefaultsUnknownBuildState(t *testing.T) {
	t.Parallel()

	got := NewMetadata(BuildMetadataInput{})

	if got.SourceRevision != UnknownValue {
		t.Errorf("SourceRevision = %q, want %q", got.SourceRevision, UnknownValue)
	}
	if got.BuildClass != BuildClassDefault {
		t.Errorf("BuildClass = %q, want %q", got.BuildClass, BuildClassDefault)
	}
	if got.Dirty != UnknownValue {
		t.Errorf("Dirty = %q, want %q", got.Dirty, UnknownValue)
	}
}
