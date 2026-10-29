// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/image"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageTestCmd(t *testing.T) {
	cmd := image.NewImageTestCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "test IMAGE_NAME", cmd.Use)
	assert.Contains(t, cmd.Short, "test")
}

func TestNewImageTestCmd_Flags(t *testing.T) {
	cmd := image.NewImageTestCmd()

	assert.NotNil(t, cmd.Flags().Lookup("test-suite"))
	assert.NotNil(t, cmd.Flags().Lookup("image-path"))
	assert.NotNil(t, cmd.Flags().Lookup("junit-xml"))
}

func TestImageTestPreservesPytestOptions(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"smoke": {
			Type: string(projectconfig.TestTypePytest),
			Pytest: map[string]any{
				"working-dir": "/tests",
				"test-paths":  []string{"cases/"},
				"extra-args":  []string{"--image", "{image-name}", "--path", "{image-path}"},
				"install":     "none",
			},
		},
	}
	testEnv.Config.Images = map[string]projectconfig.ImageConfig{
		"selected-image": {
			Name: "configured-image",
			Tests: &projectconfig.ImageTestsConfig{
				Tests: []projectconfig.TestRef{{Name: "smoke"}},
			},
		},
	}

	venvDir := filepath.Join(testEnv.Env.WorkDir(), "pytest-venv", "smoke")
	for _, directory := range []string{"/tests", "/images", filepath.Join(venvDir, "bin")} {
		require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, directory))
	}

	venvPython := filepath.Join(venvDir, "bin", "python3")
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, venvPython, nil, fileperms.PublicExecutable))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/images/candidate.raw", nil, fileperms.PublicFile))
	testEnv.CmdFactory.RegisterCommandInSearchPath("python3")

	var commands [][]string

	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		require.Equal(t, "/tests", cmd.Dir)
		commands = append(commands, cmd.Args)

		return nil
	}
	cmd := image.NewImageTestCmd()
	cmd.SetArgs([]string{
		"selected-image", "--image-path", "/images/candidate.raw",
		"--junit-xml", "/reports/results.xml",
	})
	require.NoError(t, cmd.ExecuteContext(testEnv.Env))
	require.Equal(t, [][]string{{
		venvPython, "-m", "pytest", "cases/", "--image", "selected-image",
		"--path", "/images/candidate.raw", "--junit-xml", "/reports/results.xml",
	}}, commands)
}

func TestImageTestPreservesLISAOptions(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests["smoke"] = projectconfig.TestDefinition{
		Type: string(projectconfig.TestTypeLisa),
		Lisa: map[string]any{
			"name": "selected-case",
			"extra-args": []any{
				"--image", "{image-name}", "--path", "{image-path}",
			},
		},
	}
	testEnv.Config.Images["selected-image"] = projectconfig.ImageConfig{
		Name: "configured-image",
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "smoke"}},
		},
	}

	frameworkDir := "/checkouts/lisa"

	venvDir := filepath.Join(testEnv.Env.WorkDir(), "lisa", "venv", "smoke")
	for _, directory := range []string{frameworkDir, "/images", filepath.Join(venvDir, "bin")} {
		require.NoError(t, fileutils.MkdirAll(testEnv.TestFS, directory))
	}

	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, filepath.Join(venvDir, "bin", "python3"), nil, fileperms.PublicExecutable,
	))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, "/images/candidate.qcow2", nil, fileperms.PublicFile))
	testEnv.CmdFactory.RegisterCommandInSearchPath("python3")
	testEnv.CmdFactory.RegisterCommandInSearchPath("ssh-keygen")

	runbookPath := filepath.Join(frameworkDir, "azldev-generated-smoke.yml")

	var lisaCommands [][]string

	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if filepath.Base(cmd.Args[0]) == "lisa" {
			require.Equal(t, filepath.Join(frameworkDir, "lisa"), cmd.Dir)
			lisaCommands = append(lisaCommands, cmd.Args)
			runbook, err := fileutils.ReadFile(testEnv.TestFS, runbookPath)
			require.NoError(t, err)
			assert.Contains(t, string(runbook), "selected-case")
			assert.Contains(t, string(runbook), "/images/candidate.qcow2")
		}

		return nil
	}
	cmd := image.NewImageTestCmd()
	cmd.SetArgs([]string{
		"selected-image", "--image-path", "/images/candidate.qcow2", "--lisa-dir", frameworkDir,
	})
	require.NoError(t, cmd.ExecuteContext(testEnv.Env))
	require.Equal(t, [][]string{{
		filepath.Join(venvDir, "bin", "lisa"), "-r", runbookPath,
		"--image", "selected-image", "--path", "/images/candidate.qcow2",
	}}, lisaCommands)
	exists, err := fileutils.Exists(testEnv.TestFS, runbookPath)
	require.NoError(t, err)
	assert.False(t, exists, "generated runbook should still be removed")
}
