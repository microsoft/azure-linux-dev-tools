// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

// GoVendorConfig describes a repeatable conversion, not a replacement spec.
type GoVendorConfig struct {
	// Enabled is a pointer so an explicit false overrides inherited settings.
	Enabled *bool `toml:"enabled,omitempty" json:"enabled,omitempty" jsonschema:"title=Enabled,description=Enable Go application vendoring"`
	// ModuleDir is relative to the directory left by the spec's prep section.
	ModuleDir string `toml:"module-dir,omitempty" json:"moduleDir,omitempty" jsonschema:"title=Module directory,description=Relative module directory within the prepared source tree; defaults to its root"`
	// ToolConfig is inline go-vendor-tools TOML, including reviewed license exceptions.
	ToolConfig string `toml:"tool-config,omitempty" json:"toolConfig,omitempty" jsonschema:"title=Tool configuration,description=Inline go-vendor-tools TOML for dependency overrides and licensing settings"`
	// PrepPackages supplies additional tools used by upstream prep commands.
	PrepPackages []string `toml:"prep-packages,omitempty" json:"prepPackages,omitempty" jsonschema:"title=Prep packages,description=Additional RPM packages needed to execute the upstream prep section in mock"`
}

// IsEnabled reports whether vendoring was explicitly enabled.
func (c *GoVendorConfig) IsEnabled() bool {
	return c != nil && c.Enabled != nil && *c.Enabled
}

// Validate checks paths and tool configuration before any source processing.
func (c *GoVendorConfig) Validate() error {
	if c == nil {
		return nil
	}

	if c.ModuleDir != "" {
		if err := fileutils.ValidateRelPath(c.ModuleDir); err != nil {
			return fmt.Errorf("invalid 'module-dir':\n%w", err)
		}

		if strings.ContainsAny(c.ModuleDir, "%\n\r") {
			return errors.New("'module-dir' must not contain RPM macros or newlines")
		}
	}

	var toolConfig map[string]any
	if err := toml.Unmarshal([]byte(c.ToolConfig), &toolConfig); err != nil {
		return fmt.Errorf("invalid 'tool-config' TOML:\n%w", err)
	}

	if general, ok := toolConfig["general"].(map[string]any); ok {
		if _, exists := general["go_mod_dir"]; exists {
			return errors.New("use 'module-dir' instead of 'general.go_mod_dir' in 'tool-config'")
		}
	}

	for _, name := range c.PrepPackages {
		if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("invalid 'prep-packages' entry %#q", name)
		}
	}

	return nil
}
