// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mock_test

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testMockConfigPath = "/mock/config"
	testMockRootPath   = "/mock/root"
	testSpecPath       = "/sources/component.spec"
	testSourceDirPath  = "/sources"
	testOutputDirPath  = "/output"
	testSRPMPath       = "/output/component-1.0.0.src.rpm"
	testRPMPath        = "/output/component-1.0.0.rpm"
)

func newTestCtxWithMockPrereqsPresent() *testctx.TestCtx {
	ctx := testctx.NewCtx()
	ctx.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

	return ctx
}

func TestConfigPath(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	assert.Equal(t, testMockConfigPath, runner.ConfigPath())
}

func TestMockNotPresent(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	_, err := runner.GetRootPath(ctx)
	require.Error(t, err)
}

func TestRunnerDefaults(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	runner := mock.NewRunner(ctx, testMockConfigPath)

	// Pin defaults.
	assert.Equal(t, testMockConfigPath, runner.ConfigPath())
	assert.Empty(t, runner.BindMounts())
	assert.False(t, runner.HasNetworkEnabled())
	assert.False(t, runner.HasNoPreClean())
	assert.False(t, runner.HasUnprivileged())
	assert.Empty(t, runner.BaseDir())
	assert.Empty(t, runner.RootDir())
	assert.Empty(t, runner.ConfigOpts())
}

func TestClone(t *testing.T) {
	const (
		testBaseDir   = "/base"
		testRootDir   = "/root"
		testHostPath  = "/host"
		testGuestPath = "/guest"
	)

	ctx := newTestCtxWithMockPrereqsPresent()
	runner := mock.NewRunner(ctx, testMockConfigPath)

	// Set a number of non-default options so we can confirm they propagate to the clone.
	runner.AddBindMount(testHostPath, testGuestPath)
	runner.WithBaseDir(testBaseDir)
	runner.WithRootDir(testRootDir)
	runner.WithNoPreClean()
	runner.EnableNetwork()
	runner.WithUnprivileged()
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"})

	clone := runner.Clone()

	// Confirm that the clone matches the original runner.
	assert.Equal(t, testMockConfigPath, clone.ConfigPath())
	assert.Equal(t, map[string]string{testHostPath: testGuestPath}, clone.BindMounts())
	assert.True(t, clone.HasNetworkEnabled())
	assert.True(t, clone.HasNoPreClean())
	assert.True(t, clone.HasUnprivileged())
	assert.Equal(t, testBaseDir, clone.BaseDir())
	assert.Equal(t, testRootDir, clone.RootDir())
	assert.Equal(t, map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"}, clone.ConfigOpts())
}

func TestGetRootPath(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	ctx.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		if filepath.Base(cmd.Path) == mock.MockBinary {
			return testMockRootPath, nil
		}

		return "", nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)

	rootPath, err := runner.GetRootPath(ctx)
	require.NoError(t, err)
	assert.Equal(t, testMockRootPath, rootPath)
}

func TestBuildSRPM(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testSRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.SRPMBuildOptions{}

	err := runner.BuildSRPM(ctx, testSpecPath, testSourceDirPath, testOutputDirPath, options)
	require.NoError(t, err)

	// Confirm that we invoked mock, and didn't do so under sudo.
	mockCmd := executedCmds[0]
	assert.Regexp(t, `^mock`, mockCmd)

	// Confirm that we invoked mock with the expected arguments.
	assert.Contains(t, mockCmd, "-r "+testMockConfigPath)
}

func TestBuildSRPM_MockFails(t *testing.T) {
	testError := errors.New("injected mock failure")

	ctx := newTestCtxWithMockPrereqsPresent()
	ctx.CmdFactory.RunHandler = func(_ *exec.Cmd) error { return testError }

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.SRPMBuildOptions{}

	err := runner.BuildSRPM(ctx, testSpecPath, testSourceDirPath, testOutputDirPath, options)
	require.ErrorIs(t, err, testError)
}

