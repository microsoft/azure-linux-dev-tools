// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package allowedroots_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils/aferocustom/allowedroots"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

const (
	testWorkDir   = "/tmp/work"
	testOutputDir = "/tmp/output"
	testOtherDir  = "/tmp/other"
)

func TestAllowedRootsFS_PathFiltering(t *testing.T) {
	// Create a memory filesystem for testing
	baseFS := afero.NewMemMapFs()

	// Set up test directories and files
	workDir := testWorkDir
	outputDir := testOutputDir
	otherDir := testOtherDir

	// Create directories and files
	require.NoError(t, baseFS.MkdirAll(workDir, fileperms.PublicDir))
	require.NoError(t, baseFS.MkdirAll(outputDir, fileperms.PublicDir))
	require.NoError(t, baseFS.MkdirAll(otherDir, fileperms.PublicDir))

	require.NoError(t, afero.WriteFile(baseFS, workDir+"/work-file.txt", []byte("work content"), fileperms.PublicFile))
	require.NoError(t, afero.WriteFile(
		baseFS, outputDir+"/output-file.txt", []byte("output content"), fileperms.PublicFile))
	require.NoError(t, afero.WriteFile(baseFS, otherDir+"/other-file.txt", []byte("other content"), fileperms.PublicFile))

	// Create AllowedRootsFS with allowed roots
	allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, []string{workDir, outputDir})

	t.Run("AllowedFilesCanBeRead", func(t *testing.T) {
		// Should be able to read files in allowed directories
		content, err := afero.ReadFile(allowedRootsFS, workDir+"/work-file.txt")
		require.NoError(t, err)
		require.Equal(t, "work content", string(content))

		content, err = afero.ReadFile(allowedRootsFS, outputDir+"/output-file.txt")
		require.NoError(t, err)
		require.Equal(t, "output content", string(content))
	})

	t.Run("DisallowedFilesCannotBeRead", func(t *testing.T) {
		// Should not be able to read files outside allowed directories
		_, err := afero.ReadFile(allowedRootsFS, otherDir+"/other-file.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied, "expected allowedroots.ErrAccessDenied, got: %v", err)
	})

	t.Run("AllowedFilesCanBeCreated", func(t *testing.T) {
		// Should be able to create files in allowed directories
		err := afero.WriteFile(allowedRootsFS, workDir+"/new-work-file.txt", []byte("new work content"), fileperms.PublicFile)
		require.NoError(t, err)

		// Verify it was created
		content, err := afero.ReadFile(allowedRootsFS, workDir+"/new-work-file.txt")
		require.NoError(t, err)
		require.Equal(t, "new work content", string(content))
	})

	t.Run("DisallowedFilesCannotBeCreated", func(t *testing.T) {
		// Should not be able to create files outside allowed directories
		err := afero.WriteFile(
			allowedRootsFS, otherDir+"/new-other-file.txt", []byte("new other content"), fileperms.PublicFile)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied, "expected allowedroots.ErrAccessDenied, got: %v", err)
	})

	t.Run("DisallowedDirectoriesCannotBeAccessed", func(t *testing.T) {
		// Should not be able to stat or open directories outside allowed roots
		_, err := allowedRootsFS.Stat(otherDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied, "expected allowedroots.ErrAccessDenied for Stat, got: %v", err)

		_, err = allowedRootsFS.Open(otherDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied, "expected allowedroots.ErrAccessDenied for Open, got: %v", err)
	})

	t.Run("AllowedDirectoriesCanBeCreated", func(t *testing.T) {
		subdir := workDir + "/subdir"
		err := allowedRootsFS.Mkdir(subdir, fileperms.PublicDir)
		require.NoError(t, err)
		// Should be able to stat the new directory
		info, err := allowedRootsFS.Stat(subdir)
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})

	t.Run("DisallowedDirectoriesCannotBeCreated", func(t *testing.T) {
		disallowed := otherDir + "/subdir"
		err := allowedRootsFS.Mkdir(disallowed, fileperms.PublicDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied, "expected allowedroots.ErrAccessDenied, got: %v", err)
	})
}

