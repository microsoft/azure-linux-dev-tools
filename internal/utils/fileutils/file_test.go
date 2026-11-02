// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package fileutils_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFileExists(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), "/files"))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), "/files/present", []byte("content"), fileperms.PublicFile))

	for _, testCase := range []struct {
		name    string
		path    string
		wantErr string
	}{
		{name: "file", path: "/files/present"},
		{name: "missing", path: "/files/missing", wantErr: "file not found: `/files/missing`"},
		{name: "directory", path: "/files", wantErr: "`/files` is a directory, expected a file"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := fileutils.ValidateFileExists(ctx.FS(), testCase.path)
			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type statFailureFS struct {
	opctx.FS
}

func (statFailureFS) Stat(string) (os.FileInfo, error) {
	return nil, os.ErrPermission
}

func TestValidateFileExistsAccessError(t *testing.T) {
	ctx := testctx.NewCtx()
	err := fileutils.ValidateFileExists(statFailureFS{FS: ctx.FS()}, "/file")
	require.EqualError(t, err, "cannot access `/file`:\n"+os.ErrPermission.Error())
	require.ErrorIs(t, err, os.ErrPermission)
}

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

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name          string
		filename      string
		expectedError string
	}{
		{name: "valid simple filename", filename: "source.tar.gz"},
		{name: "empty", filename: "", expectedError: "cannot be empty"},
		{name: "dot", filename: ".", expectedError: "not a valid file name"},
		{name: "dotdot", filename: "..", expectedError: "not a valid file name"},
		{name: "absolute path", filename: "/etc/passwd", expectedError: "cannot be an absolute path"},
		{name: "path traversal", filename: "../escape.tar.gz", expectedError: "without directory components"},
		{name: "directory component", filename: "subdir/file.tar.gz", expectedError: "without directory components"},
		{name: "dot prefix traversal", filename: "./file.tar.gz", expectedError: "path traversal"},
		{name: "whitespace in name", filename: "has space.tar.gz", expectedError: "must not contain whitespace"},
		{name: "tab in name", filename: "has\ttab.tar.gz", expectedError: "must not contain whitespace"},
		{name: "null byte in name", filename: "has\x00null.tar.gz", expectedError: "must not contain null bytes"},
		{name: "backslash in name", filename: "foo\\bar.tar.gz", expectedError: "must not contain backslashes"},
		{name: "non-ASCII characters", filename: "foo\x80bar.tar.gz", expectedError: "must contain only ASCII characters"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := fileutils.ValidateFilename(tc.filename)
			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRelPath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		expectedError string
	}{
		{name: "valid single segment", path: "curl.spec"},
		{name: "valid nested", path: "c/curl/curl.spec"},
		{name: "empty", path: "", expectedError: "cannot be empty"},
		{name: "absolute", path: "/etc/passwd", expectedError: "must be relative"},
		{name: "traversal segment", path: "c/../etc/passwd", expectedError: "canonical form"},
		{name: "leading dot segment", path: "./curl.spec", expectedError: "canonical form"},
		{name: "trailing slash", path: "c/curl/", expectedError: "canonical form"},
		{name: "double slash", path: "c//curl.spec", expectedError: "canonical form"},
		{name: "parent ref only", path: "..", expectedError: "not a valid file name"},
		{name: "whitespace segment", path: "c/cu rl/curl.spec", expectedError: "whitespace"},
		{name: "null byte segment", path: "c/curl\x00/curl.spec", expectedError: "null bytes"},
		{name: "backslash segment", path: "c\\curl\\curl.spec", expectedError: "backslashes"},
		{name: "non-ascii segment", path: "c/cu\x80rl/curl.spec", expectedError: "ASCII"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := fileutils.ValidateRelPath(tc.path)
			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
