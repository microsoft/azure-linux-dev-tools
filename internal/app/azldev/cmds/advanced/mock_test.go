// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced_test

import (
	"os/exec"
	"slices"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/advanced"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMockCommand(t *testing.T) {
	cmd := advanced.NewMockCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "mock", cmd.Use)
}

func TestNewBuildRPMCmd(t *testing.T) {
	cmd := advanced.NewBuildRPMCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "build-rpms", cmd.Use)
}

func TestNewShellCmd(t *testing.T) {
	cmd := advanced.NewShellCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "shell", cmd.Use)
}

func TestBuildRPMS(t *testing.T) {
	const (
		testMockConfigPath = "/mock/mock.cfg"
		testSRPMPath       = "/input/test.srpm"
		testOutputDirPath  = "/output"
	)

	t.Run("NoProjectConfig", func(t *testing.T) {
		// Construct a new env with no configuration.
		testEnv := testutils.NewTestEnv(t)

		// Pretend that "mock" exists.
		testEnv.CmdFactory.RegisterCommandInSearchPath("mock")

		rpmOptions := &advanced.BuildRPMOptions{
			MockCmdOptions: advanced.MockCmdOptions{
				MockConfigPath: testMockConfigPath,
			},
			SRPMPath:      testSRPMPath,
			OutputDirPath: testOutputDirPath,
		}

		// Confirm that we can "build" RPMs, even without a valid loaded configuration.
		result, err := advanced.BuildRPMS(testEnv.Env, rpmOptions)
		require.NoError(t, err)
		require.Equal(t, true, result)
	})

	t.Run("NoCheck", func(t *testing.T) {
		// Construct a new env with no configuration.
		testEnv := testutils.NewTestEnv(t)

		// Pretend that "mock" exists.
		testEnv.CmdFactory.RegisterCommandInSearchPath("mock")

		// Keep track of what commands get launched.
		cmds := [][]string{}
		testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
			cmds = append(cmds, cmd.Args)

			return nil
		}

		rpmOptions := &advanced.BuildRPMOptions{
			MockCmdOptions: advanced.MockCmdOptions{
				MockConfigPath: testMockConfigPath,
			},
			SRPMPath:      testSRPMPath,
			OutputDirPath: testOutputDirPath,
			NoCheck:       true,
		}

		result, err := advanced.BuildRPMS(testEnv.Env, rpmOptions)
		require.NoError(t, err)
		require.Equal(t, true, result)

		// Verify that '--nocheck' was passed to mock.
		found := false

		for _, cmd := range cmds {
			if slices.Contains(cmd, "mock") && slices.Contains(cmd, "--nocheck") {
				found = true

				break
			}
		}

		require.True(t, found, "Expected '--nocheck' flag to be passed to mock")
	})

	t.Run("ValidProjectConfig", func(t *testing.T) {
		testEnv := testutils.NewTestEnv(t)

		testEnv.Config.Project.DefaultDistro = projectconfig.DistroReference{
			Name:    "test-distro",
			Version: "1.0",
		}

		testEnv.Config.Distros["test-distro"] = projectconfig.DistroDefinition{
			Versions: map[string]projectconfig.DistroVersionDefinition{
				"1.0": {
					MockConfigPath: testMockConfigPath,
				},
			},
		}

		// Pretend that "mock" exists.
		testEnv.CmdFactory.RegisterCommandInSearchPath("mock")

		rpmOptions := &advanced.BuildRPMOptions{
			SRPMPath:      testSRPMPath,
			OutputDirPath: testOutputDirPath,
		}

		require.NoError(t, fileutils.WriteFile(testEnv.FS(), testMockConfigPath, []byte{}, 0o600))

		// Confirm that we can "build" RPMs without an explicit mock config file, because
		// the loaded project config provides that.
		result, err := advanced.BuildRPMS(testEnv.Env, rpmOptions)
		require.NoError(t, err)
		require.Equal(t, true, result)
	})
}

func TestRunShell(t *testing.T) {
	// Construct a new env.
	testEnv := testutils.NewTestEnv(t)

	// Pretend that "mock" exists.
	testEnv.CmdFactory.RegisterCommandInSearchPath("mock")

	// Keep track of what gets launched.
	cmds := [][]string{}
	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		cmds = append(cmds, cmd.Args)

		return nil
	}

	err := advanced.RunShell(testEnv.Env, &advanced.ShellOptions{}, []string{})
	require.NoError(t, err)

	found := false

	for _, cmd := range cmds {
		if slices.Contains(cmd, "mock") && slices.Contains(cmd, "--shell") {
			found = true

			break
		}
	}

	require.True(t, found, "Expected 'mock --shell' command to be executed")
}
