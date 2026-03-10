// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestApp(t *testing.T) *azldev.App {
	t.Helper()

	testEnv := testutils.NewTestEnv(t)

	return azldev.NewApp(testEnv.TestInterfaces.FileSystemFactory, testEnv.TestInterfaces.OSEnvFactory)
}

func TestNewApp(t *testing.T) {
	app := createTestApp(t)

	// An empty app should not contain any top-level commands by default.
	// Commands are registered via OnAppInit functions in the main application.
	if assert.NotNil(t, app) {
		topLevelCommandNames, err := app.CommandNames()
		require.NoError(t, err)

		assert.Empty(t, topLevelCommandNames)
	}
}

func TestApp_AddTopLevelCommand(t *testing.T) {
	app := createTestApp(t)
	cmd := &cobra.Command{Use: "test-cmd"}

	app.AddTopLevelCommand(cmd)

	topLevelCommandNames, err := app.CommandNames()
	require.NoError(t, err)

	assert.Contains(t, topLevelCommandNames, "test-cmd")
}

func TestApp_Execute(t *testing.T) {
	app := createTestApp(t)

	ran := false
	cmd := &cobra.Command{
		Use: "test-cmd",
		RunE: func(cmd *cobra.Command, args []string) error {
			ran = true

			return nil
		},
	}

	app.AddTopLevelCommand(cmd)

	result := app.Execute([]string{"test-cmd"})
	assert.Zero(t, result)
	assert.True(t, ran)
}

func TestApp_RegisterPostInitCallback(t *testing.T) {
	app := createTestApp(t)

	callbackCalled := false
	callback := func(app *azldev.App, env *azldev.Env) error {
		callbackCalled = true

		return nil
	}

	app.RegisterPostInitCallback(callback)

	result := app.Execute([]string{})
	if assert.Zero(t, result) {
		assert.True(t, callbackCalled)
	}
}

func TestApp_VerboseShortOption(t *testing.T) {
	callbackCalled := false
	app := createTestApp(t)
	app.RegisterPostInitCallback(func(app *azldev.App, env *azldev.Env) error {
		assert.True(t, env.Verbose())

		callbackCalled = true

		return nil
	})

	app.Execute([]string{"-v"})
	assert.True(t, callbackCalled)
}

func TestApp_VerboseLongOption(t *testing.T) {
	callbackCalled := false
	app := createTestApp(t)
	app.RegisterPostInitCallback(func(app *azldev.App, env *azldev.Env) error {
		assert.True(t, env.Verbose())

		callbackCalled = true

		return nil
	})

	app.Execute([]string{"--verbose"})
	assert.True(t, callbackCalled)
}

func TestApp_QuietShortOption(t *testing.T) {
	callbackCalled := false
	app := createTestApp(t)
	app.RegisterPostInitCallback(func(app *azldev.App, env *azldev.Env) error {
		assert.True(t, env.Quiet())

		callbackCalled = true

		return nil
	})

	app.Execute([]string{"-q"})
	assert.True(t, callbackCalled)
}

func TestApp_QuietLongOption(t *testing.T) {
	callbackCalled := false
	app := createTestApp(t)
	app.RegisterPostInitCallback(func(app *azldev.App, env *azldev.Env) error {
		assert.True(t, env.Quiet())

		callbackCalled = true

		return nil
	})

	app.Execute([]string{"--quiet"})
	assert.True(t, callbackCalled)
}

func TestApp_PermissiveConfigOption(t *testing.T) {
	app := createTestApp(t)

	ran := false
	cmd := &cobra.Command{
		Use: "test-cmd",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := azldev.GetEnvFromCommand(cmd)
			require.NoError(t, err)

			assert.True(t, env.PermissiveConfigParsing())

			ran = true

			return nil
		},
	}

	app.AddTopLevelCommand(cmd)

	result := app.Execute([]string{"--permissive-config", "test-cmd"})
	assert.Zero(t, result)
	assert.True(t, ran)
}

func TestApp_PermissiveConfigOption_DefaultFalse(t *testing.T) {
	app := createTestApp(t)

	ran := false
	cmd := &cobra.Command{
		Use: "test-cmd",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := azldev.GetEnvFromCommand(cmd)
			require.NoError(t, err)

			assert.False(t, env.PermissiveConfigParsing())

			ran = true

			return nil
		},
	}

	app.AddTopLevelCommand(cmd)

	result := app.Execute([]string{"test-cmd"})
	assert.Zero(t, result)
	assert.True(t, ran)
}
