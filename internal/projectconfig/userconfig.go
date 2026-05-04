// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
)

// UserConfigSubPath is the path, relative to the XDG config home, where azldev looks for
// user-level configuration overrides. The full default location is
// `${XDG_CONFIG_HOME:-$HOME/.config}/azldev/config.toml`.
const UserConfigSubPath = "azldev/config.toml"

// xdgConfigHome resolves the XDG config home directory using the provided OS environment
// abstraction, following the XDG Base Directory Specification: `$XDG_CONFIG_HOME` is used
// when set to an absolute path; otherwise it falls back to `$HOME/.config`. If neither
// variable yields an absolute path, an empty string is returned.
func xdgConfigHome(osEnv opctx.OSEnv) string {
	if cfgHome := osEnv.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(cfgHome) {
		return cfgHome
	}

	if home := osEnv.Getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config")
	}

	return ""
}

// UserConfigFilePath returns the absolute path to the user-level azldev config file,
// following the XDG Base Directory Specification, resolved against the given OS environment
// abstraction so that callers (including tests) can control environment lookups
// deterministically.
//
// The returned path is the location azldev checks for user-level configuration overrides;
// it does not guarantee that a file actually exists at that path. If the XDG config home
// cannot be determined (e.g. neither `XDG_CONFIG_HOME` nor `HOME` is set to an absolute
// path), an empty string is returned.
func UserConfigFilePath(osEnv opctx.OSEnv) string {
	cfgHome := xdgConfigHome(osEnv)
	if cfgHome == "" {
		return ""
	}

	return filepath.Join(cfgHome, UserConfigSubPath)
}

// findUserConfigFileIfExists returns the absolute path to the user-level azldev config file
// if one exists in the given filesystem; otherwise it returns an empty string. A non-nil
// error is returned only if the existence check itself fails for an unexpected reason.
func findUserConfigFileIfExists(filesystem opctx.FS, osEnv opctx.OSEnv) (string, error) {
	path := UserConfigFilePath(osEnv)
	if path == "" {
		return "", nil
	}

	if _, err := filesystem.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("failed to check for user config file at '%s':\n%w", path, err)
	}

	return path, nil
}
