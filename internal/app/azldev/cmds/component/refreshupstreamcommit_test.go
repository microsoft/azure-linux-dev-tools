// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/upstreamcommit"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUpstreamCommitsDir = "/project/base/upstream-commits"

func TestNewRefreshUpstreamCommitCmd(t *testing.T) {
	cmd := componentcmds.NewRefreshUpstreamCommitCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "refresh-upstream-commit", cmd.Use)
	assert.NotNil(t, cmd.RunE)
	assert.Nil(t, cmd.Flags().Lookup("bump"))
	assert.Contains(t, cmd.Annotations, azldev.CommandAnnotationPermissiveConfig)
}

func TestNewRefreshUpstreamCommitCmd_Flags(t *testing.T) {
	cmd := componentcmds.NewRefreshUpstreamCommitCmd()

	allFlag := cmd.Flags().Lookup("all-components")
	require.NotNil(t, allFlag, "all-components flag should be registered")

	componentFlag := cmd.Flags().Lookup("component")
	require.NotNil(t, componentFlag, "component flag should be registered")
}

func TestRefreshUpstreamCommitCmd_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnvWithoutLockfile(t)

	cmd := componentcmds.NewRefreshUpstreamCommitCmd()
	cmd.SetArgs([]string{"nonexistent-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

func TestRefreshUpstreamCommitCmd_CleansPinsThatInvalidateStrictConfig(t *testing.T) {
	testCases := []struct {
		name            string
		componentConfig string
	}{
		{
			name: "removed component",
		},
		{
			name: "converted to local",
			componentConfig: `[components.test-component.spec]
type = "local"
path = "../../specs/test-component.spec"
`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testEnv := testutils.NewTestEnvWithoutLockfile(t)

			rootConfig := `includes = [
    "base/upstream-commits/*.toml",
    "base/components/*.toml",
]

[project]
default-distro = { name = "test-distro", version = "1.0" }

[distros.test-distro]
default-version = "1.0"

[distros.test-distro.versions."1.0"]
release-ver = "1.0"
`
			require.NoError(t, fileutils.WriteFile(
				testEnv.TestFS,
				"/project/azldev.toml",
				[]byte(rootConfig),
				fileperms.PublicFile,
			))

			if testCase.componentConfig != "" {
				require.NoError(t, fileutils.WriteFile(
					testEnv.TestFS,
					"/project/base/components/test-component.toml",
					[]byte(testCase.componentConfig),
					fileperms.PublicFile,
				))
			}

			store := upstreamcommit.NewStore(testEnv.TestFS, testUpstreamCommitsDir)
			require.NoError(t, store.Save("test-component", "abc1234"))

			app := azldev.NewApp(
				testEnv.TestInterfaces.FileSystemFactory,
				testEnv.TestInterfaces.OSEnvFactory,
			)

			// The command is only registered in lock-file-free mode, which the
			// CLI selects by pre-parsing the global flags before registration.
			args := []string{
				"--without-lockfile", "component", "refresh-upstream-commit", "--all-components",
			}

			app.PreParseGlobalFlags(args)
			componentcmds.OnAppInit(app)

			exitCode := app.Execute(args)
			require.Zero(t, exitCode)

			_, exists, err := store.Get("test-component")
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

// addRefreshUpstreamComponent adds an upstream component to the test config
// without pre-populating generated commit TOML.
func addRefreshUpstreamComponent(env *testutils.TestEnv, name string) {
	env.Config.Components[name] = projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
		},
	}
}

// TestRefreshUpstreamCommits_WritesCommit exercises the full refresh pipeline.
func TestRefreshUpstreamCommits_WritesCommit(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const commit = "abc123def456"

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, "curl")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)
	assert.Equal(t, commit, results[0].UpstreamCommit)

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)

	savedCommit, exists, loadErr := store.Get("curl")
	require.NoError(t, loadErr)
	assert.True(t, exists)
	assert.Equal(t, commit, savedCommit)
}

