// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlob(t *testing.T) {
	const testDir = "/test"

	t.Run("NoMatches", func(t *testing.T) {
		ctx := testctx.NewCtx()

		matches, err := fileutils.Glob(ctx.FS(), "/non/existent/*")
		require.NoError(t, err)
		assert.Empty(t, matches)
	})

	t.Run("Matches", func(t *testing.T) {
		ctx := testctx.NewCtx()

		file1 := filepath.Join(testDir, "file1.txt")
		file2 := filepath.Join(testDir, "file2.txt")

		require.NoError(t, fileutils.WriteFile(ctx.FS(), file1, []byte("content"), fileperms.PublicFile))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), file2, []byte("content"), fileperms.PublicFile))

		matches, err := fileutils.Glob(ctx.FS(), filepath.Join(testDir, "*.txt"))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{file1, file2}, matches)
	})

	t.Run("DoubleStarPattern", func(t *testing.T) {
		ctx := testctx.NewCtx()

		subDir := filepath.Join(testDir, "subdir")
		require.NoError(t, ctx.FS().Mkdir(subDir, fileperms.PublicExecutable))

		subSubDir := filepath.Join(subDir, "subdir")
		require.NoError(t, ctx.FS().Mkdir(subSubDir, fileperms.PublicExecutable))

		file1 := filepath.Join(testDir, "file1.txt")
		require.NoError(t, fileutils.WriteFile(ctx.FS(), file1, []byte("content"), fileperms.PublicFile))

		file2 := filepath.Join(subDir, "file2.txt")
		require.NoError(t, fileutils.WriteFile(ctx.FS(), file2, []byte("content"), fileperms.PublicFile))

		file3 := filepath.Join(subSubDir, "file3.txt")
		require.NoError(t, fileutils.WriteFile(ctx.FS(), file3, []byte("content"), fileperms.PublicFile))

		matches, err := fileutils.Glob(ctx.FS(), filepath.Join(testDir, "**", "*.txt"))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{file1, file2, file3}, matches)
	})

	t.Run("DoubleStarPattern_NoMatches", func(t *testing.T) {
		ctx := testctx.NewCtx()

		subDir := filepath.Join(testDir, "subdir")
		require.NoError(t, ctx.FS().Mkdir(subDir, fileperms.PublicDir))

		matches, err := fileutils.Glob(ctx.FS(), filepath.Join(testDir, "**", "*.txt"))
		require.NoError(t, err)
		assert.Empty(t, matches)
	})
}
