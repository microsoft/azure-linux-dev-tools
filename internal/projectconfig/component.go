// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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
// When omitted from a source file reference, the file will be resolved via the lookaside cache.
type Origin struct {
	// Type indicates how the source file should be acquired.
	Type OriginType `toml:"type" json:"type" jsonschema:"required,enum=download,title=Origin type,description=Type of origin for this source file"`
	// Uri to download the source file from if origin type is 'download'. Ignored for other origin types.
	Uri string `toml:"uri,omitempty" json:"uri,omitempty" jsonschema:"title=URI,description=URI to download the source file from if origin type is 'download',example=https://example.com/source.tar.gz"`
}

// SourceFileReference encapsulates a reference to a specific source file artifact.
type SourceFileReference struct {
	// Reference to the component to which the source file belongs.
	Component ComponentReference `toml:"-" json:"-" fingerprint:"-"`

	// Name of the source file; must be non-empty.
	Filename string `toml:"filename" json:"filename"`

	// Hash of the source file, expressed as a hex string.
	Hash string `toml:"hash,omitempty" json:"hash,omitempty"`

	// Type of hash used by Hash (e.g., "SHA256", "SHA512").
	HashType fileutils.HashType `toml:"hash-type,omitempty" json:"hashType,omitempty" jsonschema:"enum=SHA256,enum=SHA512,title=Hash type,description=Hash algorithm used for the hash value"`

	// Origin for this source file. When omitted, the file is resolved via the lookaside cache.
	Origin Origin `toml:"origin,omitempty" json:"origin,omitempty" fingerprint:"-"`
}