func TestAllowedRootsFS_PathNormalization(t *testing.T) {
	baseFS := afero.NewMemMapFs()

	// Create test structure
	workDir := "/tmp/work"
	require.NoError(t, baseFS.MkdirAll(workDir+"/subdir", fileperms.PublicDir))
	require.NoError(t, afero.WriteFile(baseFS, workDir+"/subdir/file.txt", []byte("content"), fileperms.PublicFile))

	// Create AllowedRootsFS with normalized path
	allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, []string{"/tmp/work/"}) // Note trailing slash

	t.Run("TrailingSlashesAreHandled", func(t *testing.T) {
		// Should work with various path formats
		content, err := afero.ReadFile(allowedRootsFS, "/tmp/work/subdir/file.txt")
		require.NoError(t, err)
		require.Equal(t, "content", string(content))
	})

	t.Run("SubdirectoriesAreAllowed", func(t *testing.T) {
		// Should be able to access subdirectories
		content, err := afero.ReadFile(allowedRootsFS, workDir+"/subdir/file.txt")
		require.NoError(t, err)
		require.Equal(t, "content", string(content))
	})
}

func TestAllowedRootsFS_PathAllowedLogic(t *testing.T) {
	tests := []struct {
		name         string
		allowedRoots []string
		testPath     string
		expected     bool
	}{
		{
			name:         "ExactMatch",
			allowedRoots: []string{"/tmp/work"},
			testPath:     "/tmp/work",
			expected:     true,
		},
		{
			name:         "SubdirectoryMatch",
			allowedRoots: []string{"/tmp/work"},
			testPath:     "/tmp/work/subdir/file.txt",
			expected:     true,
		},
		{
			name:         "NotUnderRoot",
			allowedRoots: []string{"/tmp/work"},
			testPath:     "/tmp/other/file.txt",
			expected:     false,
		},
		{
			name:         "SimilarPathButNotUnder",
			allowedRoots: []string{"/tmp/work"},
			testPath:     "/tmp/work-similar/file.txt",
			expected:     false,
		},
		{
			name:         "MultipleRoots",
			allowedRoots: []string{"/tmp/work", "/tmp/output"},
			testPath:     "/tmp/output/result.txt",
			expected:     true,
		},
		{
			name:         "NormalizedPaths",
			allowedRoots: []string{"/tmp/work/"},
			testPath:     "/tmp/work/file.txt",
			expected:     true,
		},
		{
			name:         "DotInPaths",
			allowedRoots: []string{"/tmp/work"},
			testPath:     "/tmp/work/./file.txt",
			expected:     true,
		},
		{
			name:         "EmptyAllowedRoots",
			allowedRoots: []string{},
			testPath:     "/tmp/work/file.txt",
			expected:     false,
		},
		{
			name:         "RootDirectory",
			allowedRoots: []string{"/"},
			testPath:     "/tmp/work/file.txt",
			expected:     true,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			baseFS := afero.NewMemMapFs()

			// Create the directory structure and file if the test expects success
			if testCase.expected {
				require.NoError(t, baseFS.MkdirAll(testCase.testPath, fileperms.PublicDir))

				if !strings.HasSuffix(testCase.testPath, "/") &&
					testCase.testPath != testWorkDir &&
					testCase.testPath != testOutputDir {
					// It's a file path, create the file
					require.NoError(t, afero.WriteFile(baseFS, testCase.testPath, []byte("test content"), fileperms.PublicFile))
				}
			}

			allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, testCase.allowedRoots)

			// Test by trying to stat the path
			_, err := allowedRootsFS.Stat(testCase.testPath)

			if testCase.expected {
				require.NoError(t, err, "Path %q should be allowed for roots %v", testCase.testPath, testCase.allowedRoots)
			} else {
				require.Error(t, err, "Path %q should be denied for roots %v", testCase.testPath, testCase.allowedRoots)
				require.ErrorIs(t, err, allowedroots.ErrAccessDenied,
					"Expected allowedroots.ErrAccessDenied for path %q", testCase.testPath)
			}
		})
	}
}

