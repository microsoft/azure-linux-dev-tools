// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testComponentName = "curl"

// setupMockGitWithCounter configures mock git that tracks all git command invocations.
// Returns a pointer to the atomic counter so tests can assert network usage.
//
// The counter treats every git invocation (clone, rev-list, rev-parse, etc.)
// as a single signal because the Fedora source provider chains multiple git
// commands per resolution (clone → rev-list/rev-parse). Tests reset the
// counter between updates and assert zero vs. positive — not exact counts —
// so the signal is robust against internal provider changes that add or
// reorder git calls.
func setupMockGitWithCounter(env *testutils.TestEnv, commitHash string) *atomic.Int32 {
	var gitCalls atomic.Int32

	env.CmdFactory.RegisterCommandInSearchPath("git")

	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		gitCalls.Add(1)

		args := cmd.Args

		for _, arg := range args {
			if arg == "clone" {
				destDir := args[len(args)-1]
				_ = fileutils.MkdirAll(env.TestFS, destDir)

				return nil
			}

			if arg == "checkout" {
				return nil
			}
		}

		return nil
	}

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		gitCalls.Add(1)

		if strings.Contains(strings.Join(cmd.Args, " "), "rev-parse") {
			return commitHash, nil
		}

		if strings.Contains(strings.Join(cmd.Args, " "), "rev-list") {
			return commitHash, nil
		}

		return "", nil
	}

	return &gitCalls
}

// allComponentsFilter returns a filter for all components with lock validation skipped.
func allComponentsFilter() *componentcmds.UpdateComponentOptions {
	return &componentcmds.UpdateComponentOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}
}

// initialUpdate runs the first update to establish lock files. Returns the
// initial fingerprint and resolution hash for the named component.
func initialUpdate(
	t *testing.T, env *testutils.TestEnv,
) (fingerprint, resHash string) {
	t.Helper()

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)
	require.NotEmpty(t, results, "initial update should produce results")

	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get(testComponentName)
	require.NoError(t, lockErr)
	require.NotEmpty(t, lock.InputFingerprint, "initial lock should have fingerprint")
	require.NotEmpty(t, lock.ResolutionInputHash, "initial lock should have resolution hash")

	return lock.InputFingerprint, lock.ResolutionInputHash
}

// TestFreshness_NothingChanged_SkipsNetwork verifies Case 1: when nothing
// changed between updates, no git clones happen and the lock is untouched.
func TestFreshness_NothingChanged_SkipsNetwork(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	fpBefore, resHashBefore := initialUpdate(t, env)

	// Reset clone counter after initial update.
	initialClones := gitCalls.Load()
	require.Positive(t, initialClones, "initial update must do at least one clone")

	gitCalls.Store(0)

	// Second update — nothing changed.
	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	// No git clones should have happened.
	assert.Equal(t, int32(0), gitCalls.Load(),
		"no git calls expected when nothing changed")

	// No results in display (up-to-date filtered out).
	for _, r := range results {
		if r.Component == testComponentName {
			assert.False(t, r.Changed, "curl should not be changed")
			assert.False(t, r.Skipped, "curl should not be skipped")
		}
	}

	// Lock file should be identical.
	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get("curl")
	require.NoError(t, lockErr)
	assert.Equal(t, fpBefore, lock.InputFingerprint, "fingerprint should be unchanged")
	assert.Equal(t, resHashBefore, lock.ResolutionInputHash, "resolution hash should be unchanged")
	assert.Equal(t, commit, lock.UpstreamCommit, "commit should be unchanged")
}

// setDistroSnapshot updates the test distro version's default-component-config
// snapshot. This mirrors how real projects set snapshots in distro.toml.
func setDistroSnapshot(env *testutils.TestEnv, snapshot string) {
	distro := env.Config.Distros["test-distro"]

	version := distro.Versions["1.0"]
	version.DefaultComponentConfig.Spec.UpstreamDistro.Snapshot = snapshot
	distro.Versions["1.0"] = version

	env.Config.Distros["test-distro"] = distro
}

// TestFreshness_SnapshotChanged_SameCommit_UsesNetwork verifies Case 2: when
// the snapshot changes but the resolved commit is the same, the system
// re-resolves (network), updates the resolution hash, but the fingerprint
// stays the same (commit unchanged → build output unchanged).
func TestFreshness_SnapshotChanged_SameCommit_UsesNetwork(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	fpBefore, resHashBefore := initialUpdate(t, env)

	gitCalls.Store(0)

	// Bump snapshot at the distro level — resolution inputs change.
	setDistroSnapshot(env, "2026-06-15T00:00:00Z")

	// Second update — should re-resolve (network).
	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	assert.Positive(t, gitCalls.Load(),
		"git calls expected when snapshot changed")

	// Fingerprint should be UNCHANGED (same commit, snapshot excluded from fingerprint).
	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get("curl")
	require.NoError(t, lockErr)
	assert.Equal(t, fpBefore, lock.InputFingerprint,
		"fingerprint should be unchanged (same commit, snapshot excluded)")
	assert.Equal(t, commit, lock.UpstreamCommit,
		"commit should be the same (mock returns same hash)")

	// Resolution hash should be CHANGED (snapshot is a resolution input).
	assert.NotEqual(t, resHashBefore, lock.ResolutionInputHash,
		"resolution hash should change after snapshot bump")

	// Component should NOT be in display results as "changed" (fingerprint same).
	for _, r := range results {
		if r.Component == testComponentName {
			assert.False(t, r.Changed,
				"curl should not be marked changed (fingerprint unchanged)")
		}
	}
}

