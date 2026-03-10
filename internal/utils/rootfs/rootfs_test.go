// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package rootfs_test tests the rootfs package.
//
// NOTE: These tests intentionally use the real filesystem via [testing.T.TempDir] rather than
// [afero.MemMapFs] or testctx. This is required because rootfs provides security guarantees
// (symlink escape protection via kernel-level openat2/RESOLVE_BENEATH) that can only be
// verified with actual kernel behavior. The in-memory filesystem cannot replicate these
// security semantics.
package rootfs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/rootfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------------
// Constructor and Lifecycle Tests
// -----------------------------------------------------------------------------

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)
		require.NotNil(t, rfs)

		err = rfs.Close()
		require.NoError(t, err)
	})

	t.Run("NonExistentPath", func(t *testing.T) {
		t.Parallel()

		_, err := rootfs.New("/non/existent/path/that/does/not/exist")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "opening root")
	})

	t.Run("FileInsteadOfDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		filePath := filepath.Join(rootDir, "file.txt")

		err := os.WriteFile(filePath, []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		_, err = rootfs.New(filePath)
		require.Error(t, err)
	})
}

func TestRootFs_Close(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()

	rfs, err := rootfs.New(rootDir)
	require.NoError(t, err)

	err = rfs.Close()
	require.NoError(t, err)
}

func TestRootFs_Name(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()

	rfs, err := rootfs.New(rootDir)
	require.NoError(t, err)

	defer rfs.Close()

	assert.Equal(t, "RootFs", rfs.Name())
}

// -----------------------------------------------------------------------------
// File Operation Tests
// -----------------------------------------------------------------------------

func TestRootFs_Create(t *testing.T) {
	t.Parallel()

	t.Run("SimpleFile", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		file, err := rfs.Create("test.txt")
		require.NoError(t, err)
		require.NotNil(t, file)

		_, err = file.WriteString("hello world")
		require.NoError(t, err)

		err = file.Close()
		require.NoError(t, err)

		// Verify file exists on real filesystem.
		content, err := os.ReadFile(filepath.Join(rootDir, "test.txt"))
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(content))
	})

	t.Run("NestedPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Create should fail for nested paths since Create doesn't auto-create parents.
		_, err = rfs.Create("nested/dir/test.txt")
		require.Error(t, err)
	})
}

func TestRootFs_Open(t *testing.T) {
	t.Parallel()

	t.Run("ExistingFile", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a file directly on the filesystem.
		err := os.WriteFile(filepath.Join(rootDir, "existing.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		file, err := rfs.Open("existing.txt")
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 7)
		n, err := file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 7, n)
		assert.Equal(t, "content", string(buf))
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		_, err = rfs.Open("does-not-exist.txt")
		require.ErrorIs(t, err, fs.ErrNotExist)
		// Also verify os.IsNotExist works (required for afero compatibility).
		assert.True(t, os.IsNotExist(err), "os.IsNotExist should return true for non-existent file error")
	})

	t.Run("Directory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "subdir"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Opening a directory should succeed.
		dir, err := rfs.Open("subdir")
		require.NoError(t, err)

		defer dir.Close()

		info, err := dir.Stat()
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("NonExistentFileErrorIs", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		_, err = rfs.Open("does-not-exist.txt")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestRootFs_OpenFile(t *testing.T) {
	t.Parallel()

	t.Run("CreateWithFlags", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		file, err := rfs.OpenFile("created.txt", os.O_CREATE|os.O_WRONLY, fileperms.PrivateFile)
		require.NoError(t, err)

		_, err = file.WriteString("created content")
		require.NoError(t, err)

		err = file.Close()
		require.NoError(t, err)

		// Verify file exists.
		content, err := os.ReadFile(filepath.Join(rootDir, "created.txt"))
		require.NoError(t, err)
		assert.Equal(t, "created content", string(content))
	})

	t.Run("CreateWithNestedPathFailsWithoutParentDirs", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// OpenFile with O_CREATE should NOT auto-create parent directories (standard behavior).
		// Callers must use MkdirAll explicitly.
		_, err = rfs.OpenFile("deep/nested/path/file.txt", os.O_CREATE|os.O_WRONLY, fileperms.PrivateFile)
		require.Error(t, err)
	})

	t.Run("ExclusiveCreate", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create existing file.
		err := os.WriteFile(filepath.Join(rootDir, "exists.txt"), []byte("existing"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// O_EXCL should fail if file exists.
		_, err = rfs.OpenFile("exists.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileperms.PrivateFile)
		require.ErrorIs(t, err, fs.ErrExist)
		// Also verify os.IsExist works (required for afero.TempFile compatibility).
		assert.True(t, os.IsExist(err), "os.IsExist should return true for file-exists error")
	})

	t.Run("Truncate", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create file with content.
		err := os.WriteFile(filepath.Join(rootDir, "truncate.txt"), []byte("original content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Open with O_TRUNC should truncate the file.
		file, err := rfs.OpenFile("truncate.txt", os.O_WRONLY|os.O_TRUNC, fileperms.PrivateFile)
		require.NoError(t, err)

		_, err = file.WriteString("new")
		require.NoError(t, err)

		err = file.Close()
		require.NoError(t, err)

		content, err := os.ReadFile(filepath.Join(rootDir, "truncate.txt"))
		require.NoError(t, err)
		assert.Equal(t, "new", string(content))
	})

	t.Run("ReadOnly", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "readonly.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		file, err := rfs.OpenFile("readonly.txt", os.O_RDONLY, 0)
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 7)
		_, err = file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "content", string(buf))
	})
}

