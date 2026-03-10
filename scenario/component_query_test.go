// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// We test running `azldev query component` to make sure that spec parsing works as expected.
func TestQueryingAComponent(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a simple spec with a known name and version.
	spec := projecttest.NewSpec(
		projecttest.WithName("test-component"),
		projecttest.WithVersion("3.1.4.159"),
	)

	// Create a simple project with the spec, using test default configs for distro and mock configurations.
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.UseTestDefaultConfigs(),
	)

	// Run the component query command with test default configs copied into the container.
	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "query", spec.GetName()},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Get the parsed JSON output.
	output := results.GetJSONResult()

	// There should just be one component in the output.
	require.Len(t, output, 1, "Expected one component in the output")
	componentOutput := output[0]

	// Check for name.
	require.Contains(t, componentOutput, "Name")
	assert.Equal(t, spec.GetName(), componentOutput["Name"], "Expected component name to match")

	// Check for EVR structure.
	require.Contains(t, componentOutput, "Version")
	version := componentOutput["Version"]

	// Check for version sub-field.
	versionMap, ok := version.(map[string]interface{})
	require.True(t, ok, "Version field is not a map")
	require.Contains(t, versionMap, "Version")
	assert.Equal(t, spec.GetVersion(), versionMap["Version"])
}
