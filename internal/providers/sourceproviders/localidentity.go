// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sourceproviders

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
)

// ResolveLocalSourceIdentity computes a SHA256 hash over all files in the given
// spec directory (spec file + sidecar files like patches and scripts).
// Files are sorted by path for determinism. Returns an empty string if the
// directory contains no files.
func ResolveLocalSourceIdentity(filesystem opctx.FS, specDir string) (string, error) {
	if specDir == "" {
		return "", errors.New("spec directory cannot be empty")
	}

	// Collect all files in the spec directory.
	var filePaths []string

	err := afero.Walk(filesystem, specDir,
		func(path string, info fs.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			if !info.IsDir() {
				filePaths = append(filePaths, path)
			}

			return nil
		})
	if err != nil {
		return "", fmt.Errorf("walking spec directory %#q:\n%w", specDir, err)
	}

	if len(filePaths) == 0 {
		return "", fmt.Errorf("spec directory %#q contains no files", specDir)
	}

	// Sort for determinism across runs.
	sort.Strings(filePaths)

	// Hash each file and combine into a single digest.
	combinedHasher := sha256.New()

	for _, filePath := range filePaths {
		fileHash, hashErr := fileutils.ComputeFileHash(
			filesystem, fileutils.HashTypeSHA256, filePath,
		)
		if hashErr != nil {
			return "", fmt.Errorf("hashing file %#q:\n%w", filePath, hashErr)
		}

		relPath, relErr := filepath.Rel(specDir, filePath)
		if relErr != nil {
			return "", fmt.Errorf("computing relative path for %#q:\n%w", filePath, relErr)
		}

		fmt.Fprintf(combinedHasher, "%s=%s\n", relPath, fileHash)
	}

	return "sha256:" + hex.EncodeToString(combinedHasher.Sum(nil)), nil
}
