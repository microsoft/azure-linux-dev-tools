// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// UserConfigSubPath is the path, relative to the XDG config home, where azldev looks for
// user-level configuration overrides. The full default location is
// `${XDG_CONFIG_HOME:-$HOME/.config}/azldev/config.toml`.
const UserConfigSubPath = "azldev/config.toml"

// UserConfigFilePath returns the absolute path to the user-level azldev config file,
// following the XDG Base Directory Specification.
//
// The returned path is the location azldev checks for user-level configuration overrides;
// it does not guarantee that a file actually exists at that path.
func UserConfigFilePath() string {
	return filepath.Join(xdg.ConfigHome, UserConfigSubPath)
}

// findUserConfigFileIfExists returns the absolute path to the user-level azldev config file
// if one exists in the given filesystem; otherwise it returns an empty string. A non-nil
// error is returned only if the existence check itself fails for an unexpected reason.
func findUserConfigFileIfExists(filesystem opctx.FS) (string, error) {
	path := UserConfigFilePath()

	if _, err := filesystem.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("failed to check for user config file at '%s':\n%w", path, err)
	}

	return path, nil
}