func TestRefreshUpstreamCommits_ConfigOnlyChangeDoesNotChangeCommitTOML(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const commit = "abc123def456"

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, "curl")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	options := &componentcmds.RefreshUpstreamCommitOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	results, err := componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)

	modifiedConfig := env.Config.Components["curl"]
	modifiedConfig.Build.With = []string{"ssl"}
	env.Config.Components["curl"] = modifiedConfig

	results, err = componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err)
	assert.Empty(t, results)

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	savedCommit, exists, err := store.Get("curl")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, commit, savedCommit)
}

// TestRefreshUpstreamCommits_MultipleComponents tests refreshing multiple components.
func TestRefreshUpstreamCommits_MultipleComponents(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const commit = "multi-commit-hash"

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, "curl")
	addRefreshUpstreamComponent(env, "bash")

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
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

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)

	curlCommit, curlExists, err := store.Get("curl")
	require.NoError(t, err)
	bashCommit, bashExists, err := store.Get("bash")
	require.NoError(t, err)
	assert.True(t, curlExists)
	assert.True(t, bashExists)
	assert.Equal(t, commit, curlCommit)
	assert.Equal(t, commit, bashCommit)
}

func TestRefreshUpstreamCommits_LocalComponentDoesNotWriteCommitTOML(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	env.Config.Components["local-pkg"] = projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/specs/local-pkg/local-pkg.spec",
		},
	}

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		})
	require.NoError(t, err)
	assert.Empty(t, results)

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	_, exists, err := store.Get("local-pkg")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRefreshUpstreamCommits_NonUpstreamComponentRemovesGeneratedCommitTOML(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	env.Config.Components["local-pkg"] = projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/specs/local-pkg/local-pkg.spec",
		},
	}

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, store.Save("local-pkg", "stale-commit"))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{
				ComponentNamePatterns: []string{"local-pkg"},
			},
		})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "local-pkg", results[0].Component)
	assert.True(t, results[0].Changed)
	assert.True(t, results[0].Removed)

	_, exists, err := store.Get("local-pkg")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRefreshUpstreamCommits_CheckOnlyDetectsNonUpstreamGeneratedCommitTOML(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	env.Config.Components["local-pkg"] = projectconfig.ComponentConfig{
		Name: "local-pkg",
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/specs/local-pkg/local-pkg.spec",
		},
	}

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, store.Save("local-pkg", "stale-commit"))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{
				ComponentNamePatterns: []string{"local-pkg"},
			},
			CheckOnly: true,
		})
	require.ErrorContains(t, err, "local-pkg")
	require.Len(t, results, 1)
	assert.True(t, results[0].Changed)
	assert.True(t, results[0].Removed)

	_, exists, err := store.Get("local-pkg")
	require.NoError(t, err)
	assert.True(t, exists, "--check-only must not remove generated TOML files")
}

// TestRefreshUpstreamCommits_AdvancesStaleCommit is a regression test for the case
// where a generated pin is at commit A and the snapshot resolves to
// commit B must result in B being written (not A echoed back). Without
// clearing the configured pin before re-resolution, source resolution would
// return A and the generated TOML would never advance.
func TestRefreshUpstreamCommits_AdvancesStaleCommit(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const initialCommit = "initial-aaa111"

	const advancedCommit = "advanced-bbb222"

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))
	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, store.Save("curl", initialCommit))

	addRefreshUpstreamComponent(env, "curl")

	// Mock git now resolves to a NEW commit — upstream moved.
	setupMockGit(env, advancedCommit)

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		})
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, advancedCommit, results[0].UpstreamCommit,
		"refresh must re-resolve and return the advanced commit, not echo the configured one")
	assert.True(t, results[0].Changed, "generated commit advanced")
	assert.Equal(t, initialCommit, results[0].PreviousCommit,
		"PreviousCommit should track the prior generated TOML")

	freshStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	updatedCommit, exists, loadErr := freshStore.Get("curl")
	require.NoError(t, loadErr)
	assert.True(t, exists)
	assert.Equal(t, advancedCommit, updatedCommit)
}

