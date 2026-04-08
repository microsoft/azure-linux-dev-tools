// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package advanced_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/advanced"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCTToolsCmd(t *testing.T) {
	cmd := advanced.NewCTToolsCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "ct-tools", cmd.Use)
}

func TestNewConfigDumpCmd(t *testing.T) {
	cmd := advanced.NewConfigDumpCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "config-dump", cmd.Use)
}

func TestCTToolsCmd_HasConfigDumpSubcommand(t *testing.T) {
	cmd := advanced.NewCTToolsCmd()

	subCmds := cmd.Commands()
	found := false

	for _, sub := range subCmds {
		if sub.Use == "config-dump" {
			found = true

			break
		}
	}

	assert.True(t, found, "ct-tools should have config-dump subcommand")
}
