// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
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
