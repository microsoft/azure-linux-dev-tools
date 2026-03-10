// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/snapshot"
	"github.com/stretchr/testify/require"
)

// Tests basic snapshottable commands.
func TestMCPServerMode(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// We test the MCP server mode by sending a JSON-RPC request to list tools.
	// This doesn't test any specific tools, but makes sure that the MCP
	// server is at least up and running.
	input := `{"jsonrpc":"2.0", "method": "tools/list", "id": 1}` + "\n"
	inputReader := strings.NewReader(input)

	// Run the test and make sure it exited successfully.
	test := cmdtest.NewScenarioTest("advanced", "mcp").Locally().WithStdin(inputReader)
	testResults, err := test.Run(t)
	require.NoError(t, err)
	require.Zero(t, testResults.ExitCode, "Expected exit code to be zero")

	// Snapshot-validate the JSON output from the request.
	snapshotConfig := snapshot.NewConfig(t)
	snapshotConfig.MatchStandaloneJSON(t, testResults.Stdout)
}
