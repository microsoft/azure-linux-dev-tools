// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"fmt"

	"github.com/microsoft/azure-linux-dev-tools/defaultconfigs"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// LoadProjectConfig takes a reference directory, searches for the project's configuration, and loads it
// with any appropriate default configurations appropriately incorporated. If needed, this function
// may make use of the provided temporary directory, with the expectation that the caller is responsible
// for cleaning it up -- but not until after it is done using the loaded configuration. The loaded
// configuration may implicitly depend on the contents of the temporary directory.
func LoadProjectConfig(
	dryRunnable opctx.DryRunnable,
	fs opctx.FS,
	referenceDir string,
	disableDefaultConfig bool,
	tempDirPath string,
	extraConfigFilePaths []string,
	permissiveConfigParsing bool,
) (projectDir string, config *ProjectConfig, err error) {
	// Look for project root and azldev.toml file.
	projectDir, projectFilePath, err := FindProjectRootAndConfigFile(fs, referenceDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to find project root and config file:\n%w", err)
	}

	configFilePaths := []string{}

	// Unless we were explicitly requested not to do so, copy our embedded default config files to a temporary
	// location.
	if !disableDefaultConfig {
		tempConfigDirPath, err := fileutils.MkdirTemp(fs, tempDirPath, "azl-defconfigs-")
		if err != nil {
			return "", nil, fmt.Errorf("failed to create temp dir for default config files:\n%w", err)
		}

		defaultConfigFilePath, err := defaultconfigs.CopyTo(dryRunnable, fs, tempConfigDirPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to copy default config files:\n%w", err)
		}

		configFilePaths = append(configFilePaths, defaultConfigFilePath)
	}

	// Load the project config file next.
	configFilePaths = append(configFilePaths, projectFilePath)

	// Append any extra config files specified by the user (e.g., via --config-file flags).
	// These are loaded last, so they can override/merge with settings from the project config.
	configFilePaths = append(configFilePaths, extraConfigFilePaths...)

	// Actually load and process the config file (and any linked config files it references).
	//
	// NOTE: We don't wrap the error returned back here (if one is returned) because we already have
	// a decent one coming from this function.
	config, err = loadAndResolveProjectConfig(fs, permissiveConfigParsing, configFilePaths...)
	if err != nil {
		return "", nil, err
	}

	// Fill in the root config file path in the config object; it won't be serialized.
	config.RootConfigFilePath = projectFilePath

	return projectDir, config, nil
}
