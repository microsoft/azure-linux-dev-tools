// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/upstreamcommit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUpstreamCommitsDir = "/project/base/upstream-commits"

func TestSaveUpstreamCommitConfigs_WritesChangedCommit(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)
	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	results := []RefreshUpstreamCommitResult{{
		Component:      "curl",
		UpstreamCommit: "abc123",
		Changed:        true,
	}}

	require.NoError(t, saveUpstreamCommitConfigs(store, results, false))

	commit, exists, err := store.Get("curl")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "abc123", commit)
}

func TestSaveUpstreamCommitConfigs_SkipsUnchangedAndFailed(t *testing.T) {
	env := testutils.NewTestEnvWithoutLockfile(t)
	store := upstreamcommit.NewStore(env.TestFS, testUpstreamCommitsDir)
	results := []RefreshUpstreamCommitResult{
		{Component: "unchanged"},
		{Component: "errored", Changed: true, Error: "resolution failed"},
		{Component: "skipped", Changed: true, Skipped: true, SkipReason: "cancelled"},
	}

	require.NoError(t, saveUpstreamCommitConfigs(store, results, false))

	for _, componentName := range []string{"unchanged", "errored", "skipped"} {
		_, exists, err := store.Get(componentName)
		require.NoError(t, err)
		assert.False(t, exists)
	}
}
