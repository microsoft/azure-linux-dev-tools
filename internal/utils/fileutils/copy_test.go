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

func TestCopyFile(t *testing.T) {
	const (
		testSourcePerms = fileperms.PrivateFile | os.ModeSticky
		testSourcePath  = "/source.txt"
		testDestPath    = "/dest.txt"
		content         = "Hello, World!"
	)

	t.Run("FailsWithNonExistentSource", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Run the code-under-test
		err := fileutils.CopyFile(ctx, ctx.FS(), "/non/existent", "/dest.txt", fileutils.CopyFileOptions{})
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("Success", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), testSourcePath, []byte(content), testSourcePerms)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.CopyFile(ctx, ctx.FS(), testSourcePath, testDestPath, fileutils.CopyFileOptions{})
		require.NoError(t, err)

		// Check post-conditions
		destContent, err := fileutils.ReadFile(ctx.FS(), testDestPath)
		require.NoError(t, err)
		assert.Equal(t, content, string(destContent))

		// Confirm that mode bits were *not* copied and the sticky bit should be gone.
		info, err := ctx.FS().Stat(testDestPath)
		require.NoError(t, err)
		assert.Zero(t, info.Mode()&os.ModeSticky)
	})

	t.Run("SuccessWithPreservedMode", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), testSourcePath, []byte(content), testSourcePerms)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.CopyFile(ctx, ctx.FS(), testSourcePath, testDestPath,
			fileutils.CopyFileOptions{PreserveFileMode: true})
		require.NoError(t, err)

		// Confirm that mode bits *were* copied and the sticky bit should be present.
		info, err := ctx.FS().Stat(testDestPath)
		require.NoError(t, err)
		assert.Equal(t, testSourcePerms, info.Mode())
	})
}

func TestCopyDirRecursive(t *testing.T) {
	const (
		testSourcePerms = fileperms.PrivateFile | os.ModeSticky

		sourceDirPath = "/source"
		destDirPath   = "/dest"

		content1 = "File 1 content"
		content2 = "File 2 content"
	)

	t.Run("FailsWithNonExistentSource", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Run the code-under-test
		err := fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, fileutils.CopyDirOptions{})
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("FailsWithNonDirSource", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), sourceDirPath, []byte(content1), testSourcePerms)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, fileutils.CopyDirOptions{})
		require.Error(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		ctx := testctx.NewCtx()

		sourceFilePath1 := filepath.Join(sourceDirPath, "file1.txt")
		sourceFilePath2 := filepath.Join(sourceDirPath, "subdir", "file2.txt")

		// Setup preconditions
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath1, []byte(content1), fileperms.PrivateFile))
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(sourceFilePath2)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath2, []byte(content2), fileperms.PrivateFile))

		// Run the code-under-test
		err := fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, fileutils.CopyDirOptions{})
		require.NoError(t, err)

		// Check post-conditions
		destFilePath1 := filepath.Join(destDirPath, "file1.txt")
		destFilePath2 := filepath.Join(destDirPath, "subdir", "file2.txt")

		destContent1, err := fileutils.ReadFile(ctx.FS(), destFilePath1)
		if assert.NoError(t, err) {
			assert.Equal(t, content1, string(destContent1))
		}

		destContent2, err := fileutils.ReadFile(ctx.FS(), destFilePath2)
		if assert.NoError(t, err) {
			assert.Equal(t, content2, string(destContent2))
		}
	})

	t.Run("ConfirmDoesNotPreserveDirMode", func(t *testing.T) {
		const (
			extraDirPerms      = os.ModeSticky
			testSourceDirPerms = fileperms.PrivateDir | extraDirPerms
		)

		ctx := testctx.NewCtx()

		// Setup preconditions
		require.NoError(t, ctx.FS().MkdirAll(sourceDirPath, testSourceDirPerms))

		// Confirm that we see the mode bits we're expecting to see.
		info, err := ctx.FS().Stat(sourceDirPath)
		require.NoError(t, err)
		assert.Equal(t, testSourceDirPerms, info.Mode()&testSourceDirPerms)

		// Run the code-under-test
		err = fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, fileutils.CopyDirOptions{})
		require.NoError(t, err)

		// Confirm that mode bits were *not* copied and the extra bits should be gone.
		info, err = ctx.FS().Stat(destDirPath)
		require.NoError(t, err)
		assert.Zero(t, info.Mode()&extraDirPerms)
	})

	t.Run("FileFilterSkipsMatchingFiles", func(t *testing.T) {
		ctx := testctx.NewCtx()

		sourceFilePath1 := filepath.Join(sourceDirPath, "file1.txt")
		sourceFilePath2 := filepath.Join(sourceDirPath, "file2.txt")

		// Setup preconditions
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath1, []byte(content1), fileperms.PrivateFile))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath2, []byte(content2), fileperms.PrivateFile))

		// Use a filter that skips file1.txt
		options := fileutils.CopyDirOptions{
			FileFilter: func(_ opctx.FS, destPath string) (bool, error) {
				return filepath.Base(destPath) != "file1.txt", nil
			},
		}

		err := fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, options)
		require.NoError(t, err)

		// file1.txt should not exist at destination
		exists, err := fileutils.Exists(ctx.FS(), filepath.Join(destDirPath, "file1.txt"))
		require.NoError(t, err)
		assert.False(t, exists)

		// file2.txt should exist at destination
		destContent2, err := fileutils.ReadFile(ctx.FS(), filepath.Join(destDirPath, "file2.txt"))
		require.NoError(t, err)
		assert.Equal(t, content2, string(destContent2))
	})

	t.Run("SkipExistingFilesPreservesExisting", func(t *testing.T) {
		const existingContent = "existing content"

		ctx := testctx.NewCtx()

		sourceFilePath1 := filepath.Join(sourceDirPath, "file1.txt")
		sourceFilePath2 := filepath.Join(sourceDirPath, "file2.txt")

		// Setup source files
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath1, []byte(content1), fileperms.PrivateFile))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), sourceFilePath2, []byte(content2), fileperms.PrivateFile))

		// Pre-create file1.txt at destination with different content
		destFilePath1 := filepath.Join(destDirPath, "file1.txt")
		require.NoError(t, fileutils.WriteFile(ctx.FS(), destFilePath1, []byte(existingContent), fileperms.PrivateFile))

		options := fileutils.CopyDirOptions{
			FileFilter: fileutils.SkipExistingFiles,
		}

		err := fileutils.CopyDirRecursive(ctx, ctx.FS(), sourceDirPath, destDirPath, options)
		require.NoError(t, err)

		// file1.txt should retain its original content (not overwritten)
		destContent1, err := fileutils.ReadFile(ctx.FS(), destFilePath1)
		require.NoError(t, err)
		assert.Equal(t, existingContent, string(destContent1))

		// file2.txt should be copied since it didn't exist
		destContent2, err := fileutils.ReadFile(ctx.FS(), filepath.Join(destDirPath, "file2.txt"))
		require.NoError(t, err)
		assert.Equal(t, content2, string(destContent2))
	})
}