// TestFreshness_OverlayChanged_SkipsNetwork verifies Case 3: when only build
// inputs change (overlay edit) but resolution inputs are unchanged, the system
// reuses the locked commit (no network) and updates the fingerprint.
func TestFreshness_OverlayChanged_SkipsNetwork(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	fpBefore, resHashBefore := initialUpdate(t, env)

	gitCalls.Store(0)

	// Add a build option — changes fingerprint but not resolution inputs.
	modifiedConfig := env.Config.Components["curl"]
	modifiedConfig.Build.With = []string{"ssl"}
	env.Config.Components["curl"] = modifiedConfig

	// Second update — should NOT re-resolve.
	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	assert.Equal(t, int32(0), gitCalls.Load(),
		"no git calls expected when only build inputs changed")

	// Fingerprint should be CHANGED (build input added).
	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get("curl")
	require.NoError(t, lockErr)
	assert.NotEqual(t, fpBefore, lock.InputFingerprint,
		"fingerprint should change after build input change")
	assert.Equal(t, commit, lock.UpstreamCommit,
		"commit should be unchanged (reused from lock)")

	// Resolution hash should be UNCHANGED.
	assert.Equal(t, resHashBefore, lock.ResolutionInputHash,
		"resolution hash should be unchanged (no resolution input changed)")

	// Component SHOULD be in display results as "changed".
	foundChanged := false

	for _, r := range results {
		if r.Component == testComponentName && r.Changed {
			foundChanged = true
		}
	}

	assert.True(t, foundChanged,
		"curl should appear as changed in results (fingerprint differs)")
}

// TestFreshness_SnapshotChanged_DifferentCommit verifies Case 4: when
// snapshot changes AND the resolved commit is different, both the
// resolution hash and fingerprint should change.
func TestFreshness_SnapshotChanged_DifferentCommit(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const (
		initialCommit = "aabbccdd11223344"
		newCommit     = "eeff00112233aabb"
	)

	gitCalls := setupMockGitWithCounter(env, initialCommit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	fpBefore, resHashBefore := initialUpdate(t, env)

	gitCalls.Store(0)

	// Bump snapshot AND change the commit the mock returns.
	gitCalls = setupMockGitWithCounter(env, newCommit)
	setDistroSnapshot(env, "2026-06-15T00:00:00Z")

	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	assert.Positive(t, gitCalls.Load(),
		"git calls expected when snapshot changed")

	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get("curl")
	require.NoError(t, lockErr)

	// Everything should change.
	assert.Equal(t, newCommit, lock.UpstreamCommit,
		"commit should be the new one")
	assert.NotEqual(t, fpBefore, lock.InputFingerprint,
		"fingerprint should change (different commit)")
	assert.NotEqual(t, resHashBefore, lock.ResolutionInputHash,
		"resolution hash should change (snapshot bumped)")

	// Component should be marked changed.
	foundChanged := false

	for _, r := range results {
		if r.Component == testComponentName && r.Changed {
			foundChanged = true
		}
	}

	assert.True(t, foundChanged,
		"curl should appear as changed (new commit → new fingerprint)")
}

// addLocalComponent registers a local component with a spec file on the test FS.
func addLocalComponent(env *testutils.TestEnv, name string) {
	specPath := "/project/specs/" + name + "/" + name + ".spec"

	_ = fileutils.WriteFile(env.TestFS, specPath, []byte("Name: "+name+"\n"), 0o644)

	env.Config.Components[name] = projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
	}
}

// TestFreshness_LocalComponent_ReUpdateSucceeds verifies that local components
// work correctly with the freshness optimization. Local components have no
// upstream commit — their source identity is a content hash of the spec dir.
// The freshness path must not try to reuse a locked commit for these.
// Regression: local components can inherit a snapshot from the distro's
// default-component-config, which bypasses the HEAD-tracking check and
// causes the freshness optimizer to try reusing an empty UpstreamCommit
// as source identity.
func TestFreshness_LocalComponent_ReUpdateSucceeds(t *testing.T) {
	env := testutils.NewTestEnv(t)

	setupMockGitWithCounter(env, "doesnt-matter")
	addLocalComponent(env, "local-pkg")

	// Set a snapshot — this is the key trigger. Local components inherit
	// the snapshot from the distro's default-component-config. Without this,
	// the HEAD-tracking check would fire and always re-resolve, masking the bug.
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	// Initial update — creates the lock file.
	results, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)
	require.NotEmpty(t, results)

	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get("local-pkg")
	require.NoError(t, lockErr)
	assert.NotEmpty(t, lock.InputFingerprint, "initial lock should have fingerprint")
	assert.Empty(t, lock.UpstreamCommit, "local component has no upstream commit")

	// Second update — should succeed, not error with "source identity required".
	results2, err2 := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err2, "re-update of local component must not fail")

	// Local component should not appear as changed (nothing changed).
	for _, r := range results2 {
		if r.Component == "local-pkg" {
			assert.False(t, r.Changed, "unchanged local component should not be marked changed")
		}
	}
}

