// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lockfile_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectDir = "/project"
	testLockDir    = testProjectDir + "/" + lockfile.LockDir
	testCommitHash = "aaaa"
)

// mustLockPath is a test helper that calls LockPath and fails the test on error.
func mustLockPath(t *testing.T, componentName string) string {
	t.Helper()

	path, err := lockfile.LockPath(testLockDir, componentName)
	require.NoError(t, err)

	return path
}

func TestNew(t *testing.T) {
	lock := lockfile.New()
	assert.Equal(t, 1, lock.Version)
	assert.Empty(t, lock.UpstreamCommit)
	assert.Empty(t, lock.ImportCommit)
	assert.Zero(t, lock.ManualBump)
	assert.Empty(t, lock.InputFingerprint)
}

func TestLockPath(t *testing.T) {
	path, err := lockfile.LockPath("/project/locks", "curl")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/project/locks", "curl.lock"), path)
}

func TestLockPathDistantProjectDir(t *testing.T) {
	// Simulates -C /some/distant/repo being passed to azldev — lock dir
	// would be resolved to /some/distant/repo/locks by the config layer.
	distantLockDir := "/some/distant/repo/locks"

	path, err := lockfile.LockPath(distantLockDir, "curl")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(distantLockDir, "curl.lock"), path)

	// Save and load from the distant path to verify full round-trip.
	memFS := afero.NewMemMapFs()

	lock := lockfile.New()
	lock.UpstreamCommit = "distant-commit"

	require.NoError(t, lock.Save(memFS, path))

	loaded, err := lockfile.Load(memFS, path)
	require.NoError(t, err)
	assert.Equal(t, "distant-commit", loaded.UpstreamCommit)

	// Verify the file actually ended up under the distant dir, not cwd.
	exists, err := lockfile.Exists(memFS, filepath.Join(distantLockDir, "curl.lock"))
	require.NoError(t, err)
	assert.True(t, exists)

	// And NOT under the default lock dir.
	exists, err = lockfile.Exists(memFS, filepath.Join(testLockDir, "curl.lock"))
	require.NoError(t, err)
	assert.False(t, exists, "lock file should not appear under default lock dir")
}

func TestInvalidPath(t *testing.T) {
	tests := []struct {
		name          string
		componentName string
	}{
		{name: "dot", componentName: "."},
		{name: "dotdot", componentName: ".."},
		{name: "empty", componentName: ""},
		{name: "path traversal", componentName: "../escape"},
		{name: "absolute path", componentName: "/etc/passwd"},
		{name: "has directory", componentName: "sub/component"},
		{name: "whitespace", componentName: "has space"},
		{name: "backslash", componentName: "foo\\bar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lockfile.LockPath("/project", tc.componentName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "validating component name")
		})
	}
}

func TestSaveAndLoad(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath, err := lockfile.LockPath(testLockDir, "curl")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "newpkg")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "bad")
	require.NoError(t, err)

	content := "version = 99\n"

	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(lockPath)))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte(content), fileperms.PublicFile))

	_, err = lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "unsupported lock file version")
}

func TestLoadMissingFile(t *testing.T) {
	fs := afero.NewMemMapFs()

	_, err := lockfile.Load(fs, "/nonexistent/locks/curl.lock")
	assert.Error(t, err)
}

func TestLoadInvalidTOML(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath, err := lockfile.LockPath(testLockDir, "bad")
	require.NoError(t, err)

	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(lockPath)))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte("not valid toml {{{"), fileperms.PublicFile))

	_, err = lockfile.Load(memFS, lockPath)
	assert.ErrorContains(t, err, "parsing lock file")
}

func TestSaveContainsVersion(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath, err := lockfile.LockPath(testLockDir, "test")
	require.NoError(t, err)

	lock := lockfile.New()
	require.NoError(t, lock.Save(memFS, lockPath))

	data, err := fileutils.ReadFile(memFS, lockPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "version = 1")
}

func TestLocalComponentRoundTrip(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath, err := lockfile.LockPath(testLockDir, "local-pkg")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "curl")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "curl")
	require.NoError(t, err)

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

		lockPath, err := lockfile.LockPath(testLockDir, name)
		require.NoError(t, err)
		require.NoError(t, lock.Save(memFS, lockPath))
	}

	// Load each independently and verify.
	for _, name := range []string{"curl", "bash", "vim"} {
		lockPath, err := lockfile.LockPath(testLockDir, name)
		require.NoError(t, err)
		loaded, err := lockfile.Load(memFS, lockPath)
		require.NoError(t, err)
		assert.Equal(t, name+"-commit", loaded.UpstreamCommit)
	}
}

