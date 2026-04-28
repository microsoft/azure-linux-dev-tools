// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLockDir is the lock directory used by TestEnv's project layout.
const testLockDir = "/project/locks"

// newTestStore creates a lockfile.Store backed by the TestEnv's in-memory filesystem.
func newTestStore(t *testing.T, env *testutils.TestEnv) *lockfile.Store {
	t.Helper()

	return lockfile.NewStore(env.TestFS, testLockDir)
}

// makeResult builds an UpdateResult for testing saveComponentLocks.
//
//nolint:unparam // Helper is generic; tests happen to use "curl" consistently.
func makeResult(name, commit string, config *projectconfig.ComponentConfig) UpdateResult {
	return UpdateResult{
		Component:      name,
		UpstreamCommit: commit,
		Changed:        true,
		config:         config,
	}
}

// readLock loads a lock file from the store and returns it. Fails the test on error.
//
//nolint:unparam // Helper is generic; tests happen to use "curl" consistently.
func readLock(t *testing.T, store *lockfile.Store, name string) *lockfile.ComponentLock {
	t.Helper()

	lock, err := store.Get(name)
	require.NoError(t, err, "reading lock for %q", name)

	return lock
}

// baseConfig returns a minimal upstream component config suitable for fingerprinting.
// The config has source type "upstream" which tells ComputeIdentity to expect a
// SourceIdentity (provided via the lock's UpstreamCommit).
func baseConfig(name string) *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}
}

func TestSaveComponentLocks_ComputesFingerprint(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	results := []UpdateResult{
		makeResult("curl", "abc123", baseConfig("curl")),
	}

	err := saveComponentLocks(env.Env, store, results)
	require.NoError(t, err)

	lock := readLock(t, store, "curl")
	assert.Equal(t, "abc123", lock.UpstreamCommit)
	assert.NotEmpty(t, lock.InputFingerprint, "fingerprint should be computed and stored")
	assert.Contains(t, lock.InputFingerprint, "sha256:", "fingerprint should have sha256 prefix")
}

func TestSaveComponentLocks_DetectsFingerprintChange(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	// First save — establishes baseline fingerprint.
	config1 := baseConfig("curl")
	results1 := []UpdateResult{makeResult("curl", "abc123", config1)}

	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint
	require.NotEmpty(t, fp1)

	// Second save — same commit, but config changed (added build option).
	config2 := baseConfig("curl")
	config2.Build.With = []string{"feature_x"}

	results2 := []UpdateResult{
		{
			Component:      "curl",
			UpstreamCommit: "abc123",
			Changed:        false, // commit didn't change
			config:         config2,
		},
	}

	require.NoError(t, saveComponentLocks(env.Env, store, results2))

	fp2 := readLock(t, store, "curl").InputFingerprint
	assert.NotEqual(t, fp1, fp2, "fingerprint should change when config changes")
	assert.True(t, results2[0].Changed, "Changed should be set to true by fingerprint diff")
}

func TestSaveComponentLocks_SkipsUnchanged(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	config := baseConfig("curl")

	// First save.
	results1 := []UpdateResult{makeResult("curl", "abc123", config)}
	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint

	// Second save — identical commit and config. Changed starts as false.
	results2 := []UpdateResult{
		{
			Component:      "curl",
			UpstreamCommit: "abc123",
			Changed:        false,
			config:         config,
		},
	}

	require.NoError(t, saveComponentLocks(env.Env, store, results2))

	assert.False(t, results2[0].Changed, "should remain unchanged when fingerprint matches")

	// Fingerprint should still be the same.
	fp2 := readLock(t, store, "curl").InputFingerprint
	assert.Equal(t, fp1, fp2)
}

func TestSaveComponentLocks_SkipsErrorAndSkipped(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	results := []UpdateResult{
		{Component: "errored", Error: "resolution failed", config: baseConfig("errored")},
		{Component: "skipped", Skipped: true, SkipReason: "local", config: baseConfig("skipped")},
	}

	err := saveComponentLocks(env.Env, store, results)
	require.NoError(t, err)

	// Neither should have lock files.
	exists1, _ := store.Exists("errored")
	assert.False(t, exists1)

	exists2, _ := store.Exists("skipped")
	assert.False(t, exists2)
}

func TestSaveComponentLocks_ErrorOnNilConfig(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	results := []UpdateResult{
		{Component: "bad", UpstreamCommit: "abc", Changed: true, config: nil},
	}

	err := saveComponentLocks(env.Env, store, results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no resolved config")
}

func TestSaveComponentLocks_PreservesManualBump(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	// Pre-populate lock with ManualBump = 3.
	existingLock := lockfile.New()
	existingLock.UpstreamCommit = "old-commit"
	existingLock.ManualBump = 3

	require.NoError(t, store.Save("curl", existingLock))

	// Update with new commit.
	results := []UpdateResult{makeResult("curl", "new-commit", baseConfig("curl"))}

	require.NoError(t, saveComponentLocks(env.Env, store, results))

	lock := readLock(t, store, "curl")
	assert.Equal(t, "new-commit", lock.UpstreamCommit)
	assert.Equal(t, 3, lock.ManualBump, "ManualBump should be preserved from existing lock")
	assert.NotEmpty(t, lock.InputFingerprint)
}

func TestSaveComponentLocks_ManualBumpAffectsFingerprint(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)
	config := baseConfig("curl")

	// Save with ManualBump = 0.
	results1 := []UpdateResult{makeResult("curl", "abc123", config)}
	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint

	// Manually bump.
	lock, getLockErr := store.Get("curl")
	require.NoError(t, getLockErr)

	lock.ManualBump = 1

	require.NoError(t, store.Save("curl", lock))

	// Re-run save with same commit and config.
	results2 := []UpdateResult{
		{Component: "curl", UpstreamCommit: "abc123", Changed: false, config: config},
	}

	require.NoError(t, saveComponentLocks(env.Env, store, results2))

	fp2 := readLock(t, store, "curl").InputFingerprint
	assert.NotEqual(t, fp1, fp2, "ManualBump change should produce different fingerprint")
	assert.True(t, results2[0].Changed, "should be marked changed due to fingerprint diff")
}
