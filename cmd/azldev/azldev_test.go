// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main_test

import (
	"testing"

	main "github.com/microsoft/azure-linux-dev-tools/cmd/azldev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstantiateApp(t *testing.T) {
	app := main.InstantiateApp()
	if assert.NotNil(t, app) {
		topLevelCommandNames, err := app.CommandNames()
		require.NoError(t, err)

		// Make sure we have the expected set of top-level commands.
		assert.ElementsMatch(
			t,
			topLevelCommandNames,
			[]string{
				"docs",
				"version",
			},
		)
	}
}
