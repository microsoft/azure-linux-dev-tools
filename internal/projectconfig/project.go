// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"dario.cat/mergo"
	"github.com/brunoga/deep"
	"github.com/go-playground/validator/v10"
)

// Encapsulates loaded project configuration.
type ProjectConfig struct {
	// Basic project info.
	Project ProjectInfo `toml:"project,omitempty" json:"project,omitempty" jsonschema:"title=Project Info,description=Basic project information"`
	// Definitions of component groups.
	ComponentGroups map[string]ComponentGroupConfig `toml:"component-groups,omitempty" json:"componentGroups,omitempty" jsonschema:"title=Component Groups,description=Mapping of component group names to configurations"`
	// Definitions of components.
	Components map[string]ComponentConfig `toml:"components,omitempty" json:"components,omitempty" jsonschema:"title=Components,description=Mapping of component names to configurations"`
	// Definitions of images.
	Images map[string]ImageConfig `toml:"images,omitempty" json:"images,omitempty" jsonschema:"title=Images,description=Mapping of image names to configurations"`
	// Definitions of distros.
	Distros map[string]DistroDefinition `toml:"distros,omitempty" json:"distros,omitempty" jsonschema:"title=Distros,description=Mapping of distro names to their definitions"`
	// Configuration for tools used by azldev.
	Tools ToolsConfig `toml:"tools,omitempty" json:"tools,omitempty" jsonschema:"title=Tools configuration,description=Configuration for tools used by azldev"`

	// DefaultComponentConfig is the project-wide default applied to every component before any
	// component-group or component-level config is considered. It is the lowest-priority layer in
	// the component publish config resolution order.
	DefaultComponentConfig ComponentConfig `toml:"default-component-config,omitempty" json:"defaultComponentConfig,omitempty" jsonschema:"title=Default component config,description=Project-wide default applied to all components before group and component overrides"`

	// DefaultPackageConfig is the project-wide default applied to every binary package before any
	// package-group or component-level config is considered. It is the lowest-priority layer in the
	// package config resolution order.
	DefaultPackageConfig PackageConfig `toml:"default-package-config,omitempty" json:"defaultPackageConfig,omitempty" jsonschema:"title=Default package config,description=Project-wide default applied to all binary packages before group and component overrides"`

	// Definitions of package groups with shared configuration.
	PackageGroups map[string]PackageGroupConfig `toml:"package-groups,omitempty" json:"packageGroups,omitempty" jsonschema:"title=Package groups,description=Mapping of package group names to configurations for publish-time routing"`

	// Definitions of test suites.
	TestSuites map[string]TestSuiteConfig `toml:"test-suites,omitempty" json:"testSuites,omitempty" jsonschema:"title=Test Suites,description=Mapping of test suite names to configurations"`

	// Root config file path; not serialized.
	RootConfigFilePath string `toml:"-" json:"-"`
	// Map from component names to groups they belong to; not serialized.
	GroupsByComponent map[string][]string `toml:"-" json:"-"`
}

// Constructs a default (empty) project configuration.
func NewProjectConfig() ProjectConfig {
	return ProjectConfig{
		Project:           ProjectInfo{},
		ComponentGroups:   make(map[string]ComponentGroupConfig),
		Components:        make(map[string]ComponentConfig),
		Images:            make(map[string]ImageConfig),
		Distros:           make(map[string]DistroDefinition),
		GroupsByComponent: make(map[string][]string),
		PackageGroups:     make(map[string]PackageGroupConfig),
		TestSuites:        make(map[string]TestSuiteConfig),
	}
}

// Validates the configuration, returning an error if any semantic errors are found.
func (cfg *ProjectConfig) Validate() error {
	err := validator.New().Struct(cfg)
	if err != nil {
		return fmt.Errorf("config error:\n%w", err)
	}

	if err := validatePackageGroupMembership(cfg.PackageGroups); err != nil {
		return err
	}

	if err := validateImageTestReferences(cfg.Images, cfg.TestSuites); err != nil {
		return err
	}

	return nil
}

