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

	// Definitions of individual tests (new schema, [tests.X]).
	Tests map[string]TestDefinition `toml:"tests,omitempty" validate:"dive" jsonschema:"title=Tests,description=Definitions of individual tests"`

	// Definitions of test groups (new schema, [test-groups.X]).
	TestGroups map[string]TestGroup `toml:"test-groups,omitempty" validate:"dive" jsonschema:"title=Test Groups,description=Definitions of named bundles of tests"`

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

	// Validate component group metadata.
	if err := validateComponentGroupMetadata(f.ComponentGroups); err != nil {
		return err
	}

	// Per-component snapshot timestamps are not allowed. Components inherit
	// the snapshot from the distro/group default-component-config or the
	// project's default-distro. Per-component snapshots would create
	// non-deterministic builds that the lock file cannot reliably track.
	// Use an explicit 'upstream-commit' pin instead.

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

		if component.Spec.UpstreamDistro.Snapshot != "" {
			return fmt.Errorf(
				"component %#q has a per-component 'snapshot' on 'upstream-distro'; "+
					"snapshots should be set on the distro or group default-component-config, "+
					"or use 'upstream-commit' to pin a specific commit",
				componentName)
		}
	}

	if err := validateTestSuites(f.TestSuites); err != nil {
		return err
	}

	if err := validateTestDefinitions(f.Tests); err != nil {
		return err
	}

	if err := validateNewTestReferences(f); err != nil {
		return err
	}

	return nil
}

