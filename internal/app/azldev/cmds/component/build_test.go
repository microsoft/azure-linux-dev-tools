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
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
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

func TestPlaceRPMsByChannel_EmptyChannelStaysInPlace(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	const (
		rpmsDir = "/output/rpms"
		rpmPath = "/output/rpms/curl-8.0-1.rpm"
	)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, rpmPath, []byte("rpm"), fileperms.PrivateFile))

	rpmResults := []componentcmds.RPMResult{
		{Path: rpmPath, PackageName: "curl", Channel: ""},
	}

	require.NoError(t, componentcmds.PlaceRPMsByChannel(testEnv.Env, rpmResults, rpmsDir))

	// File stays at its original path; RPMResult.Path is unchanged.
	assert.Equal(t, rpmPath, rpmResults[0].Path)
	_, err := testEnv.TestFS.Stat(rpmPath)
	assert.NoError(t, err, "original path should still exist")
}

func TestPlaceRPMsByChannel_NoneChannelStaysInPlace(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	const (
		rpmsDir = "/output/rpms"
		rpmPath = "/output/rpms/debuginfo-8.0-1.rpm"
	)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, rpmPath, []byte("rpm"), fileperms.PrivateFile))

	rpmResults := []componentcmds.RPMResult{
		{Path: rpmPath, PackageName: "debuginfo", Channel: "none"},
	}

	require.NoError(t, componentcmds.PlaceRPMsByChannel(testEnv.Env, rpmResults, rpmsDir))

	assert.Equal(t, rpmPath, rpmResults[0].Path)
	_, err := testEnv.TestFS.Stat(rpmPath)
	assert.NoError(t, err, "original path should still exist")
}

func TestPlaceRPMsByChannel_NamedChannelMovesFile(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	const (
		rpmsDir      = "/output/rpms"
		rpmPath      = "/output/rpms/curl-8.0-1.rpm"
		expectedPath = "/output/rpms/base/curl-8.0-1.rpm"
	)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, rpmPath, []byte("rpm"), fileperms.PrivateFile))

	rpmResults := []componentcmds.RPMResult{
		{Path: rpmPath, PackageName: "curl", Channel: "base"},
	}

	require.NoError(t, componentcmds.PlaceRPMsByChannel(testEnv.Env, rpmResults, rpmsDir))

	// RPMResult.Path must be updated to the new location.
	assert.Equal(t, expectedPath, rpmResults[0].Path)

	// File must exist at the channel subdirectory.
	_, err := testEnv.TestFS.Stat(expectedPath)
	require.NoError(t, err, "file should exist at channel subdirectory")

	// File must no longer exist at the original location.
	_, err = testEnv.TestFS.Stat(rpmPath)
	assert.Error(t, err, "file should have been moved away from the original path")
}

func TestPlaceRPMsByChannel_MultipleRPMsDifferentChannels(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	const rpmsDir = "/output/rpms"

	type rpmInput struct {
		path    string
		channel string
	}

	inputs := []rpmInput{
		{"/output/rpms/curl-8.0-1.rpm", "base"},
		{"/output/rpms/curl-devel-8.0-1.rpm", "devel"},
		{"/output/rpms/curl-debuginfo-8.0-1.rpm", "none"},
		{"/output/rpms/curl-static-8.0-1.rpm", ""},
	}

	rpmResults := make([]componentcmds.RPMResult, 0, len(inputs))

	for _, in := range inputs {
		require.NoError(t, fileutils.WriteFile(testEnv.TestFS, in.path, []byte("rpm"), fileperms.PrivateFile))

		rpmResults = append(rpmResults, componentcmds.RPMResult{
			Path: in.path, PackageName: "curl", Channel: in.channel,
		})
	}

	require.NoError(t, componentcmds.PlaceRPMsByChannel(testEnv.Env, rpmResults, rpmsDir))

	for _, result := range rpmResults {
		switch result.Channel {
		case "base":
			assert.Equal(t, "/output/rpms/base/curl-8.0-1.rpm", result.Path)
		case "devel":
			assert.Equal(t, "/output/rpms/devel/curl-devel-8.0-1.rpm", result.Path)
		case "none":
			assert.Equal(t, "/output/rpms/curl-debuginfo-8.0-1.rpm", result.Path)
		case "":
			assert.Equal(t, "/output/rpms/curl-static-8.0-1.rpm", result.Path)
		}

		_, statErr := testEnv.TestFS.Stat(result.Path)
		assert.NoError(t, statErr, "RPM should exist at its final resolved path")
	}
}

func TestPlaceRPMsByChannel_MultipleRPMsSameChannel(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	const rpmsDir = "/output/rpms"

	paths := []string{
		"/output/rpms/curl-8.0-1.rpm",
		"/output/rpms/libcurl-8.0-1.rpm",
	}

	rpmResults := make([]componentcmds.RPMResult, 0, len(paths))

	for _, path := range paths {
		require.NoError(t, fileutils.WriteFile(testEnv.TestFS, path, []byte("rpm"), fileperms.PrivateFile))

		rpmResults = append(rpmResults, componentcmds.RPMResult{
			Path: path, PackageName: "curl", Channel: "base",
		})
	}

	require.NoError(t, componentcmds.PlaceRPMsByChannel(testEnv.Env, rpmResults, rpmsDir))

	assert.Equal(t, "/output/rpms/base/curl-8.0-1.rpm", rpmResults[0].Path)
	assert.Equal(t, "/output/rpms/base/libcurl-8.0-1.rpm", rpmResults[1].Path)

	for _, result := range rpmResults {
		_, err := testEnv.TestFS.Stat(result.Path)
		assert.NoError(t, err, "both RPMs should exist in the shared channel subdirectory")
	}
}

func TestPlaceRPMsByChannel_EmptyInput(t *testing.T) {
	t.Parallel()

	testEnv := testutils.NewTestEnv(t)

	err := componentcmds.PlaceRPMsByChannel(testEnv.Env, nil, "/output/rpms")
	assert.NoError(t, err)
}

func makeEnvWithDirs(t *testing.T, workDir, outputDir string) *azldev.Env {
	t.Helper()

	base := testutils.NewTestEnv(t)

	cfg := projectconfig.NewProjectConfig()
	cfg.Project.WorkDir = workDir
	cfg.Project.OutputDir = outputDir

	options := azldev.NewEnvOptions()
	options.DryRunnable = base.DryRunnable
	options.EventListener = base.EventListener
	options.Interfaces = base.TestInterfaces
	options.Config = &cfg

	return azldev.NewEnv(t.Context(), options)
}

func TestBuildComponents_NoWorkDir(t *testing.T) {
	t.Parallel()

	env := makeEnvWithDirs(t, "", "/output")
	comps := components.NewComponentSet()

	_, err := componentcmds.BuildComponents(env, comps, &componentcmds.ComponentBuildOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "work dir")
}

func TestBuildComponents_NoOutputDir(t *testing.T) {
	t.Parallel()

	env := makeEnvWithDirs(t, "/work", "")
	comps := components.NewComponentSet()

	_, err := componentcmds.BuildComponents(env, comps, &componentcmds.ComponentBuildOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "output dir")
}
