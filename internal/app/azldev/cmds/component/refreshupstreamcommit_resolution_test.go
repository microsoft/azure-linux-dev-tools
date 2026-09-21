// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/upstreamcommit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshUpstreamCommitAlwaysResolvesUpstreamComponent(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)

	gitCalls := setupMockGitWithCounter(env, "aabbccdd11223344")
	addRefreshUpstreamComponent(env, "curl")

	options := &componentcmds.RefreshUpstreamCommitOptions{
		ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
	}

	_, err := componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err)
	require.Positive(t, gitCalls.Load())

	gitCalls.Store(0)

	results, err := componentcmds.RefreshUpstreamCommits(env.Env, options)
	require.NoError(t, err)
	assert.Positive(t, gitCalls.Load(), "repeated updates must re-resolve upstream state")

	for _, result := range results {
		if result.Component == "curl" {
			assert.False(t, result.Changed)
		}
	}
}

func TestRefreshUpstreamCommitContinuesAfterComponentError(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)
	env.Env.SetConcurrency(1)

	for _, name := range []string{"first", "second"} {
		env.Config.Components[name] = projectconfig.ComponentConfig{
			Name: name,
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeUpstream,
				UpstreamDistro: projectconfig.DistroReference{
					Name: "missing-" + name,
				},
			},
		}
	}

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env,
		&componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		},
	)
	require.Error(t, err)
	require.Len(t, results, 2)

	for _, result := range results {
		assert.NotEmpty(t, result.Error)
		assert.False(t, result.Skipped)
	}
}

func TestRefreshUpstreamCommitSavesSuccessfulComponentsAfterError(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)
	env.Env.SetConcurrency(1)

	const (
		failingComponent    = "a-fails"
		successfulComponent = "z-succeeds"
		commit              = "aabbccdd11223344"
	)

	setupMockGit(env, commit)
	addRefreshUpstreamComponent(env, successfulComponent)
	env.Config.Components[failingComponent] = projectconfig.ComponentConfig{
		Name: failingComponent,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeUpstream,
			UpstreamDistro: projectconfig.DistroReference{
				Name: "missing-distro",
			},
		},
	}

	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	require.NoError(t, store.Save(successfulComponent, "old-commit"))

	results, err := componentcmds.RefreshUpstreamCommits(
		env.Env,
		&componentcmds.RefreshUpstreamCommitOptions{
			ComponentFilter: components.ComponentFilter{IncludeAllComponents: true},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "successful upstream commit TOML files were updated")

	resultsByComponent := make(map[string]componentcmds.RefreshUpstreamCommitResult, len(results))
	for _, result := range results {
		resultsByComponent[result.Component] = result
	}

	assert.NotEmpty(t, resultsByComponent[failingComponent].Error)
	assert.False(t, resultsByComponent[failingComponent].Skipped)
	assert.Equal(t, commit, resultsByComponent[successfulComponent].UpstreamCommit)
	assert.True(t, resultsByComponent[successfulComponent].Changed)

	savedCommit, exists, loadErr := store.Get(successfulComponent)
	require.NoError(t, loadErr)
	assert.True(t, exists)
	assert.Equal(t, commit, savedCommit)

	_, exists, loadErr = store.Get(failingComponent)
	require.NoError(t, loadErr)
	assert.False(t, exists)
}