func TestSymLinkOrCopy(t *testing.T) {
	const (
		testSourcePath = "/source.txt"
		testDestPath   = "/dest.txt"
		content        = "Hello, World!"
	)

	t.Run("FallsBackToCopyOnMemMapFs", func(t *testing.T) {
		// testctx.NewCtx() uses an in-memory filesystem (MemMapFs), so symlinking
		// is not supported. SymLinkOrCopy should fall back to copying the file.
		ctx := testctx.NewCtx()

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), testSourcePath, []byte(content), fileperms.PrivateFile)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.SymLinkOrCopy(ctx, ctx.FS(), testSourcePath, testDestPath, fileutils.CopyFileOptions{})
		require.NoError(t, err)

		// Check post-conditions - file should have been copied
		destContent, err := fileutils.ReadFile(ctx.FS(), testDestPath)
		require.NoError(t, err)
		assert.Equal(t, content, string(destContent))
	})

	t.Run("FailsWithNonExistentSource", func(t *testing.T) {
		ctx := testctx.NewCtx()

		// Run the code-under-test
		err := fileutils.SymLinkOrCopy(ctx, ctx.FS(), "/non/existent", testDestPath, fileutils.CopyFileOptions{})
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("PreservesFileModeOnFallback", func(t *testing.T) {
		const testSourcePerms = fileperms.PrivateFile | os.ModeSticky

		ctx := testctx.NewCtx()

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), testSourcePath, []byte(content), testSourcePerms)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.SymLinkOrCopy(ctx, ctx.FS(), testSourcePath, testDestPath,
			fileutils.CopyFileOptions{PreserveFileMode: true})
		require.NoError(t, err)

		// Check post-conditions - file mode should be preserved
		info, err := ctx.FS().Stat(testDestPath)
		require.NoError(t, err)
		assert.Equal(t, testSourcePerms, info.Mode())
	})

	t.Run("DryRunDoesNotCopy", func(t *testing.T) {
		ctx := testctx.NewCtx()
		ctx.DryRunValue = true

		// Setup preconditions
		err := fileutils.WriteFile(ctx.FS(), testSourcePath, []byte(content), fileperms.PrivateFile)
		require.NoError(t, err)

		// Run the code-under-test
		err = fileutils.SymLinkOrCopy(ctx, ctx.FS(), testSourcePath, testDestPath, fileutils.CopyFileOptions{})
		require.NoError(t, err)

		// Check post-conditions - destination should not exist in dry-run mode
		exists, err := fileutils.Exists(ctx.FS(), testDestPath)
		require.NoError(t, err)
		assert.False(t, exists)
	})
}
