// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
	"github.com/samber/lo"
)

// Default schema URI for this config file. Useful for capable editors to provide Intellisense and validation.
//
//nolint:lll // Can't really break up the long URI.
const DefaultSchemaURI = "https://raw.githubusercontent.com/microsoft/azure-linux-dev-tools/refs/heads/main/schemas/azldev.schema.json"

// Encapsulates a serialized project config file; used for serialization/deserialization.
type ConfigFile struct {
	// URI for the schema for this file format.
	SchemaURI string `toml:"$schema,omitempty"`

	// Basic project info.
	Project *ProjectInfo `toml:"project,omitempty" jsonschema:"title=Project info,description=Basic properties for this project"`

	// List of glob patterns specifying additional config files to load and deserialize.
	Includes []string `toml:"includes,omitempty" validate:"dive,required" jsonschema:"title=Includes,description=List of glob patterns specifying additional config files to load"`

	// Definitions of distros.
	Distros map[string]DistroDefinition `toml:"distros,omitempty" jsonschema:"title=Distros,description=Definitions of distros to build for or consume from"`

	// Reusable resource definitions (e.g., RPM repositories) referenced from
	// elsewhere in the configuration.
	Resources *ResourcesConfig `toml:"resources,omitempty" jsonschema:"title=Resources,description=Reusable named resource definitions"`

	// Definitions of component groups.
	ComponentGroups map[string]ComponentGroupConfig `toml:"component-groups,omitempty" validate:"dive" jsonschema:"title=Component groups,description=Definitions of component groups for this project"`

	// Definitions of components.
	Components map[string]ComponentConfig `toml:"components,omitempty" validate:"dive" jsonschema:"title=Components,description=Definitions of components for this project"`

	// Definitions of images.
	Images map[string]ImageConfig `toml:"images,omitempty" validate:"dive" jsonschema:"title=Images,description=Definitions of images for this project"`

	// Configuration for tools used by azldev.
	Tools *ToolsConfig `toml:"tools,omitempty" jsonschema:"title=Tools configuration,description=Configuration for tools used by azldev"`

	// DefaultComponentConfig is the project-wide default component configuration applied before any
	// component-group or component-level config is considered.
	DefaultComponentConfig *ComponentConfig `toml:"default-component-config,omitempty" jsonschema:"title=Default component config,description=Project-wide default applied to all components before group and component overrides"`

	// DefaultPackageConfig is the project-wide default package configuration applied before any
	// package-group or component-level config is considered.
	DefaultPackageConfig *PackageConfig `toml:"default-package-config,omitempty" jsonschema:"title=Default package config,description=Project-wide default applied to all binary packages before group and component overrides"`

	// Definitions of package groups. Groups allow shared configuration
	// to be applied to sets of binary packages.
	PackageGroups map[string]PackageGroupConfig `toml:"package-groups,omitempty" validate:"dive" jsonschema:"title=Package groups,description=Definitions of package groups for shared binary package configuration"`

	// Definitions of test suites.
	TestSuites map[string]TestSuiteConfig `toml:"test-suites,omitempty" validate:"dive" jsonschema:"title=Test Suites,description=Definitions of test suites for this project"`

	// Internal fields used to track the origin of the config file; `dir` is the directory
	// that the config file's relative paths are based from.
	sourcePath string `toml:"-"`
	dir        string `toml:"-" validate:"dir"`
}

// SourcePath returns the absolute path to the config file on disk.
func (f ConfigFile) SourcePath() string {
	return f.sourcePath
}

// Dir returns the directory containing the config file; relative paths within the config
// are resolved against this directory.
func (f ConfigFile) Dir() string {
	return f.dir
}