// validatePackageGroupMembership checks that no binary package name appears in more than one
// package group. A package may belong to at most one group to keep routing unambiguous, but it
// may also be left ungrouped.
func validatePackageGroupMembership(groups map[string]PackageGroupConfig) error {
	// Track which group each package name was first seen in.
	seenIn := make(map[string]string, len(groups))

	for groupName, group := range groups {
		for _, pkg := range group.Packages {
			if firstGroup, already := seenIn[pkg]; already && firstGroup != groupName {
				return fmt.Errorf(
					"package %#q appears in both package-group %#q and %#q; a package may only belong to one group",
					pkg, firstGroup, groupName,
				)
			}

			seenIn[pkg] = groupName
		}
	}

	return nil
}

// validateImageTestReferences checks that every test suite name in an image's
// [ImageConfig.Tests.TestSuites] list corresponds to a defined entry in the top-level
// TestSuites map.
func validateImageTestReferences(images map[string]ImageConfig, tests map[string]TestSuiteConfig) error {
	for imageName, image := range images {
		for _, testName := range image.TestNames() {
			if _, ok := tests[testName]; !ok {
				return fmt.Errorf(
					"%w: image %#q references test suite %#q, which is not defined in [test-suites]",
					ErrUndefinedTestSuite, imageName, testName,
				)
			}
		}
	}

	return nil
}

// Basic information regarding a project.
type ProjectInfo struct {
	// Human-readable description of this project.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable project description"`

	// Path to log directory to use for this project.
	LogDir string `toml:"log-dir,omitempty" json:"logDir,omitempty" jsonschema:"title=Log Directory,description=Path to the log directory,example=logs"`
	// Path to temp work directory to use for this project.
	WorkDir string `toml:"work-dir,omitempty" json:"workDir,omitempty" jsonschema:"title=Work Directory,description=Path to temporary working directory,example=work"`
	// Path to output directory to use for this project.
	OutputDir string `toml:"output-dir,omitempty" json:"outputDir,omitempty" jsonschema:"title=Output Directory,description=Path to the output directory,example=out"`

	// Path to the output directory for rendered specs (component render).
	RenderedSpecsDir string `toml:"rendered-specs-dir,omitempty" json:"renderedSpecsDir,omitempty" jsonschema:"title=Rendered Specs Directory,description=Output directory for rendered specs,example=SPECS"`

	// Path to the directory for per-component lock files.
	LockDir string `toml:"lock-dir,omitempty" json:"lockDir,omitempty" jsonschema:"title=Lock Directory,description=Directory for per-component lock files,default=locks"`

	// Default-selected distro. May be overridden at runtime.
	DefaultDistro DistroReference `toml:"default-distro,omitempty" json:"defaultDistro,omitempty" jsonschema:"title=Default Distro,description=Default selected distro reference"`

	// Default email address used for synthetic changelog entries and commits
	// when no author email is available (e.g. when no Affects commits exist).
	DefaultAuthorEmail string `toml:"default-author-email,omitempty" json:"defaultAuthorEmail,omitempty" jsonschema:"title=Default Author Email,description=Default email for synthetic changelog entries and commits"`
}

// Mutates the project info, updating it with overrides present in other.
func (p *ProjectInfo) MergeUpdatesFrom(other *ProjectInfo) error {
	err := mergo.Merge(p, other, mergo.WithOverride)
	if err != nil {
		return fmt.Errorf("failed to merge project info:\n%w", err)
	}

	return nil
}

// Returns a copy of the project info with relative file paths converted to absolute
// file paths (relative to referenceDir, not the current working directory).
func (p *ProjectInfo) WithAbsolutePaths(referenceDir string) *ProjectInfo {
	// First deep-copy ourselves.
	//
	// NOTE: We use the panicking MustCopy() because copying should only fail if the input *type*
	// is invalid. Since we're always using the same type, we never expect to see a runtime error
	// here.
	result := deep.MustCopy(p)

	result.LogDir = makeAbsolute(referenceDir, result.LogDir)
	result.WorkDir = makeAbsolute(referenceDir, result.WorkDir)
	result.OutputDir = makeAbsolute(referenceDir, result.OutputDir)
	result.RenderedSpecsDir = makeAbsolute(referenceDir, result.RenderedSpecsDir)
	result.LockDir = makeAbsolute(referenceDir, result.LockDir)

	return result
}
