// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package defaultconfigs

import (
	"embed"
	"fmt"
	"path/filepath"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
)

// Embedfs-relative path to the root directory of the embedded filesystem.
const embedfsRootDir = "/content"

// Name of the root default configuration file that should be loaded from these default files.
const rootDefaultConfigFilename = "defaults.toml"

// The embedded filesystem containing compiled-in default configuration files. This configuration
// defines distro-level defaults for consistency.
//
//go:embed content/*
var content embed.FS

// CopyTo recursively copies the contents of the [content] embedded filesystem to the specified
// destination path in the provided filesystem. Returns the path to the root configuration file
// to load from the copied directory.
func CopyTo(dryRunnable opctx.DryRunnable, fs opctx.FS, destPath string) (rootConfigFilePath string, err error) {
	sourceFS := fileutils.WrapEmbedFS(&content)

	err = fileutils.CopyDirRecursiveCrossFS(
		dryRunnable,
		sourceFS, embedfsRootDir,
		fs, destPath,
		fileutils.CopyDirOptions{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to copy default configs to '%s': %w", destPath, err)
	}

	return filepath.Join(destPath, rootDefaultConfigFilename), nil
}