// TestRefreshUpstreamCommits_CheckOnly_StaleReturnsError verifies that '--check-only'
// returns a non-nil error when a generated commit TOML is stale without writing.
func TestRefreshUpstreamCommits_CheckOnly_StaleReturnsError(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const initialCommit = "initial-aaa111"

	const advancedCommit = "advanced-bbb222"

	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))
	preStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, preStore.Save("curl", initialCommit))

	addRefreshUpstreamComponent(env, "curl")
	setupMockGit(env, advancedCommit)

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
			CheckOnly:       true,
		})
	require.Error(t, err, "stale TOML must produce a non-nil error in --check-only mode")
	assert.Contains(t, err.Error(), "stale", "error message should mention staleness")
	assert.Contains(t, err.Error(), "curl", "error message should name the stale component")
	assert.Contains(t, err.Error(), "azldev component refresh-upstream-commit -a",
		"-a-scoped run should suggest the same -a invocation to refresh")

	// Results slice must be returned alongside the error so structured
	// consumers (e.g. -O json) retain per-component data on stale runs.
	require.NotEmpty(t, results, "results must be returned even when stale")

	var foundCurl bool

	for _, r := range results {
		if r.Component == "curl" {
			foundCurl = true

			assert.True(t, r.Changed, "stale curl must surface as Changed in returned results")
		}
	}

	assert.True(t, foundCurl, "stale curl must appear in returned results slice")

	freshStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	savedCommit, exists, loadErr := freshStore.Get("curl")
	require.NoError(t, loadErr)
	assert.True(t, exists)
	assert.Equal(t, initialCommit, savedCommit)
}

// TestRefreshUpstreamCommits_CheckOnly_FreshReturnsNil verifies that '--check-only'
// returns nil when all generated commit TOMLs are fresh.
func TestRefreshUpstreamCommits_CheckOnly_FreshReturnsNil(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const commit = "fresh-commit-aaa"

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, "curl")
	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	options := &componentcmds.RefreshUpstreamCommitOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	// Phase 1: populate the generated TOML with a real refresh run.
	_, err := componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err)

	freshStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	before, beforeExists, loadErr := freshStore.Get("curl")
	require.NoError(t, loadErr)
	require.True(t, beforeExists)

	// Phase 2: --check-only against the now-fresh TOML. Must return nil.
	options.CheckOnly = true
	_, err = componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err, "fresh TOMLs must return nil error in --check-only mode")

	// The configured commit must remain unchanged.
	freshStore = upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	after, afterExists, loadErr := freshStore.Get("curl")
	require.NoError(t, loadErr)
	require.True(t, afterExists)
	assert.Equal(t, before, after)
}

// TestRefreshUpstreamCommits_CheckOnly_DetectsOrphans verifies that '--check-only'
// returns an error when an orphan generated TOML would be pruned by a normal run,
// and that the orphan is NOT actually deleted.
func TestRefreshUpstreamCommits_CheckOnly_DetectsOrphans(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	const commit = "fresh-commit-aaa"

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, "curl")
	require.NoError(t, fileutils.MkdirAll(env.TestFS, testUpstreamCommitsDir))

	// First, do a real refresh so curl's TOML is fresh; this isolates the orphan as
	// the only thing --check-only should flag.
	_, err := componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		})
	require.NoError(t, err)

	// Plant an orphan TOML after the refresh; a normal refresh would have
	// pruned it. The orphan does NOT correspond to any component in config.
	preStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, preStore.Save("removed-pkg", "orphan-commit"))

	// --check-only must report the orphan and not delete it.
	_, err = componentcmds.RefreshUpstreamCommits(
		env.Env, &componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
			CheckOnly:       true,
		})
	require.Error(t, err, "orphan TOML must produce an error in --check-only mode")
	assert.Contains(t, err.Error(), "orphan")
	assert.Contains(t, err.Error(), "removed-pkg")

	freshStore := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	_, exists, loadErr := freshStore.Get("removed-pkg")
	require.NoError(t, loadErr)
	assert.True(t, exists, "--check-only must not prune orphan TOMLs")
}
