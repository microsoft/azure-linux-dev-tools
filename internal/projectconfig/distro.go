// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"runtime"

	"dario.cat/mergo"
	"github.com/brunoga/deep"
)

// Encapsulates a reference to a version of a distro.
type DistroReference struct {
	// Name of the referenced distro.
	Name string `toml:"name" json:"name,omitempty" jsonschema:"required,title=Name,description=Name of the referenced distro"`
	// Version of the referenced distro.
	Version string `toml:"version,omitempty" json:"version,omitempty" jsonschema:"title=Version,description=Version of the referenced distro"`
	// Snapshot date/time for source code if specified components will use source as it existed at this time.
	// Note: set this on the distro or group default-component-config, not on individual components.
	// Per-component snapshots are rejected when lock validation is enabled.
	Snapshot string `toml:"snapshot,omitempty" json:"snapshot,omitempty" jsonschema:"format=date-time,title=Snapshot,description=Snapshot timestamp for source code. Set on the distro or group default-component-config only — per-component snapshots are not allowed." fingerprint:"-"`
}

// Implements the [Stringer] interface for [DistroReference].
func (r *DistroReference) String() string {
	displayName := r.Name
	if displayName == "" {
		displayName = "(default)"
	}

	displayVersion := r.Version
	if displayVersion == "" {
		displayVersion = "(default)"
	}

	return displayName + " " + displayVersion
}

// Defines a distro that components may be built for/against.
type DistroDefinition struct {
	// Human-readable description of the distro.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable description"`

	// Optionally provides a default version to use for this distro when one is not explicitly specified.
	DefaultVersion string `toml:"default-version,omitempty" json:"defaultVersion,omitempty" jsonschema:"title=Default version,description=Default version for this distro"`

	// The base URI of this distro's dist-git spec source repository.
	DistGitBaseURI string `toml:"dist-git-base-uri,omitempty" json:"distGitBaseUri,omitempty" jsonschema:"format=uri,title=Dist-git base URI,description=Base URI for the dist-git repository for this distro"`

	// The base URI of this distro's lookaside cache for source archives.
	LookasideBaseURI string `toml:"lookaside-base-uri,omitempty" json:"lookasideBaseUri,omitempty" jsonschema:"format=uri,title=Lookaside base URI,description=Base URI for lookaside cache for this distro"`

	// Published artifact information
	PackageRepositories []PackageRepository `toml:"repos,omitempty" json:"repos,omitempty" jsonschema:"title=Package Repositories,description=List of package repository definitions"`

	// When true, source file downloads will not fall back to configured origins if the lookaside cache fails.
	DisableOrigins bool `toml:"disable-origins,omitempty" json:"disableOrigins,omitempty" jsonschema:"title=Disable origins,description=When true only allow source files from the lookaside cache and do not fall back to configured origins"`

	// Versions: maps version => definition
	Versions map[string]DistroVersionDefinition `toml:"versions,omitempty" json:"versions,omitempty" jsonschema:"title=Versions,description=Mapping of distro version definitions"`
}

// Defines how to access the published repository for a distro.
type PackageRepository struct {
	BaseURI string `toml:"base-uri" json:"baseUri" jsonschema:"required,title=Base URI,description=Base URI for the repository"`
}

// Defines a specific version of a distro.
type DistroVersionDefinition struct {
	// Human-readable description of this version
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable description of the distro version"`

	// Formal `releasever` for this version.
	ReleaseVer string `toml:"release-ver" json:"releaseVer,omitempty" jsonschema:"title=Release version,description=Formal releasever string"`

	// Dist-git branch for this version (if applicable)
	DistGitBranch string `toml:"dist-git-branch,omitempty" json:"distGitBranch,omitempty" jsonschema:"title=Dist-git branch,description=Branch in the dist-git repository for this version"`

	// Default config for components.
	DefaultComponentConfig ComponentConfig `toml:"default-component-config,omitempty" json:"defaultComponentConfig,omitempty" jsonschema:"title=Default component config,description=Default component config inherited by all components built for this distro"`

	// Path to mock configuration file for this project (if one exists).
	MockConfigPath        string `toml:"mock-config,omitempty"         json:"mockConfig,omitempty"        validate:"omitempty,filepath" jsonschema:"title=Mock config file,description=Path to the mock config file for this version"`
	MockConfigPathX86_64  string `toml:"mock-config-x86_64,omitempty"  json:"mockConfigX8664,omitempty"   validate:"omitempty,filepath" jsonschema:"title=Mock config file,description=Path to the x86_64 mock config file for this version"`
	MockConfigPathAarch64 string `toml:"mock-config-aarch64,omitempty" json:"mockConfigAarch64,omitempty" validate:"omitempty,filepath" jsonschema:"title=Mock config file,description=Path to the aarch64 mock config file for this version"`
}

// MergeUpdatesFrom mutates the distro definition, updating it with overrides present in other.
// Uses [mergo.WithOverride] without WithAppendSlice so that slice fields like
// [DistroDefinition.PackageRepositories] are replaced, not appended. This supports the primary
// use case of swapping between package sources via --config-file overrides.
//
// For map fields like [DistroDefinition.Versions], mergo replaces the entire value for a
// matching key rather than doing a field-level merge within the value struct.
func (d *DistroDefinition) MergeUpdatesFrom(other *DistroDefinition) error {
	err := mergo.Merge(d, other, mergo.WithOverride)
	if err != nil {
		return fmt.Errorf("failed to merge distro definition:\n%w", err)
	}

	return nil
}

// Returns a copy of the distro definition with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (d *DistroDefinition) WithAbsolutePaths(referenceDir string) DistroDefinition {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(*d)

	for name := range result.Versions {
		result.Versions[name] = result.Versions[name].WithAbsolutePaths(referenceDir)
	}

	for i := range result.PackageRepositories {
		result.PackageRepositories[i] = result.PackageRepositories[i].WithAbsolutePaths(referenceDir)
	}

	return result
}

func (d *DistroDefinition) WithResolvedConfigs() DistroDefinition {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(*d)

	for name := range result.Versions {
		result.Versions[name] = result.Versions[name].WithResolvedConfigs()
	}

	return result
}

// Returns a copy of the distro version definition with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (v DistroVersionDefinition) WithAbsolutePaths(referenceDir string) DistroVersionDefinition {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(v)

	result.DefaultComponentConfig = *(result.DefaultComponentConfig.WithAbsolutePaths(referenceDir))

	result.MockConfigPath = makeAbsolute(referenceDir, result.MockConfigPath)

	return result
}

func (v DistroVersionDefinition) WithResolvedConfigs() DistroVersionDefinition {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(v)

	if runtime.GOARCH == "amd64" && result.MockConfigPathX86_64 != "" {
		result.MockConfigPath = result.MockConfigPathX86_64
	} else if runtime.GOARCH == "arm64" && result.MockConfigPathAarch64 != "" {
		result.MockConfigPath = result.MockConfigPathAarch64
	}

	return result
}

// Returns a copy of the package repository definition with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (r PackageRepository) WithAbsolutePaths(referenceDir string) PackageRepository {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	return deep.MustCopy(r)
}