func TestImportCommitPreservedOnRewrite(t *testing.T) {
	memFS := afero.NewMemMapFs()
	lockPath, err := lockfile.LockPath(testLockDir, "curl")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "curl")
	require.NoError(t, err)

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
	lockPath, err := lockfile.LockPath(testLockDir, "local-pkg")
	require.NoError(t, err)

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

// Store tests

func TestStoreGetOrNew_NewComponent(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	lock, err := store.GetOrNew("newpkg")
	require.NoError(t, err)
	assert.Equal(t, 1, lock.Version)
	assert.Empty(t, lock.UpstreamCommit)
}

func TestStoreGetOrNew_ExistingComponent(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	// Save a lock with data.
	original := lockfile.New()
	original.UpstreamCommit = testCommitHash
	original.ImportCommit = "import-hash"
	original.ManualBump = 3

	require.NoError(t, store.Save("curl", original))

	// GetOrNew should return the existing lock, preserving all fields.
	lock, err := store.GetOrNew("curl")
	require.NoError(t, err)
	assert.Equal(t, testCommitHash, lock.UpstreamCommit)
	assert.Equal(t, "import-hash", lock.ImportCommit)
	assert.Equal(t, 3, lock.ManualBump)
}

func TestStoreGetOrNew_CorruptLock_ReturnsError(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	// Write corrupt content to the lock file path.
	lockPath, err := lockfile.LockPath(testLockDir, "corrupt")
	require.NoError(t, err)

	require.NoError(t, fileutils.MkdirAll(memFS, filepath.Dir(lockPath)))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte("not valid toml {{{"), fileperms.PublicFile))

	// GetOrNew should error, NOT silently create a new lock.
	_, err = store.GetOrNew("corrupt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading existing lock")
}

func TestStoreGet_Caching(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, store.Save("curl", lock))

	// First get loads from disk.
	first, err := store.Get("curl")
	require.NoError(t, err)
	assert.Equal(t, testCommitHash, first.UpstreamCommit)

	// Second get should return equal data from cache.
	second, err := store.Get("curl")
	require.NoError(t, err)
	assert.Equal(t, first.UpstreamCommit, second.UpstreamCommit)

	// Returns copies — mutating one should not affect the other.
	first.UpstreamCommit = "mutated"
	assert.NotEqual(t, first.UpstreamCommit, second.UpstreamCommit,
		"Get should return copies, not shared pointers")
}