func TestBuildRPM(t *testing.T) {
	const (
		withoutFeature = "without-feature"
		withFeature    = "with-feature"
		macroName      = "macro-name"
		macroValue     = "macro-value"
	)

	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	mockOptions := mock.RPMBuildOptions{
		CommonBuildOptions: mock.CommonBuildOptions{
			Without: []string{withoutFeature},
			With:    []string{withFeature},
			Defines: map[string]string{macroName: macroValue},
		},
	}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mockOptions)
	require.NoError(t, err)

	// Confirm that mock was invoked once.
	assert.Len(t, executedCmds, 1)

	// Confirm that we invoked mock, and didn't do so under sudo.
	mockCmd := executedCmds[0]
	assert.Regexp(t, `^mock`, mockCmd)

	// Confirm that we invoked mock with the expected arguments.
	assert.Contains(t, mockCmd, "-r "+testMockConfigPath)
	assert.Contains(t, mockCmd, "--without "+withoutFeature)
	assert.Contains(t, mockCmd, "--with "+withFeature)
	assert.Contains(t, mockCmd, fmt.Sprintf("--define %s %s", macroName, macroValue))
}

func TestBuildRPM_MockFails(t *testing.T) {
	testError := errors.New("injected mock failure")

	ctx := newTestCtxWithMockPrereqsPresent()
	ctx.CmdFactory.RunHandler = func(_ *exec.Cmd) error { return testError }

	runner := mock.NewRunner(ctx, testMockConfigPath)
	options := mock.RPMBuildOptions{}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, options)
	require.ErrorIs(t, err, testError)
}

func TestCmdInChroot_MockNotPresent(t *testing.T) {
	ctx := testctx.NewCtx()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	_, err := runner.CmdInChroot(ctx, []string{"arg1", "arg2"}, true /*interactive*/)
	require.Error(t, err)
}

func TestCmdInChroot_Success(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)

	cmd, err := runner.CmdInChroot(ctx, []string{"arg1", "arg2 with spaces"}, true /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Make sure our args end up in the command line (with quoting as needed).
	assert.Contains(t, cmd.GetArgs(), "arg1 'arg2 with spaces'")

	// Ensure we *don't* see unexpected options.
	assert.NotContains(t, cmd.GetArgs(), "--no-clean")
	assert.NotContains(t, cmd.GetArgs(), "--plugin-option=bind_mount")
}

func TestCmdInChroot_BindMount(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.AddBindMount("/host-path", "/mock-path")
	assert.Equal(t, map[string]string{"/host-path": "/mock-path"}, runner.BindMounts())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for bind-mount arg.
	assert.Contains(t, cmd.GetArgs(), `--plugin-option=bind_mount:dirs=[("/host-path", "/mock-path")]`)
}

func TestCmdInChroot_NoPreClean(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithNoPreClean()
	assert.True(t, runner.HasNoPreClean())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for no-clean arg.
	assert.Contains(t, cmd.GetArgs(), "--no-clean")
}

func TestCmdInChroot_EnableNetworking(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.EnableNetwork()
	assert.True(t, runner.HasNetworkEnabled())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for enable-network arg.
	assert.Contains(t, cmd.GetArgs(), "--enable-network")
}

func TestCmdInChroot_ConfigOpts(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"})
	assert.Equal(t, map[string]string{"cleanup_on_success": "True", "cleanup_on_failure": "False"}, runner.ConfigOpts())

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for config-opts args (keys should be sorted deterministically).
	cmdArgs := cmd.GetArgs()
	assert.Contains(t, cmdArgs, "--config-opts")
	assert.Contains(t, cmdArgs, "cleanup_on_failure=False")
	assert.Contains(t, cmdArgs, "cleanup_on_success=True")
}

func TestCmdInChroot_Unprivileged(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithUnprivileged()

	cmd, err := runner.CmdInChroot(ctx, []string{"arg"}, false /*interactive*/)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Look for unpriv arg.
	assert.Contains(t, cmd.GetArgs(), "--unpriv")
}

func TestBuildRPM_WithConfigOpts(t *testing.T) {
	ctx := newTestCtxWithMockPrereqsPresent()
	executedCmds := []string{}

	ctx.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		executedCmds = append(executedCmds, strings.Join(cmd.Args, " "))

		require.NoError(t, fileutils.WriteFile(ctx.FS(), testRPMPath, []byte{}, fileperms.PrivateFile))

		return nil
	}

	runner := mock.NewRunner(ctx, testMockConfigPath)
	runner.WithConfigOpts(map[string]string{"cleanup_on_success": "True"})

	mockOptions := mock.RPMBuildOptions{}

	err := runner.BuildRPM(ctx, testSRPMPath, testOutputDirPath, mockOptions)
	require.NoError(t, err)

	// Confirm that mock was invoked once.
	assert.Len(t, executedCmds, 1)

	// Confirm the config-opts flag was passed through.
	mockCmd := executedCmds[0]
	assert.Contains(t, mockCmd, "--config-opts cleanup_on_success=True")
}
