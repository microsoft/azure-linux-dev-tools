// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveAllAndUpdateErrorIfNil_NonExistentDir(t *testing.T) {
	ctx := testctx.NewCtx()

	incomingErr := errors.New("incoming error")

	err := incomingErr
	fileutils.RemoveAllAndUpdateErrorIfNil(ctx.FS(), "/non/existent", &err)
	require.ErrorIs(t, err, incomingErr)

	err = nil
	fileutils.RemoveAllAndUpdateErrorIfNil(ctx.FS(), "/non/existent", &err)
	assert.NoError(t, err)
}

func TestRemoveAllAndUpdateErrorIfNil_Success(t *testing.T) {
	const (
		testDirRoot = "/some/dir"
		subdirPath  = "/some/dir/subdir"
	)

	// Set up some files in the test filesystem.
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), subdirPath))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(subdirPath, "file.txt"),
		[]byte("content"), fileperms.PrivateFile))

	var err error

	fileutils.RemoveAllAndUpdateErrorIfNil(ctx.FS(), testDirRoot, &err)

	// Make sure the dir is gone and success was reported.
	if assert.NoError(t, err) {
		_, statErr := ctx.FS().Stat(testDirRoot)
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	}
}

func TestReadDir_DoesNotExist(t *testing.T) {
	ctx := testctx.NewCtx()

	entries, err := fileutils.ReadDir(ctx.FS(), "/non-existent")
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, entries)
}

func TestReadDir_Exists(t *testing.T) {
	ctx := testctx.NewCtx()

	// Setup a directory with some files.
	testDir := "/testdir"
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testDir))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(testDir, "file1.txt"), []byte("content"), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(testDir, "file2.txt"), []byte("content"), fileperms.PublicFile))
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Join(testDir, "subdir")))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(testDir, "subdir", "file3.txt"), []byte("content"), fileperms.PublicFile))

	// Read!
	entries, err := fileutils.ReadDir(ctx.FS(), testDir)
	require.NoError(t, err)

	entryNames := lo.Map(entries, func(entry os.FileInfo, _ int) string {
		return entry.Name()
	})

	assert.ElementsMatch(t, []string{"file1.txt", "file2.txt", "subdir"}, entryNames)
}
