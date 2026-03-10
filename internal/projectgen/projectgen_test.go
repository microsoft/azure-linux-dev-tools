// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectgen_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectgen"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireProjectHasValidDefaultConfig(t *testing.T, ctx opctx.Ctx, projectPath string) {
	t.Helper()

	// Check that we can find a config file under the project.
	foundRoot, configFilePath, err := projectconfig.FindProjectRootAndConfigFile(ctx.FS(), projectPath)
	require.NoError(t, err)
	assert.Equal(t, projectPath, foundRoot)
	assert.NotEmpty(t, configFilePath)

	// Load the config.
	_, config, err := projectconfig.LoadProjectConfig(
		ctx,
		ctx.FS(),
		projectPath,
		true, /*disable default config?*/
		t.TempDir(),
		nil,
		false,
	)

	require.NoError(t, err)
	assert.NotNil(t, config)

	// Check for some basic properties; we expect them to be filled out and not left empty.
	assert.NotNil(t, config.Project.LogDir)
	assert.NotNil(t, config.Project.WorkDir)
	assert.NotNil(t, config.Project.OutputDir)
}

func TestFindProjectRootAndConfigFile(t *testing.T) {
	const testProjectPath = "/test/project"

	ctx := testctx.NewCtx()

	// Create a sample project config file that overrides a baked-in default.
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testProjectPath))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(testProjectPath, projectconfig.DefaultConfigFileName),
		[]byte(`
[Project]
default-distro = { name = "other", version = "42.42" }
`),
		fileperms.PublicFile))

	// Load the project.
	foundProjectDir, config, err := projectconfig.LoadProjectConfig(
		ctx, ctx.FS(), testProjectPath, false /*disable default config?*/, t.TempDir(), nil, false,
	)

	require.NoError(t, err)
	require.Equal(t, testProjectPath, foundProjectDir)
	require.Equal(t, "other", config.Project.DefaultDistro.Name)
	require.Equal(t, "42.42", config.Project.DefaultDistro.Version)
}

func TestCreateNewProject(t *testing.T) {
	const testProjectPath = "/test/project"

	t.Run("FailsWhenProjectDirExists", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		// Create the test project path.
		require.NoError(t, fileutils.MkdirAll(env.FS(), testProjectPath))

		err := projectgen.CreateNewProject(env.FS(), testProjectPath, &projectgen.NewProjectOptions{})
		require.Error(t, err)
		assert.ErrorIs(t, err, projectgen.ErrProjectRootAlreadyExists)
	})

	t.Run("Success", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		// Run the code-under-test.
		err := projectgen.CreateNewProject(env.FS(), testProjectPath, &projectgen.NewProjectOptions{})
		require.NoError(t, err)

		// Validate the config.
		requireProjectHasValidDefaultConfig(t, env.Env, testProjectPath)

		// Check for .gitignore file.
		gitIgnoreFilePath := filepath.Join(testProjectPath, ".gitignore")
		_, statErr := env.FS().Stat(gitIgnoreFilePath)
		assert.NoError(t, statErr)
	})
}

func TestInitializeProject(t *testing.T) {
	const testProjectPath = "/test/project"

	t.Run("Success", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		// Create the test project path.
		require.NoError(t, fileutils.MkdirAll(env.FS(), testProjectPath))

		// Run the code-under-test.
		err := projectgen.InitializeProject(env.FS(), testProjectPath, &projectgen.NewProjectOptions{})
		require.NoError(t, err)

		// Validate the config.
		requireProjectHasValidDefaultConfig(t, env.Env, testProjectPath)
	})

	t.Run("FailsWhenConfigFileAlreadyExists", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		// Create the test project path and a config file.
		require.NoError(t, fileutils.MkdirAll(env.FS(), testProjectPath))
		configFilePath := filepath.Join(testProjectPath, projectconfig.DefaultConfigFileName)
		require.NoError(t, fileutils.WriteFile(env.FS(), configFilePath, []byte{}, 0o600))

		// Run the code-under-test.
		err := projectgen.InitializeProject(env.FS(), testProjectPath, &projectgen.NewProjectOptions{})
		require.Error(t, err)
	})

	t.Run("FailsWhenProjectDirDoesNotExist", func(t *testing.T) {
		env := testutils.NewTestEnv(t)

		// Run the code-under-test.
		err := projectgen.InitializeProject(env.FS(), testProjectPath, &projectgen.NewProjectOptions{})
		require.Error(t, err)
	})
}
