// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBuildCommand(t *testing.T) {
	cmd := componentcmds.NewBuildCmd()
	if assert.NotNil(t, cmd) {
		assert.Equal(t, "build", cmd.Use)
		assert.NotNil(t, cmd.RunE)
	}
}

func TestNewBuildCommand_MockConfigOptFlag(t *testing.T) {
	cmd := componentcmds.NewBuildCmd()

	// Verify that the --mock-config-opt flag is registered and functional.
	flag := cmd.Flags().Lookup("mock-config-opt")
	require.NotNil(t, flag)
	assert.Equal(t, "mock-config-opt", flag.Name)
}

func TestRunBuildCommand_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := testutils.PrepareCommand(componentcmds.NewBuildCmd(), testEnv.Env)
	err := cmd.RunE(cmd, []string{})

	// Should complain that no components were selected.
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "no components were selected")
	}
}

func TestBuildComponents_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	options := componentcmds.ComponentBuildOptions{}

	results, err := componentcmds.SelectAndBuildComponents(testEnv.Env, &options)
	require.Error(t, err)
	require.Empty(t, results)
}

func TestValidateBuildOptions_SRPMOnlyWithPublish(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := testutils.PrepareCommand(componentcmds.NewBuildCmd(), testEnv.Env)
	cmd.SetArgs([]string{"--srpm-only", "--local-repo-with-publish=/some/repo"})
	err := cmd.Execute()

	require.Error(t, err)
	assert.Contains(
		t, err.Error(),
		"if any flags in the group [srpm-only local-repo-with-publish] are set none of the others can be",
	)
}

func TestValidateBuildOptions_PathOverlap(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	options := componentcmds.ComponentBuildOptions{
		LocalRepoPaths:           []string{"/shared/repo"},
		LocalRepoWithPublishPath: "/shared/repo",
	}

	_, err := componentcmds.SelectAndBuildComponents(testEnv.Env, &options)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "appears in both --local-repo and --local-repo-with-publish")
}

func TestValidateBuildOptions_NoOverlapWithDifferentPaths(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// This should not fail validation due to path overlap.
	// It will fail due to createrepo_c not being available (which is expected in unit tests).
	options := componentcmds.ComponentBuildOptions{
		LocalRepoPaths:           []string{"/upstream/repo"},
		LocalRepoWithPublishPath: "/dev/repo",
	}

	_, err := componentcmds.SelectAndBuildComponents(testEnv.Env, &options)

	// Should fail because createrepo_c is not available, NOT because of path overlap.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "createrepo_c")
	assert.NotContains(t, err.Error(), "appears in both")
}