func validateTestSuites(testSuites map[string]TestSuiteConfig) error {
	for suiteName, suite := range testSuites {
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

func validateTestDefinitions(tests map[string]TestDefinition) error {
	for testName, testDef := range tests {
		if err := testDef.Validate(testName); err != nil {
			return fmt.Errorf("invalid test %#q:\n%w", testName, err)
		}
	}

	return nil
}

// validateComponentGroupMetadata validates the optional documentation metadata declared
// on each component group.
func validateComponentGroupMetadata(groups map[string]ComponentGroupConfig) error {
	for groupName, group := range groups {
		if group.Metadata == nil {
			continue
		}

		if err := group.Metadata.Validate(); err != nil {
			return fmt.Errorf("invalid metadata on component group %#q:\n%w", groupName, err)
		}
	}

	return nil
}

func validateNewTestReferences(cfgFile ConfigFile) error {
	for groupName, group := range cfgFile.TestGroups {
		scope := fmt.Sprintf("test-group %#q tests", groupName)
		if err := validateTestGroupMembers(scope, group.Tests, cfgFile.Tests); err != nil {
			return err
		}
	}

	for componentName, component := range cfgFile.Components {
		if component.Tests == nil {
			continue
		}

		scope := fmt.Sprintf("component %#q tests.tests", componentName)
		if err := validateTestRefList(scope, component.Tests.Tests, cfgFile.Tests, cfgFile.TestGroups); err != nil {
			return err
		}
	}

	for imageName, image := range cfgFile.Images {
		if image.Tests == nil {
			continue
		}

		scope := fmt.Sprintf("image %#q tests.tests", imageName)
		if err := validateTestRefList(scope, image.Tests.Tests, cfgFile.Tests, cfgFile.TestGroups); err != nil {
			return err
		}
	}

	return nil
}

func validateTestGroupMembers(
	scope string,
	refs []TestRef,
	tests map[string]TestDefinition,
) error {
	seenRefs := make(map[string]int, len(refs))

	for idx, ref := range refs {
		hasName := ref.Name != ""
		hasGroup := ref.Group != ""

		if hasName == hasGroup {
			return fmt.Errorf(
				"%w: %s[%d] must set exactly one of 'name' or 'group'",
				ErrInvalidTestRef,
				scope,
				idx,
			)
		}

		if hasGroup {
			return fmt.Errorf(
				"%w: %s[%d].group is not allowed in [test-groups]; use .name to reference a [tests] entry",
				ErrNestedTestGroupReference,
				scope,
				idx,
			)
		}

		if _, ok := tests[ref.Name]; !ok {
			return fmt.Errorf(
				"%w: %s[%d].name references undefined test %#q",
				ErrUndefinedTest,
				scope,
				idx,
				ref.Name,
			)
		}

		refKey := "name:" + ref.Name
		if firstIdx, exists := seenRefs[refKey]; exists {
			return fmt.Errorf(
				"%w: %s[%d] duplicates %s[%d] (%#q)",
				ErrDuplicateTestRef,
				scope,
				idx,
				scope,
				firstIdx,
				ref.Name,
			)
		}

		seenRefs[refKey] = idx
	}

	return nil
}

func validateTestRefList(
	scope string,
	refs []TestRef,
	tests map[string]TestDefinition,
	groups map[string]TestGroup,
) error {
	seenRefs := make(map[string]int, len(refs))

	for idx, ref := range refs {
		hasName := ref.Name != ""
		hasGroup := ref.Group != ""

		if hasName == hasGroup {
			return fmt.Errorf(
				"%w: %s[%d] must set exactly one of 'name' or 'group'",
				ErrInvalidTestRef,
				scope,
				idx,
			)
		}

		if hasName {
			if _, ok := tests[ref.Name]; !ok {
				return fmt.Errorf(
					"%w: %s[%d].name references undefined test %#q",
					ErrUndefinedTest,
					scope,
					idx,
					ref.Name,
				)
			}

			refKey := "name:" + ref.Name
			if firstIdx, exists := seenRefs[refKey]; exists {
				return fmt.Errorf(
					"%w: %s[%d] duplicates %s[%d] (%#q)",
					ErrDuplicateTestRef,
					scope,
					idx,
					scope,
					firstIdx,
					ref.Name,
				)
			}

			seenRefs[refKey] = idx

			continue
		}

		if _, ok := groups[ref.Group]; !ok {
			return fmt.Errorf(
				"%w: %s[%d].group references undefined test-group %#q",
				ErrUndefinedTestGroup,
				scope,
				idx,
				ref.Group,
			)
		}

		refKey := "group:" + ref.Group
		if firstIdx, exists := seenRefs[refKey]; exists {
			return fmt.Errorf(
				"%w: %s[%d] duplicates %s[%d] (%#q)",
				ErrDuplicateTestRef,
				scope,
				idx,
				scope,
				firstIdx,
				ref.Group,
			)
		}

		seenRefs[refKey] = idx
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

// validateCustomSourceRef enforces the pairing rules for the 'script', 'mock-packages',
// and 'inputs' fields on a [SourceFileReference]:
//   - 'script' is required when 'origin.type' is 'custom'.
//   - 'script' must be empty when 'origin.type' is not 'custom'.
//   - 'mock-packages' must be empty when 'origin.type' is not 'custom'.
//   - 'inputs' must be empty when 'origin.type' is not 'custom'.
//   - each 'inputs' entry must be a valid filename (no path separators).
//   - each 'inputs' entry must be unique.
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

		if err := validateCustomSourceInputs(ref, componentName); err != nil {
			return err
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

	if len(ref.Origin.Inputs) > 0 {
		return fmt.Errorf(
			"source file %#q in component %#q has 'inputs' set but origin type is %#q; "+
				"'inputs' is only valid when origin type is 'custom'",
			ref.Filename, componentName, string(ref.Origin.Type))
	}

	return nil
}

func validateCustomSourceInputs(ref SourceFileReference, componentName string) error {
	seen := make(map[string]bool, len(ref.Origin.Inputs))

	for _, input := range ref.Origin.Inputs {
		if err := fileutils.ValidateFilename(input); err != nil {
			return fmt.Errorf(
				"invalid 'inputs' entry %#q for source file %#q in component %#q:\n%w",
				input, ref.Filename, componentName, err)
		}

		if seen[input] {
			return fmt.Errorf(
				"duplicate 'inputs' entry %#q for source file %#q in component %#q; each input filename must be unique",
				input, ref.Filename, componentName)
		}

		seen[input] = true

		if input == ref.Origin.Script {
			return fmt.Errorf(
				"'inputs' entry %#q for source file %#q in component %#q conflicts with 'script' filename",
				input, ref.Filename, componentName)
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