// ComponentPublishConfig holds publish channel settings for a component's packages.
// The zero value means all channels are inherited from a higher-priority config layer.
type ComponentPublishConfig struct {
	// RPMChannel identifies the publish channel for binary (non-debuginfo) packages
	// produced by this component. When empty, the value is inherited from the next layer
	// in the resolution order.
	RPMChannel string `toml:"rpm-channel,omitempty" json:"rpmChannel,omitempty" validate:"omitempty,ne=.,ne=..,excludesall=/\\" jsonschema:"title=RPM channel,description=Publish channel for binary packages produced by this component"`

	// SRPMChannel identifies the publish channel for the SRPM produced by this component.
	// When empty, the value is inherited from the next layer in the resolution order.
	SRPMChannel string `toml:"srpm-channel,omitempty" json:"srpmChannel,omitempty" validate:"omitempty,ne=.,ne=..,excludesall=/\\" jsonschema:"title=SRPM channel,description=Publish channel for the SRPM produced by this component"`

	// DebugInfoChannel identifies the publish channel for debuginfo packages produced
	// by this component. When empty, the value is inherited from the next layer in the
	// resolution order.
	DebugInfoChannel string `toml:"debuginfo-channel,omitempty" json:"debuginfoChannel,omitempty" validate:"omitempty,ne=.,ne=..,excludesall=/\\" jsonschema:"title=Debuginfo channel,description=Publish channel for debuginfo packages produced by this component"`
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

// ReleaseCalculation controls how the Release tag is managed during rendering.
type ReleaseCalculation string

const (
	// ReleaseCalculationAuto is the default. azldev auto-bumps the Release tag based on
	// synthetic commit history. Static integer releases are incremented; %autorelease
	// is handled by rpmautospec.
	ReleaseCalculationAuto ReleaseCalculation = "auto"

	// ReleaseCalculationManual skips all automatic Release tag manipulation. Use this for
	// components that manage their own release numbering (e.g. kernel).
	ReleaseCalculationManual ReleaseCalculation = "manual"
)

// ReleaseConfig holds release-related configuration for a component.
type ReleaseConfig struct {
	// Calculation controls how the Release tag is managed during rendering.
	Calculation ReleaseCalculation `toml:"calculation,omitempty" json:"calculation,omitempty" validate:"omitempty,oneof=auto manual" jsonschema:"enum=auto,enum=manual,default=auto,title=Release calculation,description=Controls how the Release tag is managed during rendering. Empty or omitted means auto."`
}

// Defines a component.
type ComponentConfig struct {
	// The component's name; not actually present in serialized files.
	Name string `toml:"-" json:"name" table:",sortkey" fingerprint:"-"`

	// Reference to the source config file that this definition came from; not present
	// in serialized files.
	SourceConfigFile *ConfigFile `toml:"-" json:"-" table:"-" fingerprint:"-"`

	// RenderedSpecDir is the output directory for this component's rendered spec files.
	// Derived at resolve time from the project's rendered-specs-dir setting; not present
	// in serialized files. Empty when rendered-specs-dir is not configured.
	RenderedSpecDir string `toml:"-" json:"renderedSpecDir,omitempty" table:"-"`

	// Where to get its spec and adjacent files from.
	Spec SpecSource `toml:"spec,omitempty" json:"spec,omitempty" jsonschema:"title=Spec,description=Identifies where to find the spec for this component"`

	// Release configuration for this component.
	Release ReleaseConfig `toml:"release,omitempty" json:"release,omitempty" table:"-" jsonschema:"title=Release configuration,description=Configuration for how the Release tag is managed during rendering."`

	// Overlays to apply to sources after they've been acquired. May mutate the spec as well as sources.
	Overlays []ComponentOverlay `toml:"overlays,omitempty" json:"overlays,omitempty" table:"-" jsonschema:"title=Overlays,description=Overlays to apply to this component's spec and/or sources"`

	// Configuration for building the component.
	Build ComponentBuildConfig `toml:"build,omitempty" json:"build,omitempty" table:"-" jsonschema:"title=Build configuration,description=Configuration for building the component"`

	// Configuration for rendering the component.
	Render ComponentRenderConfig `toml:"render,omitempty" json:"render,omitempty" table:"-" jsonschema:"title=Render configuration,description=Configuration for rendering the component"`

	// Source file references for this component.
	SourceFiles []SourceFileReference `toml:"source-files,omitempty" json:"sourceFiles,omitempty" table:"-" jsonschema:"title=Source files,description=Source files to download for this component"`

	// Per-package configuration overrides, keyed by exact binary package name.
	// Takes precedence over package-group defaults.
	Packages map[string]PackageConfig `toml:"packages,omitempty" json:"packages,omitempty" table:"-" validate:"dive" jsonschema:"title=Package overrides,description=Per-package configuration overrides keyed by exact binary package name"`

	// Publish holds the component-level publish settings. These provide default channels for
	// all packages produced by this component. Overridden by package-group and per-package settings
	// for binary and debuginfo channels.
	Publish ComponentPublishConfig `toml:"publish,omitempty" json:"publish,omitempty" table:"-" jsonschema:"title=Publish settings,description=Component-level publish channel settings" fingerprint:"-"`
}

// AllowedSourceFilesHashTypes defines the set of hash types that are supported
// for use in [SourceFileReference] entries in component configs.
// MD5 is excluded by design.
//
//nolint:gochecknoglobals // This is effectively a constant, but Go doesn't have const maps.
var AllowedSourceFilesHashTypes = map[fileutils.HashType]bool{
	fileutils.HashTypeSHA256: true,
	fileutils.HashTypeSHA512: true,
}

// Mutates the component config, updating it with overrides present in other.
func (c *ComponentConfig) MergeUpdatesFrom(other *ComponentConfig) error {
	err := mergo.Merge(c, other, mergo.WithOverride, mergo.WithAppendSlice)
	if err != nil {
		return fmt.Errorf("failed to merge project info:\n%w", err)
	}

	return nil
}

// ResolveComponentConfig applies the full config inheritance chain for a single component:
// distro defaults → project-level defaults → group defaults (sorted) → component explicit config.
// Returns a fully resolved copy; the inputs are not modified.
// On error the returned config is undefined and must not be used.
func ResolveComponentConfig(
	comp ComponentConfig,
	projectDefaults ComponentConfig,
	distroDefaults ComponentConfig,
	groups map[string]ComponentGroupConfig,
	groupMembership []string,
) (ComponentConfig, error) {
	merged := deep.MustCopy(distroDefaults)

	if err := merged.MergeUpdatesFrom(&projectDefaults); err != nil {
		return ComponentConfig{}, fmt.Errorf("failed to apply project defaults:\n%w", err)
	}

	// Apply group defaults in sorted order for determinism.
	sortedGroups := slices.Clone(groupMembership)
	sort.Strings(sortedGroups)

	for _, groupName := range sortedGroups {
		groupConfig, ok := groups[groupName]
		if !ok {
			return ComponentConfig{}, fmt.Errorf("component group not found: %#q", groupName)
		}

		if err := merged.MergeUpdatesFrom(&groupConfig.DefaultComponentConfig); err != nil {
			return ComponentConfig{}, fmt.Errorf(
				"failed to apply defaults from component group %#q:\n%w",
				groupName, err)
		}
	}

	if err := merged.MergeUpdatesFrom(&comp); err != nil {
		return ComponentConfig{}, fmt.Errorf("failed to apply component config:\n%w", err)
	}

	return merged, nil
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
		RenderedSpecDir:  c.RenderedSpecDir,
		Release:          c.Release,
		Spec:             deep.MustCopy(c.Spec),
		Build:            deep.MustCopy(c.Build),
		Render:           c.Render,
		SourceFiles:      deep.MustCopy(c.SourceFiles),
		Packages:         deep.MustCopy(c.Packages),
		Publish:          deep.MustCopy(c.Publish),
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

// IsDebugInfoPackage reports whether pkgName is a debuginfo or debugsource package.
// It matches "-debuginfo" or "-debugsource" as hyphen-delimited segments so that
// names like "kernel-debuginfo-common-x86_64" are correctly identified while
// unrelated packages like "elfutils-debuginfod" are not.
func IsDebugInfoPackage(pkgName string) bool {
	return containsRPMSegment(pkgName, "-debuginfo") || containsRPMSegment(pkgName, "-debugsource")
}

// containsRPMSegment reports whether pkgName contains segment as a hyphen-delimited
// segment — i.e. segment is followed by end-of-string or '-'. This prevents
// "-debuginfo" from matching "-debuginfod".
func containsRPMSegment(pkgName, segment string) bool {
	searchStart := 0

	for {
		idx := strings.Index(pkgName[searchStart:], segment)
		if idx < 0 {
			return false
		}

		idx += searchStart
		end := idx + len(segment)

		if end == len(pkgName) || pkgName[end] == '-' {
			return true
		}

		searchStart = idx + 1
	}
}

// ResolvePackagePublishChannel returns the publish channel for a binary package produced by a
// component. The caller must pass a resolved [ComponentConfig] (one whose Publish field already
// reflects project-level, distro, and component-group defaults).
//
// Resolution order (later wins):
//  0. The project-level default package config ([ProjectConfig.DefaultPackageConfig]), used
//     only as a fallback when all higher-priority sources produce an empty channel.
//  1. The resolved component-level publish channel ([ComponentPublishConfig.RPMChannel] or
//     [ComponentPublishConfig.DebugInfoChannel], depending on the package name).
//  2. The matching package-group's publish channel, if the package belongs to one.
//  3. The component's explicit per-package publish channel override, if set.
func ResolvePackagePublishChannel(pkgName string, comp *ComponentConfig, proj *ProjectConfig) (string, error) {
	isDebugInfo := IsDebugInfoPackage(pkgName)

	// Lowest priority: project-level default package config acts as a fallback for
	// packages not covered by any higher-priority source. This matches the old
	// single-field 'publish.channel' behaviour where the default applied to all packages.
	channel := packagePublishChannel(&proj.DefaultPackageConfig.Publish, isDebugInfo)

	// Component-level channel overrides the project default.
	var compChannel string
	if isDebugInfo {
		compChannel = comp.Publish.DebugInfoChannel
	} else {
		compChannel = comp.Publish.RPMChannel
	}

	if compChannel != "" {
		channel = compChannel
	}

	// Apply package-group override if this package belongs to one.
	for _, group := range proj.PackageGroups {
		if slices.Contains(group.Packages, pkgName) {
			if groupChannel := packagePublishChannel(&group.DefaultPackageConfig.Publish, isDebugInfo); groupChannel != "" {
				channel = groupChannel
			}

			break
		}
	}

	// Apply the explicit per-package override (highest priority).
	if pkgConfig, ok := comp.Packages[pkgName]; ok {
		if pkgChannel := packagePublishChannel(&pkgConfig.Publish, isDebugInfo); pkgChannel != "" {
			channel = pkgChannel
		}
	}

	return channel, nil
}

// packagePublishChannel returns the rpm-channel or debuginfo-channel from publish config
// depending on whether the package is a debuginfo package. For non-debuginfo packages,
// it falls back to the deprecated 'channel' field for backwards compatibility.
func packagePublishChannel(publish *PackagePublishConfig, isDebugInfo bool) string {
	if isDebugInfo {
		return publish.DebugInfoChannel
	}

	return publish.EffectiveRPMChannel()
}