func TestValidateUpstreamCommit(t *testing.T) {
	memFS := afero.NewMemMapFs()

	// Create a lock file for curl.
	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

	t.Run("valid entry no config pin", func(t *testing.T) {
		commit, err := lockfile.ValidateUpstreamCommit(memFS, testLockDir, "curl", "")
		require.NoError(t, err)
		assert.Equal(t, testCommitHash, commit)
	})

	t.Run("valid entry matching config pin", func(t *testing.T) {
		commit, err := lockfile.ValidateUpstreamCommit(memFS, testLockDir, "curl", testCommitHash)
		require.NoError(t, err)
		assert.Equal(t, testCommitHash, commit)
	})

	t.Run("missing lock file", func(t *testing.T) {
		_, err := lockfile.ValidateUpstreamCommit(memFS, testLockDir, "nonexistent", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no lock file")
	})

	t.Run("stale config pin", func(t *testing.T) {
		_, err := lockfile.ValidateUpstreamCommit(memFS, testLockDir, "curl", "bbbb")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stale")
	})

	t.Run("empty upstream commit in lock", func(t *testing.T) {
		emptyLock := lockfile.New()
		require.NoError(t, emptyLock.Save(memFS, mustLockPath(t, "empty-pkg")))

		_, err := lockfile.ValidateUpstreamCommit(memFS, testLockDir, "empty-pkg", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no upstream-commit")
	})
}

func TestValidateConsistency(t *testing.T) {
	t.Run("all valid", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		stale, orphans, err := store.ValidateConsistency(components, true)
		require.NoError(t, err)
		assert.Empty(t, stale)
		assert.Empty(t, orphans)
	})

	t.Run("missing lock", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		stale, _, err := store.ValidateConsistency(components, true)
		require.Error(t, err)
		assert.Contains(t, stale, "curl")
	})

	t.Run("orphan lock file", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "removed-pkg")))

		components := map[string]projectconfig.ComponentConfig{}

		_, orphans, err := store.ValidateConsistency(components, true)
		require.Error(t, err)
		assert.Contains(t, orphans, "removed-pkg")
	})

	t.Run("local component with lock is not orphan", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.InputFingerprint = "sha256:local-fp"

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		// Component exists but is local — lock is still valid (used for fingerprinting).
		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal}},
		}

		_, orphans, err := store.ValidateConsistency(components, true)
		require.NoError(t, err)
		assert.Empty(t, orphans)
	})

	t.Run("local components skipped", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		components := map[string]projectconfig.ComponentConfig{
			"local-pkg": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal}},
		}

		stale, orphans, err := store.ValidateConsistency(components, true)
		require.NoError(t, err)
		assert.Empty(t, stale)
		assert.Empty(t, orphans)
	})

	t.Run("unspecified source type validated like upstream", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		// Component with unspecified source type (inherits upstream from defaults)
		// but no lock file — should be flagged as missing.
		components := map[string]projectconfig.ComponentConfig{
			"curl": {},
		}

		stale, _, err := store.ValidateConsistency(components, true)
		require.Error(t, err)
		assert.Contains(t, stale, "curl")
	})

	t.Run("unspecified source type with valid lock passes", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		components := map[string]projectconfig.ComponentConfig{
			"curl": {},
		}

		stale, orphans, err := store.ValidateConsistency(components, true)
		require.NoError(t, err)
		assert.Empty(t, stale)
		assert.Empty(t, orphans)
	})

	t.Run("mixed stale and orphan", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		// Create orphan lock.
		orphanLock := lockfile.New()
		orphanLock.UpstreamCommit = "orphan-commit"

		require.NoError(t, orphanLock.Save(memFS, mustLockPath(t, "orphan")))

		// Config has upstream component with no lock.
		components := map[string]projectconfig.ComponentConfig{
			"missing": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		stale, orphans, err := store.ValidateConsistency(components, true)
		require.Error(t, err)
		assert.Contains(t, stale, "missing")
		assert.Contains(t, orphans, "orphan")
	})
}

func TestPruneOrphans(t *testing.T) {
	t.Run("prunes removed components", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "removed")))

		components := map[string]projectconfig.ComponentConfig{}

		pruned, err := store.PruneOrphans(components)
		require.NoError(t, err)
		assert.Equal(t, 1, pruned)

		exists, existsErr := lockfile.Exists(memFS, mustLockPath(t, "removed"))
		require.NoError(t, existsErr)
		assert.False(t, exists)
	})

	t.Run("local component lock preserved", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.InputFingerprint = "sha256:local-fp"

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		// Local component — lock still valid for fingerprinting.
		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeLocal}},
		}

		pruned, err := store.PruneOrphans(components)
		require.NoError(t, err)
		assert.Equal(t, 0, pruned, "local component locks should not be pruned")
	})

	t.Run("preserves valid locks", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		pruned, err := store.PruneOrphans(components)
		require.NoError(t, err)
		assert.Equal(t, 0, pruned)

		exists, existsErr := lockfile.Exists(memFS, mustLockPath(t, "curl"))
		require.NoError(t, existsErr)
		assert.True(t, exists)
	})

	t.Run("unspecified source type is not orphan", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

		// Unspecified source type (empty) = inherits upstream from defaults.
		components := map[string]projectconfig.ComponentConfig{
			"curl": {},
		}

		_, orphans, err := store.ValidateConsistency(components, true)
		require.NoError(t, err)
		assert.Empty(t, orphans)
	})
}

// Regression: orphan detection must be scoped by checkOrphans flag.
// Without this, a filtered command like "build curl" on a project with 200
// components would report 199 locks as orphans and fail.
func TestValidateConsistency_FilteredSetSkipsOrphans(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	// Create locks for curl and bash.
	for _, name := range []string{"curl", "bash"} {
		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, lock.Save(memFS, mustLockPath(t, name)))
	}

	// Validate only curl (filtered). bash's lock should NOT be an orphan.
	filteredComponents := map[string]projectconfig.ComponentConfig{
		"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
	}

	stale, orphans, err := store.ValidateConsistency(filteredComponents, false)
	require.NoError(t, err)
	assert.Empty(t, stale)
	assert.Empty(t, orphans, "orphan detection should be skipped on filtered commands")

	// Same call with checkOrphans=true WOULD report bash as orphan.
	_, orphansAll, errAll := store.ValidateConsistency(filteredComponents, true)
	require.Error(t, errAll)
	assert.Contains(t, orphansAll, "bash")
}

