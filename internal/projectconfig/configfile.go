// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
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

	// Internal fields used to track the origin of the config file; `dir` is the directory
	// that the config file's relative paths are based from.
	sourcePath string `toml:"-"`
	dir        string `toml:"-" validate:"dir"`
}

// Validates the format and internal consistency of the config file. Semantic errors are reported.
func (f ConfigFile) Validate() error {
	err := validator.New().Struct(f)
	if err != nil {
		return fmt.Errorf("config file error:\n%w", err)
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
	const defaultPerms = 0o644

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
