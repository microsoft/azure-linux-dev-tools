// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"dario.cat/mergo"
)

// Encapsulates information about the tools used by the image customizer.
type ImageCustomizerConfig struct {
	// Full tag name of the Image Customizer container.
	ContainerTag string `toml:"containerTag,omitempty" json:"containerTag,omitempty" jsonschema:"title=Container tag,description=Container tag of the Image Customizer container"`
}

// Encapsulates information about the tools used by azldev.
type ToolsConfig struct {
	// Configuration for the Image Customizer tool.
	ImageCustomizer ImageCustomizerConfig `toml:"imageCustomizer,omitempty" json:"imageCustomizer,omitempty" jsonschema:"title=Image Customizer tool configuration,description=Configuration for the Image Customizer tool"`
}

func (tc *ToolsConfig) MergeUpdatesFrom(other *ToolsConfig) error {
	err := mergo.Merge(tc, other, mergo.WithOverride)
	if err != nil {
		return fmt.Errorf("failed to merge tools configuration:\n%w", err)
	}

	return nil
}