// -----------------------------------------------------------------------------
// Directory Operation Tests
// -----------------------------------------------------------------------------

func TestRootFs_Mkdir(t *testing.T) {
	t.Parallel()

	t.Run("SingleDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Mkdir("newdir", fileperms.PrivateDir)
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "newdir"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("NestedDirectoryFails", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Mkdir (not MkdirAll) should fail for nested paths.
		err = rfs.Mkdir("nested/dir", fileperms.PrivateDir)
		require.Error(t, err)
	})

	t.Run("ExistingDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "existing"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Mkdir on existing directory should fail.
		err = rfs.Mkdir("existing", fileperms.PrivateDir)
		require.ErrorIs(t, err, fs.ErrExist)
		// Also verify os.IsExist works (required for afero.TempDir compatibility).
		assert.True(t, os.IsExist(err), "os.IsExist should return true for directory-exists error")
	})
}

func TestRootFs_MkdirAll(t *testing.T) {
	t.Parallel()

	t.Run("DeepNestedPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.MkdirAll("a/b/c/d/e", fileperms.PrivateDir)
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "a/b/c/d/e"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("PartiallyExistingPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.MkdirAll(filepath.Join(rootDir, "partial/existing"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Should succeed even if some directories exist.
		err = rfs.MkdirAll("partial/existing/new/path", fileperms.PrivateDir)
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "partial/existing/new/path"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("DotPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// "." should return early without error.
		err = rfs.MkdirAll(".", fileperms.PrivateDir)
		require.NoError(t, err)
	})

	t.Run("ExistingDirectoryNoError", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "existing"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// MkdirAll should not error on existing directories.
		err = rfs.MkdirAll("existing", fileperms.PrivateDir)
		require.NoError(t, err)
	})
}

// -----------------------------------------------------------------------------
// Removal Tests
// -----------------------------------------------------------------------------

