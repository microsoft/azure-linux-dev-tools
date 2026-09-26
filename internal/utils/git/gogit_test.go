// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoRelPath(t *testing.T) {
	tempDir := t.TempDir()

	realRepoDir := filepath.Join(tempDir, "real", "repo")
	linkedRepoDir := filepath.Join(tempDir, "link", "repo")

	require.NoError(t, os.MkdirAll(filepath.Join(realRepoDir, "locks"), fileperms.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(realRepoDir, "locks", "curl.lock"), nil, fileperms.PrivateFile))
	require.NoError(t, os.Symlink(filepath.Join(tempDir, "real"), filepath.Join(tempDir, "link")))

	testCases := []struct {
		name     string
		repoRoot string
		absPath  string
		expected string
	}{
		{
			name:     "no symlinks",
			repoRoot: realRepoDir,
			absPath:  filepath.Join(realRepoDir, "locks", "curl.lock"),
			expected: filepath.Join("locks", "curl.lock"),
		},
		{
			name:     "path through symlink",
			repoRoot: realRepoDir,
			absPath:  filepath.Join(linkedRepoDir, "locks", "curl.lock"),
			expected: filepath.Join("locks", "curl.lock"),
		},
		{
			name:     "root through symlink",
			repoRoot: linkedRepoDir,
			absPath:  filepath.Join(realRepoDir, "locks", "curl.lock"),
			expected: filepath.Join("locks", "curl.lock"),
		},
		{
			name:     "missing file through symlink",
			repoRoot: realRepoDir,
			absPath:  filepath.Join(linkedRepoDir, "locks", "new", "openssl.lock"),
			expected: filepath.Join("locks", "new", "openssl.lock"),
		},
		{
			name:     "repository root itself",
			repoRoot: realRepoDir,
			absPath:  linkedRepoDir,
			expected: ".",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			relPath, err := git.RepoRelPath(testCase.repoRoot, testCase.absPath)
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, relPath)
		})
	}
}

func TestRepoRelPath_EscapesRoot(t *testing.T) {
	tempDir := t.TempDir()

	repoDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(repoDir, fileperms.PrivateDir))

	require.NoError(t, os.Symlink(tempDir, filepath.Join(repoDir, "outside")))

	for _, absPath := range []string{
		filepath.Join(tempDir, "other", "curl.lock"),
		filepath.Join(repoDir, "outside", "other", "curl.lock"),
	} {
		_, err := git.RepoRelPath(repoDir, absPath)
		require.Error(t, err, absPath)
		assert.Contains(t, err.Error(), "escapes repository root")
	}
}
