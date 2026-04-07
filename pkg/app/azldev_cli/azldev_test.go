// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package azldev_cli_test

import (
	"testing"

	azldev_cli "github.com/microsoft/azure-linux-dev-tools/pkg/app/azldev_cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstantiateApp(t *testing.T) {
	app := azldev_cli.InstantiateApp()
	if assert.NotNil(t, app) {
		topLevelCommandNames, err := app.CommandNames()
		require.NoError(t, err)

		// Make sure we have the expected set of top-level commands.
		assert.ElementsMatch(
			t,
			topLevelCommandNames,
			[]string{
				"advanced",
				"component",
				"config",
				"docs",
				"image",
				"package",
				"project",
				"version",
			},
		)
	}
}
