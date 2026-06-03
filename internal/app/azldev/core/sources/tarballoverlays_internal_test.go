// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package sources

import (
	"os"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupOverlaysByTarball(t *testing.T) {
	t.Run("groups overlays by tarball name preserving order", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{
				Type:     projectconfig.ComponentOverlayTarballFileRemove,
				Tarball:  "pkg-1.0.tar.gz",
				Filename: "unwanted.conf",
			},
			{
				Type:        projectconfig.ComponentOverlayTarballSearchReplace,
				Tarball:     "pkg-1.0.tar.gz",
				Filename:    "config.h",
				Regex:       "old",
				Replacement: "new",
			},
			{
				Type:     projectconfig.ComponentOverlayTarballFileRemove,
				Tarball:  "other-2.0.tar.xz",
				Filename: "docs/*.md",
			},
		}

		groups := groupOverlaysByTarball(overlays)

		require.Len(t, groups, 2)

		assert.Equal(t, "pkg-1.0.tar.gz", groups[0].tarball)
		require.Len(t, groups[0].overlays, 2)
		assert.Equal(t, projectconfig.ComponentOverlayTarballFileRemove, groups[0].overlays[0].Type)
		assert.Equal(t, projectconfig.ComponentOverlayTarballSearchReplace, groups[0].overlays[1].Type)

		assert.Equal(t, "other-2.0.tar.xz", groups[1].tarball)
		require.Len(t, groups[1].overlays, 1)
	})

	t.Run("skips non-tarball overlays", func(t *testing.T) {
		overlays := []projectconfig.ComponentOverlay{
			{Type: projectconfig.ComponentOverlaySetSpecTag, Tag: "Version", Value: "1.0"},
			{Type: projectconfig.ComponentOverlayTarballFileRemove, Tarball: "pkg.tar.gz", Filename: "f"},
			{Type: projectconfig.ComponentOverlayAddFile, Filename: "new.txt", Source: "src"},
		}

		groups := groupOverlaysByTarball(overlays)

		require.Len(t, groups, 1)
		assert.Equal(t, "pkg.tar.gz", groups[0].tarball)
		require.Len(t, groups[0].overlays, 1)
	})
}

func TestGlobFilesInDir(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.MkdirAll(workDir+"/sub", 0o755))
	require.NoError(t, os.WriteFile(workDir+"/file.txt", nil, fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(workDir+"/sub/deep.txt", nil, fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(workDir+"/sub/other.md", nil, fileperms.PrivateFile))

	t.Run("simple glob", func(t *testing.T) {
		matches, err := globFilesInDir(workDir, "*.txt")
		require.NoError(t, err)
		require.Len(t, matches, 1)
	})

	t.Run("doublestar glob", func(t *testing.T) {
		matches, err := globFilesInDir(workDir, "**/*.txt")
		require.NoError(t, err)
		require.Len(t, matches, 2)
	})

	t.Run("no matches", func(t *testing.T) {
		matches, err := globFilesInDir(workDir, "*.rs")
		require.NoError(t, err)
		assert.Empty(t, matches)
	})
}

func TestTarballFileRemove(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(workDir+"/keep.txt", []byte("keep"), fileperms.PrivateFile))
	require.NoError(t, os.WriteFile(workDir+"/remove.conf", []byte("remove"), fileperms.PrivateFile))

	err := tarballFileRemove(workDir, "*.conf")
	require.NoError(t, err)

	assert.FileExists(t, workDir+"/keep.txt")
	assert.NoFileExists(t, workDir+"/remove.conf")
}

func TestTarballFileRemoveNoMatch(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(workDir+"/file.txt", nil, fileperms.PrivateFile))

	err := tarballFileRemove(workDir, "*.conf")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOverlayDidNotApply)
}

func TestTarballSearchReplace(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(workDir+"/config.h", []byte("#define OLD_VALUE 1\n"), fileperms.PrivateFile))

	err := tarballSearchReplace(workDir, "config.h", "OLD_VALUE", "NEW_VALUE")
	require.NoError(t, err)

	content, readErr := os.ReadFile(workDir + "/config.h")
	require.NoError(t, readErr)
	assert.Equal(t, "#define NEW_VALUE 1\n", string(content))
}

func TestTarballSearchReplaceNoMatch(t *testing.T) {
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(workDir+"/config.h", []byte("#define SOMETHING 1\n"), fileperms.PrivateFile))

	err := tarballSearchReplace(workDir, "config.h", "NONEXISTENT", "new")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOverlayDidNotApply)
}
