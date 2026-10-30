// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"slices"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestOnAppInit(t *testing.T) {
	ctrl := gomock.NewController(t)
	app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))

	component.OnAppInit(app)

	// Make sure the component command was added to the app.
	topLevelCommandNames, err := app.CommandNames()
	require.NoError(t, err)

	assert.Contains(t, topLevelCommandNames, "component")
}

func TestOnAppInit_CommandsByMode(t *testing.T) {
	testCases := []struct {
		name            string
		args            []string
		refreshExpected bool
	}{
		{name: "default mode", args: nil, refreshExpected: true},
		{name: "lock-file-free mode", args: []string{"--without-lockfile"}, refreshExpected: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))

			app.PreParseGlobalFlags(testCase.args)
			component.OnAppInit(app)

			commandNames, err := app.CommandNames("component")
			require.NoError(t, err)

			assert.Equal(t, testCase.refreshExpected,
				slices.Contains(commandNames, "refresh-upstream-commit"))

			// The lock-file command names stay reachable in lock-file-free mode,
			// where they are registered as hidden no-ops for compatibility.
			for _, name := range []string{"history", "query", "update"} {
				assert.Contains(t, commandNames, name)
			}
		})
	}
}

func TestNewComponentCommands_LockValidationFlagByMode(t *testing.T) {
	// The lock-file consistency flags only mean something when lock files are in
	// play, so lock-file-free mode leaves them unregistered.
	assert.NotNil(t, component.NewBuildCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewRenderCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewComponentListCommand().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewHistoryCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewComponentQueryCommand().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewUpdateCmd().Flags().Lookup("skip-lock-validation"))

	withoutLockfile := component.WithoutLockfileFlags()
	assert.Nil(t, component.NewBuildCmd(withoutLockfile).Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewRenderCmd(withoutLockfile).Flags().Lookup("skip-lock-validation"),
		"render selects lock behavior per component, independently of the global mode")
	assert.Nil(t, component.NewComponentListCommand(withoutLockfile).Flags().Lookup("skip-lock-validation"))
}

func TestLocklessComponents_SkipLegacyHistoryAndQuery(t *testing.T) {
	env := testutils.NewTestEnv(t)
	env.Config.LocklessComponents = map[string]bool{"rust-asyncband": true}
	env.Config.Components["rust-asyncband"] = projectconfig.ComponentConfig{
		Name: "rust-asyncband",
		Spec: projectconfig.SpecSource{SourceType: projectconfig.SpecSourceTypeUpstream},
	}
	filter := components.ComponentFilter{IncludeAllComponents: true}

	history, err := component.ComponentHistory(env.Env, &component.HistoryOptions{
		ComponentFilter: filter,
		SharedTomlMode:  "show",
	})
	require.NoError(t, err)
	assert.Empty(t, history)

	query, err := component.QueryComponents(env.Env, &component.QueryComponentsOptions{ComponentFilter: filter})
	require.NoError(t, err)
	assert.Empty(t, query)
}
