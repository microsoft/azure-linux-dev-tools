// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// testLockDir is the lock directory used by TestEnv's project layout.
const (
	testLockDir    = "/project/locks"
	testCommitHash = "abc123"
)

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
		sourceIdentity: commit,
	}
}

// readLock loads a lock file from the store and returns it. Fails the test on error.
//

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
		makeResult("curl", testCommitHash, baseConfig("curl")),
	}

	err := saveComponentLocks(env.Env, store, results)
	require.NoError(t, err)

	lock := readLock(t, store, "curl")
	assert.Equal(t, testCommitHash, lock.UpstreamCommit)
	assert.NotEmpty(t, lock.InputFingerprint, "fingerprint should be computed and stored")
	assert.Contains(t, lock.InputFingerprint, "sha256:", "fingerprint should have sha256 prefix")
}

func TestSaveComponentLocks_DetectsFingerprintChange(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	// First save — establishes baseline fingerprint.
	config1 := baseConfig("curl")
	results1 := []UpdateResult{makeResult("curl", testCommitHash, config1)}

	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint
	require.NotEmpty(t, fp1)

	// Second save — same commit, but config changed (added build option).
	config2 := baseConfig("curl")
	config2.Build.With = []string{"feature_x"}

	results2 := []UpdateResult{
		{
			Component:      "curl",
			UpstreamCommit: testCommitHash,
			Changed:        false, // commit didn't change
			config:         config2,
			sourceIdentity: testCommitHash,
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
	results1 := []UpdateResult{makeResult("curl", testCommitHash, config)}
	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint

	// Second save — identical commit and config. Changed starts as false.
	results2 := []UpdateResult{
		{
			Component:      "curl",
			UpstreamCommit: testCommitHash,
			Changed:        false,
			config:         config,
			sourceIdentity: testCommitHash,
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
	results1 := []UpdateResult{makeResult("curl", testCommitHash, config)}
	require.NoError(t, saveComponentLocks(env.Env, store, results1))

	fp1 := readLock(t, store, "curl").InputFingerprint

	// Manually bump.
	lock, getLockErr := store.Get("curl")
	require.NoError(t, getLockErr)

	lock.ManualBump = 1

	require.NoError(t, store.Save("curl", lock))

	// Re-run save with same commit and config.
	results2 := []UpdateResult{
		{Component: "curl", UpstreamCommit: testCommitHash, Changed: false, config: config, sourceIdentity: testCommitHash},
	}

	require.NoError(t, saveComponentLocks(env.Env, store, results2))

	fp2 := readLock(t, store, "curl").InputFingerprint
	assert.NotEqual(t, fp1, fp2, "ManualBump change should produce different fingerprint")
	assert.True(t, results2[0].Changed, "should be marked changed due to fingerprint diff")
}

// bumpComponents increments ManualBump and recomputes the fingerprint.
// Verify it changes the lock and produces a different fingerprint.
func TestBumpComponents_IncrementsManualBump(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	// Pre-populate a lock with ManualBump = 0.
	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, store.Save("curl", lock))

	config := baseConfig("curl")
	comp := newMockComp(t, "curl", config)

	results, err := bumpComponents(env.Env, store, []components.Component{comp}, &UpdateComponentOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)

	bumped := readLock(t, store, "curl")
	assert.Equal(t, 1, bumped.ManualBump)
	assert.NotEmpty(t, bumped.InputFingerprint)
}

// Bumping twice should increment to 2 and produce a different fingerprint each time.
func TestBumpComponents_SequentialBumps(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	lock := lockfile.New()
	lock.UpstreamCommit = testCommitHash

	require.NoError(t, store.Save("curl", lock))

	config := baseConfig("curl")
	comp := newMockComp(t, "curl", config)
	comps := []components.Component{comp}

	// First bump.
	_, err := bumpComponents(env.Env, store, comps, &UpdateComponentOptions{})
	require.NoError(t, err)

	fp1 := readLock(t, store, "curl").InputFingerprint

	// Second bump.
	_, err = bumpComponents(env.Env, store, comps, &UpdateComponentOptions{})
	require.NoError(t, err)

	lock2 := readLock(t, store, "curl")
	assert.Equal(t, 2, lock2.ManualBump)
	assert.NotEqual(t, fp1, lock2.InputFingerprint, "second bump should produce different fingerprint")
}

// Bumping a local component with no lock file should error (same as upstream).
func TestBumpComponents_ErrorOnLocalNoLock(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	localConfig := &projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/specs/local-pkg/local-pkg.spec",
		},
	}
	comp := newMockComp(t, "local-pkg", localConfig)

	_, err := bumpComponents(env.Env, store, []components.Component{comp}, &UpdateComponentOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot bump")
}

// Bumping a local component with an existing lock should succeed.
func TestBumpComponents_BumpsLocalWithLock(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	specPath := "/specs/local-pkg/local-pkg.spec"
	require.NoError(t, fileutils.WriteFile(env.TestFS, specPath, []byte("Name: local-pkg\n"), fileperms.PrivateFile))

	lock := lockfile.New()
	lock.InputFingerprint = "sha256:old-fingerprint"

	require.NoError(t, store.Save("local-pkg", lock))

	localConfig := &projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
	}
	comp := newMockComp(t, "local-pkg", localConfig)

	results, err := bumpComponents(env.Env, store, []components.Component{comp}, &UpdateComponentOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)

	bumpedLock := readLock(t, store, "local-pkg")
	assert.Equal(t, 1, bumpedLock.ManualBump)
	assert.NotEqual(t, "sha256:old-fingerprint", bumpedLock.InputFingerprint,
		"fingerprint must change after bump")
}

// Bumping a component with no lock file should fail with a clear message.
func TestBumpComponents_ErrorOnNoLockFile(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	config := baseConfig("curl")
	comp := newMockComp(t, "curl", config)

	_, err := bumpComponents(env.Env, store, []components.Component{comp}, &UpdateComponentOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot bump")
}

// Bumping mixed components: upstream and local with locks both succeed.
func TestBumpComponents_MixedComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)
	store := newTestStore(t, env)

	// Create lock for upstream component.
	upstreamLock := lockfile.New()
	upstreamLock.UpstreamCommit = testCommitHash

	require.NoError(t, store.Save("curl", upstreamLock))

	// Create lock and spec for local component.
	specPath := "/specs/local-pkg/local-pkg.spec"
	require.NoError(t, fileutils.WriteFile(env.TestFS, specPath, []byte("Name: local-pkg\n"), fileperms.PrivateFile))

	localLock := lockfile.New()
	localLock.InputFingerprint = "sha256:local-fp"

	require.NoError(t, store.Save("local-pkg", localLock))

	upstreamComp := newMockComp(t, "curl", baseConfig("curl"))
	localComp := newMockComp(t, "local-pkg", &projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
	})

	comps := []components.Component{localComp, upstreamComp}
	results, err := bumpComponents(env.Env, store, comps, &UpdateComponentOptions{})
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Both should be bumped.
	assert.True(t, results[0].Changed)
	assert.True(t, results[1].Changed)

	assert.Equal(t, 1, readLock(t, store, "local-pkg").ManualBump)
	assert.Equal(t, 1, readLock(t, store, "curl").ManualBump)
}

// newMockComp creates a MockComponent with the given name and config using gomock.
func newMockComp(t *testing.T, name string, config *projectconfig.ComponentConfig) components.Component {
	t.Helper()

	ctrl := gomock.NewController(t)
	comp := components_testutils.NewMockComponent(ctrl)
	comp.EXPECT().GetName().AnyTimes().Return(name)
	comp.EXPECT().GetConfig().AnyTimes().Return(config)

	return comp
}
