// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"os/exec"
	"strings"
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/lockfile"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testLockDir = "/project/locks"

func TestNewUpdateCmd(t *testing.T) {
	cmd := componentcmds.NewUpdateCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "update", cmd.Use)
	assert.NotNil(t, cmd.RunE)
}

func TestNewUpdateCmd_Flags(t *testing.T) {
	cmd := componentcmds.NewUpdateCmd()

	allFlag := cmd.Flags().Lookup("all-components")
	require.NotNil(t, allFlag, "all-components flag should be registered")

	componentFlag := cmd.Flags().Lookup("component")
	require.NotNil(t, componentFlag, "component flag should be registered")
}

func TestUpdateCmd_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewUpdateCmd()
	cmd.SetArgs([]string{"nonexistent-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

// setupMockGit configures the test environment's CmdFactory to simulate git operations.
// git clone: creates a destination directory.
// git rev-parse / git rev-list: returns the provided commit hash.
// All other git commands succeed silently.
func setupMockGit(env *testutils.TestEnv, commitHash string) {
	env.CmdFactory.RegisterCommandInSearchPath("git")

	env.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		args := cmd.Args

		// git clone: create a minimal repo structure in the destination dir.
		for idx, arg := range args {
			if arg == "clone" {
				// Last arg is the destination directory.
				destDir := args[len(args)-1]

				return fileutils.MkdirAll(env.TestFS, destDir)
			}

			// git checkout: no-op.
			if arg == "checkout" {
				return nil
			}

			// git -C <dir> rev-list: return the commit hash (for snapshot resolution).
			if arg == "rev-list" || (idx > 0 && args[idx-1] == "-C" && strings.Contains(strings.Join(args, " "), "rev-list")) {
				return nil
			}
		}

		return nil
	}

	env.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		// git rev-parse HEAD: return the configured commit hash.
		if strings.Contains(strings.Join(cmd.Args, " "), "rev-parse") {
			return commitHash, nil
		}

		// git log / git rev-list --before: return the commit hash.
		if strings.Contains(strings.Join(cmd.Args, " "), "rev-list") {
			return commitHash, nil
		}

		return "", nil
	}
}

// addUpstreamComponent registers an upstream component in the test config.
func addUpstreamComponent(env *testutils.TestEnv, name string) {
	env.Config.Components[name] = projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}
}

// TestUpdateComponents_WritesFingerprint exercises the full UpdateComponents pipeline
// with mocked git, verifying that lock files are created with fingerprints.
func TestUpdateComponents_WritesFingerprint(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "abc123def456"

	setupMockGit(env, commit)
	addUpstreamComponent(env, "curl")

	// Pre-create a lock file so the spec file is writable on memfs.
	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	results, err := componentcmds.UpdateComponents(env.Env, &componentcmds.UpdateComponentOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)
	assert.Equal(t, commit, results[0].UpstreamCommit)

	// Verify lock file was written with fingerprint.
	store := lockfile.NewStore(env.TestFS, testLockDir)

	lock, loadErr := store.Get("curl")
	require.NoError(t, loadErr)
	assert.Equal(t, commit, lock.UpstreamCommit)
	assert.NotEmpty(t, lock.InputFingerprint, "lock should have a computed fingerprint")
	assert.Contains(t, lock.InputFingerprint, "sha256:")
}

// TestUpdateComponents_FingerprintLifecycle exercises the full update → modify → re-update
// flow through the public UpdateComponents API.
func TestUpdateComponents_FingerprintLifecycle(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "abc123def456"

	setupMockGit(env, commit)
	addUpstreamComponent(env, "curl")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	options := &componentcmds.UpdateComponentOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	// Phase 1: Initial update.
	results, err := componentcmds.UpdateComponents(env.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)

	store := lockfile.NewStore(env.TestFS, testLockDir)
	fp1 := mustGetFingerprint(t, store, "curl")

	// Phase 2: Re-run with same commit — idempotent.
	results2, err := componentcmds.UpdateComponents(env.Env, options)
	require.NoError(t, err)
	assert.Empty(t, results2, "idempotent re-run should produce no display results")

	// Recreate store to bypass read cache.
	store = lockfile.NewStore(env.TestFS, testLockDir)
	fp2 := mustGetFingerprint(t, store, "curl")
	assert.Equal(t, fp1, fp2, "fingerprint should be stable on re-run")

	// Phase 3: Modify config — fingerprint should change.
	// Build.With survives inheritance (mergo preserves non-empty fields from
	// later layers), so adding a build option changes the config hash.
	modifiedConfig := env.Config.Components["curl"]
	modifiedConfig.Build.With = []string{"ssl"}
	env.Config.Components["curl"] = modifiedConfig

	_, err = componentcmds.UpdateComponents(env.Env, options)
	require.NoError(t, err)

	// Recreate store to bypass read cache.
	store = lockfile.NewStore(env.TestFS, testLockDir)
	fp3 := mustGetFingerprint(t, store, "curl")
	assert.NotEqual(t, fp1, fp3, "config change (Build.With) must produce a different fingerprint")
}

// TestUpdateComponents_MultipleComponents tests update with multiple components.
func TestUpdateComponents_MultipleComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)

	const commit = "multi-commit-hash"

	setupMockGit(env, commit)
	addUpstreamComponent(env, "curl")
	addUpstreamComponent(env, "bash")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	results, err := componentcmds.UpdateComponents(env.Env, &componentcmds.UpdateComponentOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)

	// Should have results for both (may include skipped too).
	var changedNames []string

	for _, r := range results {
		if r.Changed {
			changedNames = append(changedNames, r.Component)
		}
	}

	assert.Contains(t, changedNames, "curl")
	assert.Contains(t, changedNames, "bash")

	// Both should have lock files with fingerprints.
	store := lockfile.NewStore(env.TestFS, testLockDir)

	curlFP := mustGetFingerprint(t, store, "curl")
	bashFP := mustGetFingerprint(t, store, "bash")

	assert.NotEmpty(t, curlFP)
	assert.NotEmpty(t, bashFP)
	// Note: components with identical configs (same source type, no overlays, same commit)
	// will produce the same fingerprint — Name is excluded from the hash by design.
	// This is correct: the fingerprint captures build-affecting inputs only.
}

// TestUpdateComponents_SkipsLocalComponent verifies local components are skipped.
func TestUpdateComponents_SkipsLocalComponent(t *testing.T) {
	env := testutils.NewTestEnv(t)

	setupMockGit(env, "doesnt-matter")

	// Add a local component (no upstream).
	specPath := "/project/specs/local-pkg/local-pkg.spec"
	require.NoError(t, fileutils.WriteFile(env.TestFS, specPath, []byte("Name: local-pkg\n"), fileperms.PrivateFile))

	env.Config.Components["local-pkg"] = projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       specPath,
		},
	}

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testLockDir))

	results, err := componentcmds.UpdateComponents(env.Env, &componentcmds.UpdateComponentOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	})
	require.NoError(t, err)

	// Should be skipped.
	for _, r := range results {
		if r.Component == "local-pkg" {
			assert.True(t, r.Skipped)

			return
		}
	}

	// If local-pkg isn't in results at all, that's also acceptable (filtered out).
}

// mustGetFingerprint reads the fingerprint from a lock file, failing the test on error.
func mustGetFingerprint(t *testing.T, store *lockfile.Store, name string) string {
	t.Helper()

	lock, err := store.Get(name)
	require.NoError(t, err, "loading lock for %q", name)

	return lock.InputFingerprint
}
