// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
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
		name string
		args []string
	}{
		{name: "default mode", args: nil},
		{name: "lock-file-free mode", args: []string{"--without-lockfile"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))

			app.PreParseGlobalFlags(testCase.args)
			component.OnAppInit(app)

			commandNames, err := app.CommandNames("component")
			require.NoError(t, err)

			assert.Contains(t, commandNames, "refresh-upstream-commit")

			// The lock-file command names stay reachable in lock-file-free mode,
			// where they are registered as hidden no-ops for compatibility.
			for _, name := range []string{"history", "query", "update"} {
				assert.Contains(t, commandNames, name)
			}
		})
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