func TestRootFs_Remove(t *testing.T) {
	t.Parallel()

	t.Run("File", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "remove.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Remove("remove.txt")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootDir, "remove.txt"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("EmptyDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "emptydir"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Remove("emptydir")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootDir, "emptydir"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("NonEmptyDirectoryFails", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "nonempty"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "nonempty/file.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Remove should fail on non-empty directory.
		err = rfs.Remove("nonempty")
		require.Error(t, err)
	})

	t.Run("NonExistentPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Remove("does-not-exist")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestRootFs_RemoveAll(t *testing.T) {
	t.Parallel()

	t.Run("File", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "file.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.RemoveAll("file.txt")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootDir, "file.txt"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("NestedDirectoryTree", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a nested structure.
		err := os.MkdirAll(filepath.Join(rootDir, "tree/branch1/leaf"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.MkdirAll(filepath.Join(rootDir, "tree/branch2"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "tree/root.txt"), []byte("root"), fileperms.PrivateFile)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "tree/branch1/file.txt"), []byte("branch1"), fileperms.PrivateFile)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "tree/branch1/leaf/deep.txt"), []byte("leaf"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.RemoveAll("tree")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootDir, "tree"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("NonExistentPathSucceeds", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// RemoveAll should succeed for non-existent paths.
		err = rfs.RemoveAll("does-not-exist")
		require.NoError(t, err)
	})

	t.Run("EmptyDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "empty"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.RemoveAll("empty")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(rootDir, "empty"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

// -----------------------------------------------------------------------------
// Rename Tests
// -----------------------------------------------------------------------------

func TestRootFs_Rename(t *testing.T) {
	t.Parallel()

	t.Run("FileSuccess", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "old.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Rename("old.txt", "new.txt")
		require.NoError(t, err)

		// Old file should not exist.
		_, err = os.Stat(filepath.Join(rootDir, "old.txt"))
		require.ErrorIs(t, err, fs.ErrNotExist)

		// New file should exist with same content.
		content, err := os.ReadFile(filepath.Join(rootDir, "new.txt"))
		require.NoError(t, err)
		assert.Equal(t, "content", string(content))
	})

	t.Run("DirectorySuccess", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "olddir"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Rename("olddir", "newdir")
		require.NoError(t, err)

		// Old directory should not exist.
		_, err = os.Stat(filepath.Join(rootDir, "olddir"))
		require.ErrorIs(t, err, fs.ErrNotExist)

		// New directory should exist.
		info, err := os.Stat(filepath.Join(rootDir, "newdir"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("DestinationExistsOverwrites", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "source.txt"), []byte("source"), fileperms.PrivateFile)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "dest.txt"), []byte("dest"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Rename overwrites existing destination (standard rename behavior).
		err = rfs.Rename("source.txt", "dest.txt")
		require.NoError(t, err)

		// Source should not exist.
		_, err = os.Stat(filepath.Join(rootDir, "source.txt"))
		require.ErrorIs(t, err, fs.ErrNotExist)

		// Destination should have source content.
		content, err := os.ReadFile(filepath.Join(rootDir, "dest.txt"))
		require.NoError(t, err)
		assert.Equal(t, "source", string(content))
	})

	t.Run("SourceNotExistsFails", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Rename("nonexistent.txt", "new.txt")
		require.Error(t, err)
	})

	t.Run("PreservesPermissions", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		originalPerm := os.FileMode(0o755)

		err := os.WriteFile(filepath.Join(rootDir, "perms.txt"), []byte("content"), originalPerm)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Rename("perms.txt", "renamed.txt")
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "renamed.txt"))
		require.NoError(t, err)
		assert.Equal(t, originalPerm, info.Mode().Perm())
	})
}

// -----------------------------------------------------------------------------
// Stat Tests
// -----------------------------------------------------------------------------

func TestRootFs_Stat(t *testing.T) {
	t.Parallel()

	t.Run("File", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "file.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		info, err := rfs.Stat("file.txt")
		require.NoError(t, err)
		assert.Equal(t, "file.txt", info.Name())
		assert.False(t, info.IsDir())
		assert.Equal(t, int64(7), info.Size())
	})

	t.Run("Directory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "dir"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		info, err := rfs.Stat("dir")
		require.NoError(t, err)
		assert.Equal(t, "dir", info.Name())
		assert.True(t, info.IsDir())
	})

	t.Run("NonExistent", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		_, err = rfs.Stat("nonexistent")
		require.Error(t, err)
		require.ErrorIs(t, err, fs.ErrNotExist)

		// Also verify os.IsNotExist works (required for afero.Exists compatibility).
		assert.True(t, os.IsNotExist(err), "os.IsNotExist should return true for non-existent file error")
	})
}

// -----------------------------------------------------------------------------
// Chmod Tests
// -----------------------------------------------------------------------------

func TestRootFs_Chmod(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "chmod.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Chmod("chmod.txt", 0o700)
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "chmod.txt"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	})

	t.Run("NonExistent", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Chmod("nonexistent", 0o755)
		require.Error(t, err)
	})
}