// Validates the format and internal consistency of the config file. Semantic errors are reported.
func (f ConfigFile) Validate() error {
	err := validator.New().Struct(f)
	if err != nil {
		return fmt.Errorf("config file error:\n%w", err)
	}

	// Validate package group configurations.
	for groupName, group := range f.PackageGroups {
		if err := group.Validate(); err != nil {
			return fmt.Errorf("invalid package group %#q:\n%w", groupName, err)
		}
	}

	// Validate 'overlay-files' scope and placeholder usage. 'overlay-files' is
	// only settable at project scope (the top-level 'default-component-config',
	// where every entry must contain the '{component}' placeholder as a whole
	// path segment) and at component scope ('[components.X]', where entries are
	// plain globs and '{component}' is forbidden). Distro and component-group
	// 'default-component-config' entries may not set 'overlay-files' at all.
	if err := f.validateOverlayFilesByScope(); err != nil {
		return err
	}

	// Per-component snapshot timestamps are not allowed. Components inherit
	// the snapshot from the distro/group default-component-config or the
	// project's default-distro. Per-component snapshots would create
	// non-deterministic builds that the lock file cannot reliably track.
	// Use an explicit 'upstream-commit' pin instead.

	// Validate overlay configurations for each component.
	for componentName, component := range f.Components {
		if err := validateOverlayFilesEntries(
			component.OverlayFiles, validateComponentOverlayFilesEntry,
		); err != nil {
			return fmt.Errorf("invalid 'overlay-files' for component %#q:\n%w", componentName, err)
		}

		for i, overlay := range component.Overlays {
			err := overlay.Validate()
			if err != nil {
				return fmt.Errorf("invalid overlay %d for component %#q:\n%w", i+1, componentName, err)
			}
		}

		// Validate build configuration.
		err := component.Build.Validate()
		if err != nil {
			return fmt.Errorf("invalid build config for component %#q:\n%w", componentName, err)
		}

		if err := validateSourceFiles(component.SourceFiles, componentName); err != nil {
			return err
		}

		if component.Spec.UpstreamDistro.Snapshot != "" {
			return fmt.Errorf(
				"component %#q has a per-component 'snapshot' on 'upstream-distro'; "+
					"snapshots should be set on the distro or group default-component-config, "+
					"or use 'upstream-commit' to pin a specific commit",
				componentName)
		}
	}

	// Validate test suite configurations.
	for suiteName, suite := range f.TestSuites {
		// Suite names are used as path components (e.g., for the per-suite venv directory),
		// so reject anything that could escape the intended directory or otherwise be unsafe
		// across platforms.
		if err := fileutils.ValidateFilename(suiteName); err != nil {
			return fmt.Errorf("invalid test suite name %#q:\n%w", suiteName, err)
		}

		suite.Name = suiteName

		if err := suite.Validate(); err != nil {
			return fmt.Errorf("invalid test suite %#q:\n%w", suiteName, err)
		}
	}

	return nil
}

// validateOverlayFilesByScope enforces where 'overlay-files' may appear.
//
//   - Project scope (top-level 'default-component-config'): each entry must
//     contain the '{component}' placeholder as a whole path segment; the
//     placeholder drives pattern-based component discovery.
//   - Component scope ('[components.X]'): plain globs; the '{component}'
//     placeholder is forbidden (the name is already fixed).
//   - Distro-version and component-group 'default-component-config':
//     'overlay-files' is not allowed at all. Broad-scope defaults for this
//     field silently displace project-level discovery and per-component
//     overrides, so we require callers to place the list at project or
//     component scope where the intent is unambiguous.
func (f ConfigFile) validateOverlayFilesByScope() error {
	if f.DefaultComponentConfig != nil {
		if err := validateOverlayFilesEntries(
			f.DefaultComponentConfig.OverlayFiles, validateProjectOverlayFilesEntry,
		); err != nil {
			return fmt.Errorf("invalid project 'default-component-config':\n%w", err)
		}
	}

	for distroName, distro := range f.Distros {
		for versionName, version := range distro.Versions {
			if version.DefaultComponentConfig.OverlayFiles != nil {
				return fmt.Errorf(
					"invalid 'default-component-config' for distro %#q version %#q:\n"+
						"%w: 'overlay-files' is only allowed on the project-level "+
						"'default-component-config' (with '%s' patterns) or on individual "+
						"'[components.X]' entries",
					distroName, versionName,
					ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder,
				)
			}
		}
	}

	for groupName, group := range f.ComponentGroups {
		if group.DefaultComponentConfig.OverlayFiles != nil {
			return fmt.Errorf(
				"invalid 'default-component-config' for component group %#q:\n"+
					"%w: 'overlay-files' is only allowed on the project-level "+
					"'default-component-config' (with '%s' patterns) or on individual "+
					"'[components.X]' entries",
				groupName,
				ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder,
			)
		}
	}

	return nil
}

