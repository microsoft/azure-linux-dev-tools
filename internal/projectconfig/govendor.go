// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/pelletier/go-toml/v2"
)

// GoVendorConfig describes a repeatable conversion, not a replacement spec.
type GoVendorConfig struct {
	// Enabled is a pointer so an explicit false overrides inherited settings.
	Enabled *bool `toml:"enabled,omitempty" json:"enabled,omitempty" jsonschema:"title=Enabled,description=Enable Go application vendoring"`
	// ModuleDir is relative to the directory left by the spec's prep section.
	ModuleDir string `toml:"module-dir,omitempty" json:"moduleDir,omitempty" jsonschema:"title=Module directory,description=Relative module directory within the prepared source tree; defaults to its root"`
	// ConfigFile identifies a native go-vendor-tools TOML file, relative to the declaring
	// config file. Resolved to an absolute path; its contents are fingerprinted separately.
	ConfigFile string `toml:"config-file,omitempty" json:"configFile,omitempty" jsonschema:"title=Vendor configuration file,description=Path to a go-vendor-tools TOML file relative to the declaring component configuration. Replaces upstream settings when specified,example=go-vendor-tools.toml" fingerprint:"-"`
	// PrepPackages supplies additional tools used by upstream prep commands.
	PrepPackages []string `toml:"prep-packages,omitempty" json:"prepPackages,omitempty" jsonschema:"title=Prep packages,description=Additional RPM packages needed to execute the upstream prep section in mock"`
}

// IsEnabled reports whether vendoring was explicitly enabled.
func (c *GoVendorConfig) IsEnabled() bool {
	return c != nil && c.Enabled != nil && *c.Enabled
}

// Validate checks configuration values before any source processing.
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

	if strings.ContainsAny(c.ConfigFile, "\x00\r\n") {
		return errors.New("'config-file' must be a local file path without NUL or newlines")
	}

	for _, name := range c.PrepPackages {
		if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("invalid 'prep-packages' entry %#q", name)
		}
	}

	return nil
}

// SourceContentIdentity hashes the selected configuration independently of its path.
// No configured file yields no identity; an explicitly empty file still has a hash.
func (c *GoVendorConfig) SourceContentIdentity(fs opctx.FS) (string, error) {
	if c == nil || c.ConfigFile == "" {
		return "", nil
	}

	content, err := c.ReadConfigFile(fs)
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256([]byte(content))

	return hex.EncodeToString(digest[:]), nil
}

// ReadConfigFile reads and validates the explicitly selected native configuration.
// An empty file is a valid, intentional replacement; callers must not fall back to upstream settings.
func (c *GoVendorConfig) ReadConfigFile(fs opctx.FS) (string, error) {
	data, err := fileutils.ReadFile(fs, c.ConfigFile)
	if err != nil {
		return "", fmt.Errorf("reading 'go-vendor.config-file' %#q:\n%w", c.ConfigFile, err)
	}

	var settings map[string]any
	if err := toml.Unmarshal(data, &settings); err != nil {
		return "", fmt.Errorf("invalid TOML in 'go-vendor.config-file' %#q:\n%w", c.ConfigFile, err)
	}

	if general, ok := settings["general"].(map[string]any); ok {
		if _, exists := general["go_mod_dir"]; exists {
			return "", fmt.Errorf("use 'module-dir' instead of 'general.go_mod_dir' in %#q", c.ConfigFile)
		}
	}

	return string(data), nil
}