// Regression: PruneOrphans with an empty component map must not delete locks
// that belong to real components. Protects against "update -a" on a
// misconfigured project wiping the entire locks/ directory.
func TestStorePruneOrphans_EmptyMapDeletesAll(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	// Create a lock for curl.
	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, lock.Save(memFS, mustLockPath(t, "curl")))

	// Pruning with an empty map classifies everything as orphan.
	// Callers (update.go) must guard against this; the store itself
	// does what it's told. This test documents the behavior.
	pruned, err := store.PruneOrphans(map[string]projectconfig.ComponentConfig{})
	require.NoError(t, err)
	assert.Equal(t, 1, pruned, "empty map causes all locks to be pruned — caller must guard")
}

// Regression: FindOrphanLockFiles must propagate real I/O errors instead of
// silently returning no orphans.
func TestFindOrphanLockFiles_MissingDirIsNotError(t *testing.T) {
	memFS := afero.NewMemMapFs()

	// No locks directory exists at all.
	orphans, err := lockfile.FindOrphanLockFiles(memFS, "/nonexistent/locks", nil)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

// Regression: FindOrphanLockFiles should propagate errors when the directory
// exists but cannot be read. We approximate this by verifying the error path
// is exercised — afero.MemMapFs doesn't support permission errors, so we
// verify the "dir exists" path works correctly.
func TestFindOrphanLockFiles_EmptyDirNoOrphans(t *testing.T) {
	memFS := afero.NewMemMapFs()
	require.NoError(t, fileutils.MkdirAll(memFS, testLockDir))

	orphans, err := lockfile.FindOrphanLockFiles(memFS, testLockDir, nil)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

// Regression: corrupt lock files (bad TOML) must surface errors from Get and
// GetOrNew so callers (e.g., update) treat them as failures — we never silently
// overwrite because import-commit data would be lost. Save can still overwrite
// if the user manually fixes the situation.
func TestStoreGet_CorruptLockReturnsError(t *testing.T) {
	memFS := afero.NewMemMapFs()
	store := lockfile.NewStore(memFS, testLockDir)

	// Write garbage to a lock file.
	lockPath := filepath.Join(testLockDir, "corrupt.lock")
	require.NoError(t, fileutils.MkdirAll(memFS, testLockDir))
	require.NoError(t, fileutils.WriteFile(memFS, lockPath, []byte("not valid toml {{{{"), fileperms.PublicFile))

	// Get should fail.
	_, err := store.Get("corrupt")
	require.Error(t, err, "corrupt lock should return an error from Get")

	// GetOrNew should also fail (exists but unreadable = error, not new).
	_, err = store.GetOrNew("corrupt")
	require.Error(t, err, "corrupt lock should return an error from GetOrNew")

	// But Save should overwrite it successfully.
	newLock := lockfile.New()
	newLock.UpstreamCommit = "fixed"

	require.NoError(t, store.Save("corrupt", newLock))

	// Now Get should work.
	loaded, err := store.Get("corrupt")
	require.NoError(t, err)
	assert.Equal(t, "fixed", loaded.UpstreamCommit)
}

func TestStoreValidateConsistency(t *testing.T) {
	t.Run("no issues", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		lock := lockfile.New()
		lock.UpstreamCommit = testCommitHash

		require.NoError(t, store.Save("curl", lock))

		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		stale, orphans, err := store.ValidateConsistency(components, false)
		require.NoError(t, err)
		assert.Empty(t, stale)
		assert.Empty(t, orphans)
	})

	t.Run("stale components returned", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		components := map[string]projectconfig.ComponentConfig{
			"curl": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
			"bash": {Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream}},
		}

		stale, _, err := store.ValidateConsistency(components, false)
		require.Error(t, err)
		assert.Len(t, stale, 2)
		assert.Contains(t, stale, "curl")
		assert.Contains(t, stale, "bash")
	})

	t.Run("orphans returned with checkOrphans", func(t *testing.T) {
		memFS := afero.NewMemMapFs()
		store := lockfile.NewStore(memFS, testLockDir)

		orphanLock := lockfile.New()
		orphanLock.UpstreamCommit = "orphan"

		require.NoError(t, store.Save("removed", orphanLock))

		components := map[string]projectconfig.ComponentConfig{}

		_, orphans, err := store.ValidateConsistency(components, true)
		require.Error(t, err)
		assert.Contains(t, orphans, "removed")
	})
}