// -----------------------------------------------------------------------------
// Ownership and Time Operation Tests
// -----------------------------------------------------------------------------

func TestRootFs_Chown(t *testing.T) {
	t.Parallel()

	t.Run("SameOwner", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "chown.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Chown to current user should succeed (or fail with permission error if different).
		// Use -1 to keep current owner, which should always work.
		err = rfs.Chown("chown.txt", -1, -1)
		require.NoError(t, err)
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		err = rfs.Chown("nonexistent.txt", -1, -1)
		require.Error(t, err)
	})
}

func TestRootFs_Chtimes(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "chtimes.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		targetTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		err = rfs.Chtimes("chtimes.txt", targetTime, targetTime)
		require.NoError(t, err)

		// Verify modification time was changed.
		info, err := os.Stat(filepath.Join(rootDir, "chtimes.txt"))
		require.NoError(t, err)
		assert.Equal(t, targetTime, info.ModTime().UTC())
	})

	t.Run("NonExistentFile", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		now := time.Now()
		err = rfs.Chtimes("nonexistent.txt", now, now)
		require.Error(t, err)
	})
}

// -----------------------------------------------------------------------------
// Symlink Security Tests
// -----------------------------------------------------------------------------

func TestRootFs_SymlinkSecurity(t *testing.T) {
	t.Parallel()

	t.Run("SymlinkWithinRootAllowed", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create target file.
		err := os.WriteFile(filepath.Join(rootDir, "target.txt"), []byte("target content"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Create symlink within root pointing to target.
		err = os.Symlink("target.txt", filepath.Join(rootDir, "link.txt"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Should be able to open the symlink.
		file, err := rfs.Open("link.txt")
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 14)
		_, err = file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "target content", string(buf))
	})

	t.Run("SymlinkEscapingRootBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a file outside the root.
		outsideDir := t.TempDir()

		err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Create symlink that escapes the root.
		err = os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(rootDir, "escape.txt"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Opening the escaping symlink should fail due to RESOLVE_BENEATH.
		_, err = rfs.Open("escape.txt")
		require.Error(t, err)
	})

	t.Run("RelativeSymlinkEscapeBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a symlink using relative path that escapes.
		err := os.Symlink("../../../etc/passwd", filepath.Join(rootDir, "passwd_link"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Should be blocked by kernel.
		_, err = rfs.Open("passwd_link")
		require.Error(t, err)
	})

	t.Run("AbsoluteSymlinkOutsideRootBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create symlink with absolute path outside root.
		err := os.Symlink("/etc/hostname", filepath.Join(rootDir, "hostname_link"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Should be blocked.
		_, err = rfs.Open("hostname_link")
		require.Error(t, err)
	})

	t.Run("NestedSymlinkChainWithinRootAllowed", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create target.
		err := os.WriteFile(filepath.Join(rootDir, "real.txt"), []byte("real"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Create chain: link1 -> link2 -> real.txt
		err = os.Symlink("real.txt", filepath.Join(rootDir, "link2.txt"))
		require.NoError(t, err)

		err = os.Symlink("link2.txt", filepath.Join(rootDir, "link1.txt"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Chain within root should work.
		file, err := rfs.Open("link1.txt")
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 4)
		_, err = file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "real", string(buf))
	})

	t.Run("SymlinkToDirectoryWithinRootAllowed", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create target directory with file.
		err := os.Mkdir(filepath.Join(rootDir, "realdir"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "realdir/file.txt"), []byte("in dir"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Create symlink to directory.
		err = os.Symlink("realdir", filepath.Join(rootDir, "dirlink"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Should be able to access file through directory symlink.
		file, err := rfs.Open("dirlink/file.txt")
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 6)
		_, err = file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "in dir", string(buf))
	})

	t.Run("CreateThroughEscapingSymlinkBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		outsideDir := t.TempDir()

		// Create symlink to outside directory.
		err := os.Symlink(outsideDir, filepath.Join(rootDir, "outside_link"))
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Attempting to create a file through the escaping symlink should fail.
		_, err = rfs.Create("outside_link/malicious.txt")
		require.Error(t, err)

		// Verify nothing was created outside.
		_, err = os.Stat(filepath.Join(outsideDir, "malicious.txt"))
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

// -----------------------------------------------------------------------------
// Path Resolution Edge Case Tests
// -----------------------------------------------------------------------------

func TestRootFs_PathResolutionEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("EmptyPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Empty path should resolve to root directory.
		info, err := rfs.Stat("")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("DotPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// "." should refer to root directory.
		info, err := rfs.Stat(".")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("DotSlashPath", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "dotslash.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// "./file" should work.
		info, err := rfs.Stat("./dotslash.txt")
		require.NoError(t, err)
		assert.Equal(t, "dotslash.txt", info.Name())
	})

	t.Run("DoubleSlashes", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.MkdirAll(filepath.Join(rootDir, "a/b"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "a/b/file.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Double slashes should be normalized.
		info, err := rfs.Stat("a//b//file.txt")
		require.NoError(t, err)
		assert.Equal(t, "file.txt", info.Name())
	})

	t.Run("TrailingSlash", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "trailing"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Trailing slash should be handled.
		info, err := rfs.Stat("trailing/")
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("DotDotEscapeBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Attempting to escape with ".." should fail.
		_, err = rfs.Stat("../")
		require.Error(t, err)

		_, err = rfs.Open("../../etc/passwd")
		require.Error(t, err)
	})

	t.Run("DotDotWithinRootAllowed", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.MkdirAll(filepath.Join(rootDir, "dir1/subdir"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.Mkdir(filepath.Join(rootDir, "dir2"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "dir2/target.txt"), []byte("found"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// ".." that stays within root should work.
		file, err := rfs.Open("dir1/subdir/../../dir2/target.txt")
		require.NoError(t, err)

		defer file.Close()

		buf := make([]byte, 5)
		_, err = file.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "found", string(buf))
	})

	t.Run("LeadingSlashStripped", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "leading.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Leading slash should be stripped (treated as relative to root).
		info, err := rfs.Stat("/leading.txt")
		require.NoError(t, err)
		assert.Equal(t, "leading.txt", info.Name())
	})

	t.Run("MultipleLeadingSlashesNormalized", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.MkdirAll(filepath.Join(rootDir, "foo/bar"), fileperms.PrivateDir)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(rootDir, "foo/bar/file.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Multiple leading slashes should be normalized (filepath.Clean handles this).
		info, err := rfs.Stat("///foo/bar/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "file.txt", info.Name())
	})

	t.Run("AbsolutePathOutsideRootBlocked", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Absolute path outside root is blocked by os.Root kernel-level checks.
		// The path "/etc/passwd" becomes "etc/passwd" relative to root, which doesn't exist.
		_, err = rfs.Stat("/etc/passwd")
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("CleanedPathWithDots", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.WriteFile(filepath.Join(rootDir, "clean.txt"), []byte("cleaned"), fileperms.PrivateFile)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Path with "./" components should be cleaned.
		info, err := rfs.Stat("./././clean.txt")
		require.NoError(t, err)
		assert.Equal(t, "clean.txt", info.Name())
	})
}

// -----------------------------------------------------------------------------
// Additional Coverage Tests
// -----------------------------------------------------------------------------

func TestRootFs_MkdirAllErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("PathWithLeadingSlash", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Path with leading slash - the empty part should be skipped.
		err = rfs.MkdirAll("/a/b/c", fileperms.PrivateDir)
		require.NoError(t, err)

		// Verify the directories were created.
		info, err := os.Stat(filepath.Join(rootDir, "a/b/c"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("PermissionDeniedDuringMkdir", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a directory with no write permission.
		restrictedDir := filepath.Join(rootDir, "restricted")

		err := os.Mkdir(restrictedDir, 0o555)
		require.NoError(t, err)

		// Ensure cleanup even if test fails.
		defer func() {
			_ = os.Chmod(restrictedDir, 0o755)
		}()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Try to create a subdirectory - should fail due to permissions.
		err = rfs.MkdirAll("restricted/subdir/deep", fileperms.PrivateDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating directory")
	})
}

func TestRootFs_OpenFileErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("NestedPathWithoutParentDirsFails", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Try to create file in nested path that doesn't exist - should fail.
		_, err = rfs.OpenFile("nonexistent/subdir/file.txt", os.O_CREATE|os.O_WRONLY, fileperms.PrivateFile)
		require.Error(t, err)
	})

	t.Run("CreateInRootDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Create file directly in root - dir is "." so MkdirAll should be skipped.
		file, err := rfs.OpenFile("root-file.txt", os.O_CREATE|os.O_WRONLY, fileperms.PrivateFile)
		require.NoError(t, err)

		err = file.Close()
		require.NoError(t, err)

		// Verify file exists.
		_, err = os.Stat(filepath.Join(rootDir, "root-file.txt"))
		require.NoError(t, err)
	})
}

func TestRootFs_RemoveAllErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("RemoveFileWithPermissionError", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a directory with a file, then remove write permission from parent.
		subdir := filepath.Join(rootDir, "protected")

		err := os.Mkdir(subdir, fileperms.PrivateDir)
		require.NoError(t, err)

		filePath := filepath.Join(subdir, "file.txt")

		err = os.WriteFile(filePath, []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Remove write permission from the directory containing the file.
		err = os.Chmod(subdir, 0o555)
		require.NoError(t, err)

		// Ensure cleanup.
		defer func() {
			_ = os.Chmod(subdir, 0o755)
		}()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// RemoveAll should fail because we can't remove files from protected dir.
		err = rfs.RemoveAll("protected/file.txt")
		require.Error(t, err)
	})

	t.Run("RemoveNonDirWithError", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create a file.
		filePath := filepath.Join(rootDir, "testfile.txt")

		err := os.WriteFile(filePath, []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Make the file immutable (on Linux, this prevents deletion).
		// We'll use a different approach - create a protected parent.
		protectedDir := filepath.Join(rootDir, "protected")

		err = os.Mkdir(protectedDir, fileperms.PrivateDir)
		require.NoError(t, err)

		protectedFile := filepath.Join(protectedDir, "immutable.txt")

		err = os.WriteFile(protectedFile, []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Remove write permission so file can't be deleted.
		err = os.Chmod(protectedDir, 0o555)
		require.NoError(t, err)

		defer func() {
			_ = os.Chmod(protectedDir, 0o755)
		}()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Try to remove the protected file - should fail.
		err = rfs.RemoveAll("protected/immutable.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "removing")
	})
}

func TestRootFs_RenameErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("WriteFailsDuringRename", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		// Create source file.
		err := os.WriteFile(filepath.Join(rootDir, "source.txt"), []byte("content"), fileperms.PrivateFile)
		require.NoError(t, err)

		// Create a protected directory for the destination.
		protectedDir := filepath.Join(rootDir, "protected")

		err = os.Mkdir(protectedDir, 0o555)
		require.NoError(t, err)

		defer func() {
			_ = os.Chmod(protectedDir, 0o755)
		}()

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Try to rename to a path where we can't write - should fail with permission denied.
		err = rfs.Rename("source.txt", "protected/dest.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
	})
}

func TestRootFs_ChmodErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("ChmodOnDirectory", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()

		err := os.Mkdir(filepath.Join(rootDir, "testdir"), fileperms.PrivateDir)
		require.NoError(t, err)

		rfs, err := rootfs.New(rootDir)
		require.NoError(t, err)

		defer rfs.Close()

		// Chmod on directory should work.
		err = rfs.Chmod("testdir", 0o755)
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(rootDir, "testdir"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	})
}

func TestRootFs_CreateNestedPathFails(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()

	rfs, err := rootfs.New(rootDir)
	require.NoError(t, err)

	defer rfs.Close()

	// Create with nested path should fail (Create doesn't auto-create dirs).
	_, err = rfs.Create("nonexistent/subdir/file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating file")
}

func TestRootFs_OpenFileOpenError(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()

	rfs, err := rootfs.New(rootDir)
	require.NoError(t, err)

	defer rfs.Close()

	// Try to open non-existent file without O_CREATE.
	_, err = rfs.OpenFile("nonexistent.txt", os.O_RDONLY, 0)
	require.Error(t, err)
	// Error should be os.IsNotExist compatible for afero utility functions.
	assert.True(t, os.IsNotExist(err), "os.IsNotExist should return true for non-existent file error")
}
