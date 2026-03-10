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
	}
}

// Validates the configuration, returning an error if any semantic errors are found.
func (cfg *ProjectConfig) Validate() error {
	err := validator.New().Struct(cfg)
	if err != nil {
		return fmt.Errorf("config error:\n%w", err)
	}

	return nil
}

// Type of a project.
type ProjectType string

const (
	// A project that uses the classic Azure Linux toolkit (<= AZL3.0).
	ProjectTypeClassicToolkit ProjectType = "classic-toolkit"
)

// Basic information regarding a project.
type ProjectInfo struct {
	// Type of this project.
	Type ProjectType `toml:"type,omitempty" json:"type,omitempty" validate:"omitempty,oneof='classic-toolkit'" jsonschema:"enum=classic-toolkit,enum=,title=Project Type,description=Type of the project"`
	// Human-readable description of this project.
	Description string `toml:"description,omitempty" json:"description,omitempty" jsonschema:"title=Description,description=Human readable project description"`

	// Path to log directory to use for this project.
	LogDir string `toml:"log-dir,omitempty" json:"logDir,omitempty" jsonschema:"title=Log Directory,description=Path to the log directory,example=logs"`
	// Path to temp work directory to use for this project.
	WorkDir string `toml:"work-dir,omitempty" json:"workDir,omitempty" jsonschema:"title=Work Directory,description=Path to temporary working directory,example=work"`
	// Path to output directory to use for this project.
	OutputDir string `toml:"output-dir,omitempty" json:"outputDir,omitempty" jsonschema:"title=Output Directory,description=Path to the output directory,example=out"`

	// Default-selected distro. May be overridden at runtime.
	DefaultDistro DistroReference `toml:"default-distro,omitempty" json:"defaultDistro,omitempty" jsonschema:"title=Default Distro,description=Default selected distro reference"`
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

	return result
}