func TestAllowedRootsFS_FileSystemOperations(t *testing.T) {
	baseFS := afero.NewMemMapFs()
	allowedDir := "/tmp/allowed"
	deniedDir := "/tmp/denied"

	// Setup base filesystem
	require.NoError(t, baseFS.MkdirAll(allowedDir, fileperms.PublicDir))
	require.NoError(t, baseFS.MkdirAll(deniedDir, fileperms.PublicDir))
	require.NoError(t, afero.WriteFile(baseFS, allowedDir+"/test.txt", []byte("test content"), fileperms.PublicFile))
	require.NoError(t, afero.WriteFile(baseFS, deniedDir+"/test.txt", []byte("denied content"), fileperms.PublicFile))

	allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, []string{allowedDir})

	t.Run("Create", func(t *testing.T) {
		// Allowed
		file, err := allowedRootsFS.Create(allowedDir + "/new.txt")
		require.NoError(t, err)
		require.NotNil(t, file)
		file.Close()

		// Denied
		_, err = allowedRootsFS.Create(deniedDir + "/new.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Open", func(t *testing.T) {
		// Allowed
		file, err := allowedRootsFS.Open(allowedDir + "/test.txt")
		require.NoError(t, err)
		require.NotNil(t, file)
		file.Close()

		// Denied
		_, err = allowedRootsFS.Open(deniedDir + "/test.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("OpenFile", func(t *testing.T) {
		// Allowed
		file, err := allowedRootsFS.OpenFile(allowedDir+"/open_file.txt", os.O_CREATE|os.O_WRONLY, fileperms.PublicFile)
		require.NoError(t, err)
		require.NotNil(t, file)
		file.Close()

		// Denied
		_, err = allowedRootsFS.OpenFile(deniedDir+"/open_file.txt", os.O_CREATE|os.O_WRONLY, fileperms.PublicFile)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Stat", func(t *testing.T) {
		// Allowed
		info, err := allowedRootsFS.Stat(allowedDir + "/test.txt")
		require.NoError(t, err)
		require.NotNil(t, info)

		// Denied
		_, err = allowedRootsFS.Stat(deniedDir + "/test.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Remove", func(t *testing.T) {
		// Setup file to remove
		require.NoError(t, afero.WriteFile(baseFS, allowedDir+"/to_remove.txt", []byte("remove me"), fileperms.PublicFile))

		// Allowed
		err := allowedRootsFS.Remove(allowedDir + "/to_remove.txt")
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.Remove(deniedDir + "/test.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("MkdirAll", func(t *testing.T) {
		// Allowed
		err := allowedRootsFS.MkdirAll(allowedDir+"/subdir/deep", fileperms.PublicDir)
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.MkdirAll(deniedDir+"/subdir/deep", fileperms.PublicDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("MkdirAll_DoesNotCreateParentDirectoriesOutsideAllowedRoots", func(t *testing.T) {
		// Test that MkdirAll does not create parent directories outside the allowed roots.
		// This prevents a security issue where calling MkdirAll on a path under an allowed root
		// would create unauthorized parent directories above the allowed root.
		// Create a fresh filesystem and AllowedRootsFS
		securityTestFS := afero.NewMemMapFs()
		securityAllowedRootsFS := allowedroots.NewAllowedRootsFS(securityTestFS, []string{"/a/b/c"})

		// Try to create /a/b/c/d - this should fail because it would require
		// creating /a and /a/b which are outside the allowed root /a/b/c
		err := securityAllowedRootsFS.MkdirAll("/a/b/c/d", fileperms.PublicDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)

		// Verify that no unauthorized directories were created
		exists, _ := afero.Exists(securityTestFS, "/a")
		require.False(t, exists, "Directory /a should not have been created")

		exists, _ = afero.Exists(securityTestFS, "/a/b")
		require.False(t, exists, "Directory /a/b should not have been created")

		exists, _ = afero.Exists(securityTestFS, "/a/b/c")
		require.False(t, exists, "Directory /a/b/c should not have been created")

		exists, _ = afero.Exists(securityTestFS, "/a/b/c/d")
		require.False(t, exists, "Directory /a/b/c/d should not have been created")

		// Test that direct creation of parent directories is still blocked
		err = securityAllowedRootsFS.MkdirAll("/a", fileperms.PublicDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)

		err = securityAllowedRootsFS.MkdirAll("/a/b", fileperms.PublicDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("RemoveAll", func(t *testing.T) {
		// Setup directory to remove
		require.NoError(t, baseFS.MkdirAll(allowedDir+"/to_remove/subdir", fileperms.PublicDir))

		// Allowed
		err := allowedRootsFS.RemoveAll(allowedDir + "/to_remove")
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.RemoveAll(deniedDir)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Rename", func(t *testing.T) {
		// Setup file to rename
		require.NoError(t, afero.WriteFile(baseFS, allowedDir+"/to_rename.txt", []byte("rename me"), fileperms.PublicFile))

		// Allowed (both paths in allowed dir)
		err := allowedRootsFS.Rename(allowedDir+"/to_rename.txt", allowedDir+"/renamed.txt")
		require.NoError(t, err)

		// Denied (source outside allowed)
		err = allowedRootsFS.Rename(deniedDir+"/test.txt", allowedDir+"/moved.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)

		// Denied (destination outside allowed)
		err = allowedRootsFS.Rename(allowedDir+"/renamed.txt", deniedDir+"/moved.txt")
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Chmod", func(t *testing.T) {
		// Allowed
		err := allowedRootsFS.Chmod(allowedDir+"/test.txt", fileperms.PrivateFile)
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.Chmod(deniedDir+"/test.txt", fileperms.PrivateFile)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Chown", func(t *testing.T) {
		// Allowed
		err := allowedRootsFS.Chown(allowedDir+"/test.txt", 1000, 1000)
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.Chown(deniedDir+"/test.txt", 1000, 1000)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})

	t.Run("Chtimes", func(t *testing.T) {
		now := time.Now()

		// Allowed
		err := allowedRootsFS.Chtimes(allowedDir+"/test.txt", now, now)
		require.NoError(t, err)

		// Denied
		err = allowedRootsFS.Chtimes(deniedDir+"/test.txt", now, now)
		require.Error(t, err)
		require.ErrorIs(t, err, allowedroots.ErrAccessDenied)
	})
}

func TestAllowedRootsFS_Name(t *testing.T) {
	baseFS := afero.NewMemMapFs()
	allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, []string{"/tmp"})

	require.Equal(t, "AllowedRootsFS", allowedRootsFS.Name())
}

func TestAllowedRootsFS_AferoCompatibility(t *testing.T) {
	baseFS := afero.NewMemMapFs()
	allowedDir := "/tmp/allowed"

	// Setup base filesystem
	require.NoError(t, baseFS.MkdirAll(allowedDir, fileperms.PublicDir))
	require.NoError(t, afero.WriteFile(baseFS, allowedDir+"/test.txt", []byte("test content"), fileperms.PublicFile))

	allowedRootsFS := allowedroots.NewAllowedRootsFS(baseFS, []string{allowedDir})

	t.Run("WithReadOnlyFs", func(t *testing.T) {
		roFS := afero.NewReadOnlyFs(allowedRootsFS)

		// Should be able to read
		content, err := afero.ReadFile(roFS, allowedDir+"/test.txt")
		require.NoError(t, err)
		require.Equal(t, "test content", string(content))

		// Should not be able to write (read-only)
		err = afero.WriteFile(roFS, allowedDir+"/new.txt", []byte("new"), fileperms.PublicFile)
		require.Error(t, err)
	})

	t.Run("WithCopyOnWriteFs", func(t *testing.T) {
		overlayFS := afero.NewMemMapFs()
		cowFS := afero.NewCopyOnWriteFs(allowedRootsFS, overlayFS)

		// Should be able to read from base
		content, err := afero.ReadFile(cowFS, allowedDir+"/test.txt")
		require.NoError(t, err)
		require.Equal(t, "test content", string(content))

		// Should be able to write (to overlay)
		err = afero.WriteFile(cowFS, allowedDir+"/overlay.txt", []byte("overlay content"), fileperms.PublicFile)
		require.NoError(t, err)

		// Should be able to read from overlay
		content, err = afero.ReadFile(cowFS, allowedDir+"/overlay.txt")
		require.NoError(t, err)
		require.Equal(t, "overlay content", string(content))
	})
}
