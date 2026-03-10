// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// The default filename of an Azure Linux project configuration file.
const DefaultConfigFileName string = "azldev.toml"

// Error returned when the config file is not present.
var ErrConfigFileNotFound = errors.New("could not find config file: azldev.toml")

// Starting at referenceDir, locates the containing project root directory
// and its configuration file. Returns ErrConfigFileNotFound if they could not be located.
func FindProjectRootAndConfigFile(
	fs opctx.FS,
	referenceDir string,
) (projectRootDir, configFilePath string, err error) {
	var currentPath string

	// Start at the current (or specified) directory, and keep going up until we find the config file.
	currentPath, err = filepath.Abs(referenceDir)
	if err != nil {
		return projectRootDir, configFilePath, fmt.Errorf("failed to resolve absolute path:\n%w", err)
	}

	for {
		candidatePath := path.Join(currentPath, DefaultConfigFileName)

		if _, statErr := fs.Stat(candidatePath); statErr == nil {
			projectRootDir = currentPath
			configFilePath = candidatePath

			return projectRootDir, configFilePath, nil
		}

		if currentPath == "/" {
			break
		}

		currentPath = path.Dir(currentPath)
	}

	//
	// If we got down here, then we couldn't find the config file in the given
	// directory.
	//

	return projectRootDir, configFilePath, ErrConfigFileNotFound
}
