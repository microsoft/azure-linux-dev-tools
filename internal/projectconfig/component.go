// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"dario.cat/mergo"
	"github.com/brunoga/deep"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

const (
	// HashTypeSHA256 represents the SHA-256 hash algorithm.
	HashTypeSHA256 = fileutils.HashTypeSHA256

	// HashTypeSHA512 represents the SHA-512 hash algorithm.
	HashTypeSHA512 = fileutils.HashTypeSHA512
)

// ComponentReference encapsulates a reference to a source component.
type ComponentReference struct {
	// Name of the component.
	Name string

	// Version of the component (optional).
	Version *rpm.Version
}

// OriginType indicates the type of origin for a source file.
type OriginType string

const (
	// OriginTypeURI indicates that the source file is fetched from a URI.
	OriginTypeURI OriginType = "download"
)

// Origin describes where a source file comes from and how to retrieve it.
type Origin struct {
	// Type indicates how the source file should be acquired.
	Type OriginType `toml:"type" json:"type" jsonschema:"required,enum=download,title=Origin type,description=Type of origin for this source file"`
	// Uri to download the source file from if origin type is 'download'. Ignored for other origin types.
	Uri string `toml:"uri,omitempty" json:"uri,omitempty" jsonschema:"title=URI,description=URI to download the source file from if origin type is 'download',example=https://example.com/source.tar.gz"`
}

// SourceFileReference encapsulates a reference to a specific source file artifact.
type SourceFileReference struct {
	// Reference to the component to which the source file belongs.
	Component ComponentReference `toml:"-" json:"-"`

	// Name of the source file; must be non-empty.
	Filename string `toml:"filename" json:"filename"`

	// Hash of the source file, expressed as a hex string.
	Hash string `toml:"hash,omitempty" json:"hash,omitempty"`

	// Type of hash used by Hash (e.g., "sha256", "sha512").
	HashType fileutils.HashType `toml:"hash-type,omitempty" json:"hashType,omitempty"`

	// Type of origin for this source file (e.g., URI, custom).
	Origin Origin `toml:"origin" json:"origin"`
}

// Defines a component group. Component groups are logical groupings of components (see [ComponentConfig]).
// A component group is useful because it allows for succinctly naming/identifying a curated set of components,
// say in a command line interface. Note that a component group does not uniquely "own" its components; a
// component may belong to multiple groups, and components need not belong to any group.
type ComponentGroupConfig struct {
	// A human-friendly description of this component group.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Description of this component group"`

	// List of explicitly included components, identified by name.
	Components []string `toml:"components,omitempty" json:"components,omitempty" jsonschema:"title=Components,description=List of component names that are members of this group"`

	// List of glob patterns specifying raw spec files that define components.
	SpecPathPatterns []string `toml:"specs,omitempty" json:"specs,omitempty" validate:"dive,required" jsonschema:"title=Spec path patterns,description=List of glob patterns identifying local specs for components in this group,example=SPECS/**/.spec"`
	// List of glob patterns specifying files to specifically ignore from spec selection.
	ExcludedPathPatterns []string `toml:"excluded-paths,omitempty" json:"excludedPaths,omitempty" jsonschema:"title=Excluded path patterns,description=List of glob patterns identifying local paths to exclude from spec selection,example=build/**"`

	// Default configuration to apply to component members of this group.
	DefaultComponentConfig ComponentConfig `toml:"default-component-config,omitempty" json:"defaultComponentConfig,omitempty" jsonschema:"title=Default component configuration,description=Default component config inherited by all members of this component group"`
}

// Returns a copy of the component group config with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (g ComponentGroupConfig) WithAbsolutePaths(referenceDir string) ComponentGroupConfig {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(g)

	// Fix up paths.
	for i := range result.SpecPathPatterns {
		result.SpecPathPatterns[i] = makeAbsolute(referenceDir, result.SpecPathPatterns[i])
	}

	for i := range result.ExcludedPathPatterns {
		result.ExcludedPathPatterns[i] = makeAbsolute(referenceDir, result.ExcludedPathPatterns[i])
	}

	result.DefaultComponentConfig = *(result.DefaultComponentConfig.WithAbsolutePaths(referenceDir))

	return result
}

// Defines a component.
type ComponentConfig struct {
	// The component's name; not actually present in serialized files.
	Name string `toml:"-" json:"name" table:",sortkey"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-"`

	// Where to get its spec and adjacent files from.
	Spec SpecSource `toml:"spec,omitempty" json:"spec,omitempty" jsonschema:"title=Spec,description=Identifies where to find the spec for this component"`

	// Overlays to apply to sources after they've been acquired. May mutate the spec as well as sources.
	Overlays []ComponentOverlay `toml:"overlays,omitempty" json:"overlays,omitempty" table:"-" jsonschema:"title=Overlays,description=Overlays to apply to this component's spec and/or sources"`

	// Configuration for building the component.
	Build ComponentBuildConfig `toml:"build,omitempty" json:"build,omitempty" table:"-" jsonschema:"title=Build configuration,description=Configuration for building the component"`

	// Source file references for this component.
	SourceFiles []SourceFileReference `toml:"source-files,omitempty" json:"sourceFiles,omitempty" table:"-" jsonschema:"title=Source files,description=Source files to download for this component"`
}

// Mutates the component config, updating it with overrides present in other.
func (c *ComponentConfig) MergeUpdatesFrom(other *ComponentConfig) error {
	err := mergo.Merge(c, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge project info:\n%w", err)
	}

	return nil
}

// Returns a copy of the component config with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (c *ComponentConfig) WithAbsolutePaths(referenceDir string) *ComponentConfig {
	// Deep copy the input to avoid unexpected sharing. Make sure *not* to deep-copy
	// the SourceConfigFile, as we *do* want to alias that pointer, sharing it across
	// all configs that came from that source config file.
	result := &ComponentConfig{
		Name:             c.Name,
		SourceConfigFile: c.SourceConfigFile,
		Spec:             deep.MustCopy(c.Spec),
		Build:            deep.MustCopy(c.Build),
		SourceFiles:      deep.MustCopy(c.SourceFiles),
	}

	// Fix up paths.
	result.Spec.Path = makeAbsolute(referenceDir, result.Spec.Path)

	// Copy and fix up overlays.
	if c.Overlays != nil {
		result.Overlays = make([]ComponentOverlay, len(c.Overlays))

		for i := range result.Overlays {
			result.Overlays[i] = *c.Overlays[i].WithAbsolutePaths(referenceDir)
		}
	}

	return result
}
