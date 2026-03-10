// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package project_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/project"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/opctx/opctx_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestOnAppInit(t *testing.T) {
	ctrl := gomock.NewController(t)
	app := azldev.NewApp(opctx_test.NewMockFileSystemFactory(ctrl), opctx_test.NewMockOSEnvFactory(ctrl))

	project.OnAppInit(app)

	// Make sure the project command was added to the app.
	topLevelCommandNames, err := app.CommandNames()
	require.NoError(t, err)

	assert.Contains(t, topLevelCommandNames, "project")
}