// overlayFilesValidator validates a single 'overlay-files' entry against
// the placeholder rules of a particular config scope.
type overlayFilesValidator func(entry string) error

// validateProjectOverlayFilesEntry validates entries in the project-level
// default-component-config. Every entry MUST contain the '{component}'
// placeholder exactly once as a whole path segment — it's the discovery
// mechanism.
func validateProjectOverlayFilesEntry(entry string) error {
	return validateOverlayFilesPlaceholder(entry)
}

// validateComponentOverlayFilesEntry validates entries in a '[components.X]'
// table. Entries are plain globs; '{component}' is forbidden because the
// component name is already fixed by the table key.
func validateComponentOverlayFilesEntry(entry string) error {
	if !hasOverlayFilesPlaceholder(entry) {
		return nil
	}

	return fmt.Errorf(
		"%w: %q is only allowed in project-level 'default-component-config' entries; entry %#q",
		ErrInvalidOverlayFilesEntry, OverlayFilesComponentPlaceholder, entry,
	)
}

// validateOverlayFilesEntries runs validate against each entry in overlayFiles,
// annotating the returned error with the offending index.
func validateOverlayFilesEntries(overlayFiles []string, validate overlayFilesValidator) error {
	for idx, entry := range overlayFiles {
		if err := validate(entry); err != nil {
			return fmt.Errorf("'overlay-files'[%d]: %w", idx, err)
		}
	}

	return nil
}

// validateSourceFiles checks 'source-files' configuration for a component:
//   - All filenames must be unique.
//   - Hash type must be a supported algorithm when specified.
//   - Hash value without a hash type is not allowed.
//   - Origin must be present and valid for each source file.
//   - 'replace-upstream' and 'replace-reason' must be set together.
func validateSourceFiles(sourceFiles []SourceFileReference, componentName string) error {
	seen := make(map[string]bool, len(sourceFiles))

	for _, ref := range sourceFiles {
		if err := fileutils.ValidateFilename(ref.Filename); err != nil {
			return fmt.Errorf("invalid filename %#q for source file in component %#q:\n%w", ref.Filename, componentName, err)
		}

		if seen[ref.Filename] {
			return fmt.Errorf(
				"duplicate filename %#q in 'source-files' for component %#q; each filename must be unique",
				ref.Filename, componentName)
		}

		seen[ref.Filename] = true

		if ref.HashType != "" && !AllowedSourceFilesHashTypes[ref.HashType] {
			return fmt.Errorf(
				"unsupported hash type %#q for source file %#q, component %#q; supported types are %v",
				ref.HashType, ref.Filename, componentName, lo.Keys(AllowedSourceFilesHashTypes))
		}

		if ref.Hash != "" && ref.HashType == "" {
			return fmt.Errorf(
				"hash value specified without hash type for source file %#q, component %#q; "+
					"'hash-type' must be set when 'hash' is provided",
				ref.Filename, componentName)
		}

		if err := validateReplaceUpstream(ref, componentName); err != nil {
			return err
		}

		if err := validateCustomSourceRef(ref, componentName); err != nil {
			return err
		}

		if err := validateOrigin(ref.Origin, ref.Filename, componentName); err != nil {
			return err
		}
	}

	return nil
}

// validateReplaceUpstream enforces the pairing rules for the 'replace-upstream' and
// 'replace-reason' fields on a [SourceFileReference]:
//   - 'replace-upstream = true' requires a non-empty 'replace-reason' (whitespace-only
//     values do not count).
//   - 'replace-reason' may only be set when 'replace-upstream = true'. A non-empty
//     'replace-reason' (even if it would trim to empty) is rejected when
//     'replace-upstream' is false, to surface the configuration mistake rather than
//     silently ignoring the value.
func validateReplaceUpstream(ref SourceFileReference, componentName string) error {
	if ref.ReplaceUpstream && strings.TrimSpace(ref.ReplaceReason) == "" {
		return fmt.Errorf(
			"source file %#q in component %#q has 'replace-upstream = true' but no 'replace-reason'; "+
				"a non-empty 'replace-reason' is required to document the override",
			ref.Filename, componentName)
	}

	if !ref.ReplaceUpstream && ref.ReplaceReason != "" {
		return fmt.Errorf(
			"source file %#q in component %#q has 'replace-reason' set but 'replace-upstream' is not true; "+
				"'replace-reason' is only valid when 'replace-upstream = true'",
			ref.Filename, componentName)
	}

	return nil
}

