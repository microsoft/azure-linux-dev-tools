// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/snapshot"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComponentIdentitySnapshots tests basic CLI output snapshots for the identity commands.
func TestComponentIdentitySnapshots(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	tests := map[string]testhelpers.ScenarioTest{
		"identity help":      cmdtest.NewScenarioTest("component", "identity", "--help").Locally(),
		"diff-identity help": cmdtest.NewScenarioTest("component", "diff-identity", "--help").Locally(),
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			snapshot.TestSnapshottableCmd(t, test)
		})
	}
}

// TestComponentIdentityInContainer runs the full identity pipeline in a container:
// creates a project with two components, computes identity, modifies one component,
// recomputes identity, and diffs the two.
func TestComponentIdentityInContainer(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create two specs for the project.
	specA := projecttest.NewSpec(
		projecttest.WithName("component-a"),
		projecttest.WithVersion("1.0.0"),
	)
	specB := projecttest.NewSpec(
		projecttest.WithName("component-b"),
		projecttest.WithVersion("2.0.0"),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(specA),
		projecttest.AddSpec(specB),
		projecttest.UseTestDefaultConfigs(),
	)

	// Script that:
	// 1. Computes identity for all components → base.json
	// 2. Modifies component-a's spec file (changes version)
	// 3. Recomputes identity → head.json
	// 4. Diffs the two → diff.json
	testScript := `
set -ex

rm -rf project/build
ln -s /var/lib/mock project/build

# Compute base identity
azldev -C project -v component identity -a --output-format json > base.json

# Modify component-a's spec (change version)
sed -i 's/Version: 1.0.0/Version: 1.1.0/' project/specs/component-a/component-a.spec

# Compute head identity
azldev -C project -v component identity -a --output-format json > head.json

# Diff the two
azldev -v component diff-identity base.json head.json --output-format json > diff.json
`

	scenarioTest := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(testScript))

	// Serialize the project and add it to the container.
	projectStagingDir := t.TempDir()
	project.Serialize(t, projectStagingDir)
	scenarioTest.AddDirRecursive(t, "project", projectStagingDir)

	// Add test default configs.
	scenarioTest.AddDirRecursive(t, projecttest.TestDefaultConfigsSubdir, projecttest.TestDefaultConfigsDir())

	results, err := scenarioTest.
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)

	require.NoError(t, err)
	results.AssertZeroExitCode(t)

	t.Logf("stdout:\n%s", results.Stdout)
	t.Logf("stderr:\n%s", results.Stderr)

	// Parse base identity.
	baseBytes, err := os.ReadFile(filepath.Join(results.Workdir, "base.json"))
	require.NoError(t, err, "base.json should exist")

	var baseIdentities []map[string]interface{}
	require.NoError(t, json.Unmarshal(baseBytes, &baseIdentities))
	require.Len(t, baseIdentities, 2, "should have 2 components in base identity")

	// Parse head identity.
	headBytes, err := os.ReadFile(filepath.Join(results.Workdir, "head.json"))
	require.NoError(t, err, "head.json should exist")

	var headIdentities []map[string]interface{}
	require.NoError(t, json.Unmarshal(headBytes, &headIdentities))
	require.Len(t, headIdentities, 2, "should have 2 components in head identity")

	// Verify fingerprints differ for the modified component.
	baseFPs := identityMap(baseIdentities)
	headFPs := identityMap(headIdentities)

	assert.NotEqual(t, baseFPs["component-a"], headFPs["component-a"],
		"component-a fingerprint should change after spec modification")
	assert.Equal(t, baseFPs["component-b"], headFPs["component-b"],
		"component-b fingerprint should NOT change")

	// Parse and validate the diff output.
	diffBytes, err := os.ReadFile(filepath.Join(results.Workdir, "diff.json"))
	require.NoError(t, err, "diff.json should exist")

	var diffReport map[string][]string
	require.NoError(t, json.Unmarshal(diffBytes, &diffReport))

	assert.Contains(t, diffReport["changed"], "component-a",
		"diff should report component-a as changed")
	assert.Contains(t, diffReport["unchanged"], "component-b",
		"diff should report component-b as unchanged")
	assert.Empty(t, diffReport["added"], "no components should be added")
	assert.Empty(t, diffReport["removed"], "no components should be removed")
}

// identityMap converts the JSON identity array to a map of component name → fingerprint.
func identityMap(identities []map[string]interface{}) map[string]string {
	result := make(map[string]string, len(identities))

	for _, entry := range identities {
		name, _ := entry["component"].(string)
		fingerprint, _ := entry["fingerprint"].(string)
		result[name] = fingerprint
	}

	return result
}
