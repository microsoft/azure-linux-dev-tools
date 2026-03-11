// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDiffSourcesCmd(t *testing.T) {
	cmd := component.NewDiffSourcesCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "diff-sources", cmd.Use)

	outputFileFlag := cmd.Flags().Lookup("output-file")
	require.NotNil(t, outputFileFlag, "--output-file flag should be registered")
	assert.Empty(t, outputFileFlag.DefValue)
}

func TestDiffSourcesCmd_NoMatch(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewDiffSourcesCmd()
	cmd.SetArgs([]string{"nonexistent-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}