// validateCustomSourceRef enforces the pairing rules for the 'script' and 'mock-packages'
// fields on a [SourceFileReference]:
//   - 'script' is required when 'origin.type' is 'custom'.
//   - 'script' must be empty when 'origin.type' is not 'custom'.
//   - 'mock-packages' must be empty when 'origin.type' is not 'custom'.
func validateCustomSourceRef(ref SourceFileReference, componentName string) error {
	if ref.Origin.Type == OriginTypeCustom {
		if ref.Origin.Script == "" {
			return fmt.Errorf(
				"source file %#q in component %#q has 'custom' origin but no 'script'; "+
					"a non-empty 'script' filename is required for 'custom' origin",
				ref.Filename, componentName)
		}

		if err := fileutils.ValidateFilename(ref.Origin.Script); err != nil {
			return fmt.Errorf(
				"invalid 'script' value %#q for source file %#q in component %#q:\n%w",
				ref.Origin.Script, ref.Filename, componentName, err)
		}

		return nil
	}

	if ref.Origin.Script != "" {
		return fmt.Errorf(
			"source file %#q in component %#q has 'script' set but origin type is %#q; "+
				"'script' is only valid when origin type is 'custom'",
			ref.Filename, componentName, string(ref.Origin.Type))
	}

	if len(ref.Origin.MockPackages) > 0 {
		return fmt.Errorf(
			"source file %#q in component %#q has 'mock-packages' set but origin type is %#q; "+
				"'mock-packages' is only valid when origin type is 'custom'",
			ref.Filename, componentName, string(ref.Origin.Type))
	}

	return nil
}

// validateOrigin checks that a source file [Origin] is present and valid for its type.
// For [OriginTypeURI] ('download'), the [Origin.Uri] field must be a valid URI with a scheme.
func validateOrigin(origin Origin, filename string, componentName string) error {
	if origin.Type == "" {
		return fmt.Errorf(
			"missing 'origin' for source file %#q, component %#q; "+
				"an origin is required for all source file entries",
			filename, componentName)
	}

	switch origin.Type {
	case OriginTypeURI:
		if origin.Uri == "" {
			return fmt.Errorf(
				"missing 'uri' for source file %#q, component %#q; "+
					"'uri' is required when 'origin' type is 'download'",
				filename, componentName)
		}

		parsed, err := url.Parse(origin.Uri)
		if err != nil {
			return fmt.Errorf(
				"invalid 'uri' for source file %#q, component %#q:\n%w",
				filename, componentName, err)
		}

		if parsed.Scheme == "" {
			return fmt.Errorf(
				"invalid 'uri' for source file %#q, component %#q; "+
					"URI %#q is missing a scheme (e.g. 'https://')",
				filename, componentName, origin.Uri)
		}

	case OriginTypeCustom:
		// Script validation is handled by validateCustomSourceRef on SourceFileReference.
		// Reject 'uri' since it is meaningless for custom-generated sources.
		if origin.Uri != "" {
			return fmt.Errorf(
				"source file %#q in component %#q has 'uri' set but origin type is 'custom'; "+
					"'uri' is only valid when origin type is 'download'",
				filename, componentName)
		}

	default:
		return fmt.Errorf(
			"unsupported 'origin' type %#q for source file %#q, component %#q",
			origin.Type, filename, componentName)
	}

	return nil
}

// ToBytes serializes the config file to a byte slice in TOML format.
func (f ConfigFile) ToBytes() ([]byte, error) {
	bytes, err := toml.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize project config:\n%w", err)
	}

	return bytes, nil
}

// Serializes writes the config file to the specified path in appropriate format (TOML). If the given path already
// exists, it will be overwritten.
func (f ConfigFile) Serialize(fs opctx.FS, filePath string) error {
	const defaultPerms = fileperms.PublicFile

	bytes, err := f.ToBytes()
	if err != nil {
		return err
	}

	err = fileutils.WriteFile(fs, filePath, bytes, defaultPerms)
	if err != nil {
		return fmt.Errorf("failed to write project config:\n%w", err)
	}

	return nil
}
