// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"
	"net/url"

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

	// Validate overlay configurations for each component.
	for componentName, component := range f.Components {
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
	}

	return nil
}

// validateSourceFiles checks 'source-files' configuration for a component:
//   - All filenames must be unique.
//   - Hash type must be a supported algorithm when specified.
//   - Hash value without a hash type is not allowed.
//   - Origin must be present and valid for each source file.
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

		if err := validateOrigin(ref.Origin, ref.Filename, componentName); err != nil {
			return err
		}
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