// TestFreshness_LocalComponent_LegacyLock_ReUpdateSucceeds reproduces the exact
// failure path: a local component with an inherited snapshot and a legacy lock
// file (has InputFingerprint but no ResolutionInputHash). The legacy migration
// path sets ResolutionStale=false to trigger Case 2 (reuse commit, backfill
// hash), but Case 2 fails for local components because UpstreamCommit is empty
// and ComputeIdentity requires a non-empty source identity.
func TestFreshness_LocalComponent_LegacyLock_ReUpdateSucceeds(t *testing.T) {
	env := testutils.NewTestEnv(t)

	setupMockGitWithCounter(env, "doesnt-matter")
	addLocalComponent(env, "local-pkg")
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	// Write a legacy lock directly — has InputFingerprint but no
	// ResolutionInputHash, simulating a lock created before the freshness
	// feature existed. Must use WriteLock before any UpdateComponents call
	// so the store cache isn't contaminated with a full lock.
	legacyLock := lockfile.New()
	legacyLock.InputFingerprint = "sha256:placeholder-will-differ"
	env.WriteLock(t, "local-pkg", legacyLock)

	// Update with legacy lock — must not error with
	// "source identity is required for component with source type local".
	_, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err,
		"update of local component with legacy lock must not fail "+
			"(was: 'source identity is required for component with source type local')")
}

// TestFreshness_ForceRecalculate_BypassesFreshness verifies that
// --force-recalculate causes all components to be re-resolved even when
// their freshness status says they're current.
func TestFreshness_ForceRecalculate_BypassesFreshness(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	_, _ = initialUpdate(t, env)

	gitCalls.Store(0)

	// Second update with --force-recalculate — should re-resolve even
	// though nothing changed.
	opts := allComponentsFilter()
	opts.ForceRecalculate = true

	_, err := componentcmds.UpdateComponents(env.Env, opts)
	require.NoError(t, err)

	assert.Positive(t, gitCalls.Load(),
		"--force-recalculate must bypass freshness and trigger re-resolution")
}

// TestFreshness_HeadTracking_AlwaysReResolves verifies that upstream
// components with no snapshot and no pin (HEAD-tracking) always re-resolve.
// The freshness optimization assumes "same inputs → same commit," which
// doesn't hold for HEAD-tracking components since upstream pushes change
// the result without any config edit.
func TestFreshness_HeadTracking_AlwaysReResolves(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)

	// Explicitly clear snapshot — HEAD-tracking means no snapshot, no pin.
	setDistroSnapshot(env, "")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	// Initial update.
	_, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	gitCalls.Store(0)

	// Second update — nothing changed in config, but HEAD-tracking
	// components must always re-resolve.
	_, err = componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	assert.Positive(t, gitCalls.Load(),
		"HEAD-tracking component (no snapshot/pin) must always re-resolve")
}

// TestFreshness_UpstreamLegacyLock_ReResolves verifies that upstream
// components with a legacy lock file (InputFingerprint set but no
// ResolutionInputHash) trigger a full re-resolution to backfill the hash.
// Without this, legacy locks would be permanently treated as "current"
// because the empty-hash comparison was skipped.
func TestFreshness_UpstreamLegacyLock_ReResolves(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "aabbccdd11223344"

	gitCalls := setupMockGitWithCounter(env, commit)
	addUpstreamComponent(env, testComponentName)
	setDistroSnapshot(env, "2025-01-01T00:00:00Z")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	// Write a legacy lock — has fingerprint + commit but no resolution hash.
	legacyLock := lockfile.New()
	legacyLock.InputFingerprint = "sha256:legacy-placeholder"
	legacyLock.UpstreamCommit = commit
	env.WriteLock(t, testComponentName, legacyLock)

	gitCalls.Store(0)

	// Update with legacy lock — must re-resolve to backfill resolution hash.
	_, err := componentcmds.UpdateComponents(env.Env, allComponentsFilter())
	require.NoError(t, err)

	assert.Positive(t, gitCalls.Load(),
		"upstream component with legacy lock (no resolution hash) must re-resolve")

	// Verify resolution hash was backfilled.
	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, lockErr := store.Get(testComponentName)
	require.NoError(t, lockErr)
	assert.NotEmpty(t, lock.ResolutionInputHash,
		"resolution hash should be populated after re-resolution")
	assert.Equal(t, commit, lock.UpstreamCommit,
		"commit should be unchanged (mock returns same hash)")
}
