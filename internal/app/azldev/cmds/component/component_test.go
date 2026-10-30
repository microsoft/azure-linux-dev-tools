// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
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

func TestOnAppInit_RegistersBothWorkflows(t *testing.T) {
	ctrl := gomock.NewController(t)
	app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))
	component.OnAppInit(app)

	commandNames, err := app.CommandNames("component")
	require.NoError(t, err)

	for _, name := range []string{"history", "query", "update", "refresh-upstream-commit"} {
		assert.Contains(t, commandNames, name)
	}
}

func TestNewComponentCommands_LockValidationFlags(t *testing.T) {
	assert.NotNil(t, component.NewBuildCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewRenderCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewComponentListCommand().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewHistoryCmd().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewComponentQueryCommand().Flags().Lookup("skip-lock-validation"))
	assert.NotNil(t, component.NewUpdateCmd().Flags().Lookup("skip-lock-validation"))
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
