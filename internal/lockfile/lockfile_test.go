// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectDir = "/project"
	testCommitHash = "aaaa"
)

func TestNew(t *testing.T) {
	lock := lockfile.New()
	assert.Equal(t, 1, lock.Version)
	assert.Empty(t, lock.UpstreamCommit)
	assert.Empty(t, lock.ImportCommit)
	assert.Zero(t, lock.ManualBump)
	assert.Empty(t, lock.InputFingerprint)
}

func TestLockPath(t *testing.T) {
	path := lockfile.LockPath("/project", "curl")
	assert.Equal(t, filepath.Join("/project", "locks", "curl.lock"), path)
}

func TestLockPathDistantProjectDir(t *testing.T) {
	// Simulates -C /some/distant/repo being passed to azldev.
	distantDir := "/some/distant/repo"

	path := lockfile.LockPath(distantDir, "curl")
	assert.Equal(t, filepath.Join(distantDir, "locks", "curl.lock"), path)

	// Save and load from the distant path to verify full round-trip.
	memFS := afero.NewMemMapFs()

	lock := lockfile.New()
	lock.UpstreamCommit = "distant-commit"

	require.NoError(t, lock.Save(memFS, path))

	loaded, err := lockfile.Load(memFS, path)
	require.NoError(t, err)
	assert.Equal(t, "distant-commit", loaded.UpstreamCommit)

	// Verify the file actually ended up under the distant dir, not cwd.
	exists, err := lockfile.Exists(memFS, filepath.Join(distantDir, "locks", "curl.lock"))
	require.NoError(t, err)
	assert.True(t, exists)

	// And NOT under the default project dir.
	exists, err = lockfile.Exists(memFS, filepath.Join(testProjectDir, "locks", "curl.lock"))
	require.NoError(t, err)
	assert.False(t, exists, "lock file should not appear under default project dir")
}

func TestSaveAndLoad(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "curl")

	original := lockfile.New()
	original.UpstreamCommit = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	original.ImportCommit = "0000111122223333444455556666777788889999"
	original.ManualBump = 2
	original.InputFingerprint = "sha256:abcdef1234567890"

	require.NoError(t, original.Save(memFS, lockPath))

	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)

	assert.Equal(t, 1, loaded.Version)
	assert.Equal(t, "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", loaded.UpstreamCommit)
	assert.Equal(t, "0000111122223333444455556666777788889999", loaded.ImportCommit)
	assert.Equal(t, 2, loaded.ManualBump)
	assert.Equal(t, "sha256:abcdef1234567890", loaded.InputFingerprint)
}

func TestSaveCreatesDirectory(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "newpkg")

	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, lock.Save(memFS, lockPath))

	// Verify the file was created.
	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)
	assert.Equal(t, testCommitHash, loaded.UpstreamCommit)
}

func TestLoadUnsupportedVersion(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "bad")

	content := "version = 99\n"

	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(lockPath)))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte(content), fileperms.PublicFile))

	_, err := lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "unsupported lock file version")
}

func TestLoadMissingFile(t *testing.T) {
	fs := afero.NewMemMapFs()

	_, err := lockfile.Load(fs, "/nonexistent/locks/curl.lock")
	assert.Error(t, err)
}

func TestLoadInvalidTOML(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "bad")

	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(lockPath)))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte("not valid toml {{{"), fileperms.PublicFile))

	_, err := lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "parsing lock file")
}

func TestSaveContainsVersion(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "test")

	lock := lockfile.New()
	require.NoError(t, lock.Save(memFS, lockPath))

	data, err := fileutils.ReadFile(memFS, lockPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "version = 1")
}

func TestLocalComponentRoundTrip(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "local-pkg")

	// Local component: no upstream commit, no import commit.
	original := lockfile.New()
	original.InputFingerprint = "sha256:localfp"

	require.NoError(t, original.Save(memFS, lockPath))

	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)

	assert.Empty(t, loaded.UpstreamCommit)
	assert.Empty(t, loaded.ImportCommit)
	assert.Equal(t, "sha256:localfp", loaded.InputFingerprint)
}

func TestExists(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "curl")

	exists, err := lockfile.Exists(memFS, lockPath)
	require.NoError(t, err)
	assert.False(t, exists)

	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, lock.Save(memFS, lockPath))

	exists, err = lockfile.Exists(memFS, lockPath)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestRemove(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "curl")

	lock := lockfile.New()
	require.NoError(t, lock.Save(memFS, lockPath))

	exists, err := lockfile.Exists(memFS, lockPath)
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, lockfile.Remove(memFS, lockPath))

	exists, err = lockfile.Exists(memFS, lockPath)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestMultipleComponentsIndependentFiles(t *testing.T) {
	memFS := afero.NewMemMapFs()

	// Save three different components.
	for _, name := range []string{"curl", "bash", "vim"} {
		lock := lockfile.New()
		lock.UpstreamCommit = name + "-commit"

		require.NoError(t, lock.Save(memFS, lockfile.LockPath(testProjectDir, name)))
	}

	// Load each independently and verify.
	for _, name := range []string{"curl", "bash", "vim"} {
		loaded, err := lockfile.Load(memFS, lockfile.LockPath(testProjectDir, name))
		require.NoError(t, err)
		assert.Equal(t, name+"-commit", loaded.UpstreamCommit)
	}
}

func TestImportCommitPreservedOnRewrite(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "curl")

	// First write: set import-commit and upstream-commit to same value (initial import).
	original := lockfile.New()
	original.ImportCommit = "initial-import-commit"
	original.UpstreamCommit = "initial-import-commit"

	require.NoError(t, original.Save(memFS, lockPath))

	// Simulate what update does: load, update upstream-commit, preserve import-commit.
	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)

	// Import-commit should not be changed — it's write-once.
	assert.Equal(t, "initial-import-commit", loaded.ImportCommit)

	loaded.UpstreamCommit = "newer-upstream-commit"

	require.NoError(t, loaded.Save(memFS, lockPath))

	// Reload and verify import-commit survived while upstream-commit moved.
	reloaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)
	assert.Equal(t, "initial-import-commit", reloaded.ImportCommit, "import-commit should be preserved")
	assert.Equal(t, "newer-upstream-commit", reloaded.UpstreamCommit, "upstream-commit should be updated")
}

func TestResolutionInputHashRoundTrip(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "curl")

	// v2 field: currently stubbed but should survive round-trip.
	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash
	lock.ResolutionInputHash = "sha256:resolution-inputs"

	require.NoError(t, lock.Save(memFS, lockPath))

	loaded, err := lockfile.Load(memFS, lockPath)
	require.NoError(t, err)
	assert.Equal(t, "sha256:resolution-inputs", loaded.ResolutionInputHash)
}

func TestOmitEmptyFields(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath := lockfile.LockPath(testProjectDir, "local-pkg")

	// Local component: only version and fingerprint set.
	lock := lockfile.New()
	lock.InputFingerprint = "sha256:local"

	require.NoError(t, lock.Save(memFS, lockPath))

	data, err := fileutils.ReadFile(memFS, lockPath)
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "import-commit", "empty import-commit should be omitted")
	assert.NotContains(t, content, "upstream-commit", "empty upstream-commit should be omitted")
	assert.NotContains(t, content, "manual-bump", "zero manual-bump should be omitted")
	assert.NotContains(t, content, "resolution-input-hash", "empty resolution-input-hash should be omitted")
	assert.Contains(t, content, "input-fingerprint")
}
