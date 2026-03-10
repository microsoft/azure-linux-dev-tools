// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/project"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitProjectCmd(t *testing.T) {
	cmd := project.NewInitCmd()
	require.NotNil(t, cmd)

	assert.Equal(t, "init", cmd.Name())
}

func TestRunInitProjectCmd(t *testing.T) {
	t.Run("no positional args", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		cmd := project.NewInitCmd()
		cmd.SetContext(env.Env)
		cmd.SetArgs([]string{})

		const testDirPath = "/newdir"

		// Create the test dir in our test filesystem and simulate CD'ing to it.
		require.NoError(t, fileutils.MkdirAll(env.FS(), testDirPath))
		require.NoError(t, env.TestOSEnv.Chdir(testDirPath))

		err := cmd.Execute()
		require.NoError(t, err)

		// We don't properly validate the behavior since that's already covered by
		// the tests in the projectgen_test package. We at least make a minimal
		// effort to ensure the well-known config file exists now to check that
		// something meaningful happened.
		_, err = env.FS().Stat(filepath.Join(testDirPath, projectconfig.DefaultConfigFileName))
		require.NoError(t, err)
	})

	t.Run("positional args", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		cmd := project.NewInitCmd()
		cmd.SetContext(env.Env)
		cmd.SetArgs([]string{"arg1", "arg2"})

		err := cmd.Execute()
		require.Error(t, err)
	})
}
