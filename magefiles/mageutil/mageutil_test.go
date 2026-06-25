// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mageutil_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/magefiles/mageutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLicenseFile(t *testing.T) {
	licenseNames := []string{
		"LICENSE",
		"license",
		"LICENSE.txt",
		"LICENSE.BSD",
		"LICENSE.MPL-2.0",
		"LICENCE",
		"COPYING",
		"COPYING.md",
		"COPYRIGHT",
		"NOTICE",
		"PATENTS",
	}
	for _, name := range licenseNames {
		assert.Truef(t, mageutil.IsLicenseFile(name), "expected %q to be recognized as a license file", name)
	}

	nonLicenseNames := []string{
		"join.go",
		"README.md",
		"go.mod",
		"go.sum",
		"VERSION",
		"codecov.yml",
		"main_license.go",
	}
	for _, name := range nonLicenseNames {
		assert.Falsef(t, mageutil.IsLicenseFile(name), "expected %q to not be recognized as a license file", name)
	}
}

func TestCopyLicenseFilesCopiesOnlyLicenses(t *testing.T) {
	srcDir := t.TempDir()
	destDir := filepath.Join(t.TempDir(), "dest")

	srcFiles := map[string]string{
		"LICENSE.BSD":     "bsd license text",
		"LICENSE.MPL-2.0": "mpl license text",
		"COPYING.md":      "copying text",
		"join.go":         "package securejoin",
		"README.md":       "readme",
	}
	for name, contents := range srcFiles {
		require.NoError(t, os.WriteFile(filepath.Join(srcDir, name), []byte(contents), fileperms.PublicFile))
	}

	// A subdirectory with a license file should not be traversed.
	subDir := filepath.Join(srcDir, "pathrs-lite")
	require.NoError(t, os.MkdirAll(subDir, fileperms.PublicDir))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "LICENSE"), []byte("nested"), fileperms.PublicFile))

	require.NoError(t, mageutil.CopyLicenseFiles(srcDir, destDir))

	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)

	copied := make([]string, 0, len(entries))
	for _, entry := range entries {
		copied = append(copied, entry.Name())
	}

	sort.Strings(copied)
	assert.Equal(t, []string{"COPYING.md", "LICENSE.BSD", "LICENSE.MPL-2.0"}, copied)

	contents, err := os.ReadFile(filepath.Join(destDir, "LICENSE.BSD"))
	require.NoError(t, err)
	assert.Equal(t, "bsd license text", string(contents))
}

func TestCopyLicenseFilesNoLicensesLeavesNoDestDir(t *testing.T) {
	srcDir := t.TempDir()
	destDir := filepath.Join(t.TempDir(), "dest")

	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), fileperms.PublicFile))

	require.NoError(t, mageutil.CopyLicenseFiles(srcDir, destDir))

	_, err := os.Stat(destDir)
	assert.True(t, os.IsNotExist(err), "expected dest dir not to be created when there are no license files")
}
