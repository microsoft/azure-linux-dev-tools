// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/project"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProjectCmd(t *testing.T) {
	cmd := project.NewNewCmd()
	require.NotNil(t, cmd)

	assert.Equal(t, "new", cmd.Name())
}

func TestRunNewProjectCmd(t *testing.T) {
	const testProjectPath = "/test/project"

	t.Run("no positional args", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		cmd := project.NewNewCmd()
		cmd.SetContext(env.Env)

		err := cmd.Execute()
		require.Error(t, err)
	})

	t.Run("single positional args", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		cmd := project.NewNewCmd()
		cmd.SetContext(env.Env)
		cmd.SetArgs([]string{testProjectPath})

		err := cmd.Execute()
		require.NoError(t, err)

		// We don't properly validate the behavior since that's already covered by
		// the tests in the projectgen_test package. We at least make a minimal
		// effort to ensure the well-known config file exists now to check that
		// something meaningful happened.
		_, err = env.FS().Stat(filepath.Join(testProjectPath, projectconfig.DefaultConfigFileName))
		require.NoError(t, err)
	})

	t.Run("multiple positional args", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		cmd := project.NewNewCmd()
		cmd.SetContext(env.Env)
		cmd.SetArgs([]string{"arg1", "arg2"})

		err := cmd.Execute()
		require.Error(t, err)
	})
}
