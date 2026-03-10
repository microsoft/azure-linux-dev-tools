// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/snapshot"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/require"
)

// Tests basic snapshottable commands.
func TestSnapshots(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	tests := map[string]testhelpers.ScenarioTest{
		"help":                   cmdtest.NewScenarioTest("help").Locally(),
		"config generate-schema": cmdtest.NewScenarioTest("config", "generate-schema").Locally(),
		"--help":                 cmdtest.NewScenarioTest("--help").Locally(),
		"--help with color":      cmdtest.NewScenarioTest("--help", "--color=always").Locally(),
		"--bogus-flag":           cmdtest.NewScenarioTest("--bogus-flag").Locally(),
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			snapshot.TestSnapshottableCmd(t, test)
		})
	}
}

// Tests basic snapshottable commands also work as expected in a container.
func TestSnapshotsContainer(t *testing.T) {
	t.Parallel()
	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	tests := map[string]testhelpers.ScenarioTest{
		"help":                   cmdtest.NewScenarioTest("help").InContainer(),
		"config generate-schema": cmdtest.NewScenarioTest("config", "generate-schema").InContainer(),
		"--help":                 cmdtest.NewScenarioTest("--help").InContainer(),
		"--bogus-flag":           cmdtest.NewScenarioTest("--bogus-flag").InContainer(),
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			snapshot.TestSnapshottableCmd(t, test)
		})
	}
}

// Tests that azldev's basic commands are still usable when there is invalid config present.
func TestAzlDevWithInvalidConfig(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create any old invalid config that we know won't be valid.
	const someInvalidConfig = "invalid{"

	// Write any invalid config to the azldev.toml file and make sure "version" still works: we
	// should still get a zero exit code and a version string emitted.
	test := cmdtest.NewScenarioTest("version").AddFileContents("azldev.toml", strings.NewReader(someInvalidConfig)).InContainer()
	results, err := test.Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)
	require.Contains(t, results.Stdout, "Version")

	// Now try to run a command that requires a valid config. We should get a non-zero exit code.
	test = cmdtest.NewScenarioTest(
		"component", "list", "--all-components",
	).AddFileContents("azldev.toml", strings.NewReader(someInvalidConfig)).InContainer()

	snapshot.TestSnapshottableCmd(t, test)
}

// We test `azldev version` in a separate test because its output varies based on environmental
// conditions (e.g., build, date, platform); we don't snapshot its output, but instead check for
// a known substring.
func TestAzlDevVersion(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	test := cmdtest.NewScenarioTest("version").Locally()

	// Run and make sure it exits with 0.
	results, err := test.Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)

	require.Contains(t, results.Stdout, "Version")
}

// Test that `azldev docs md` generates real markdown docs.
func TestAzlDevDocsMd(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	outputDir := t.TempDir()

	test := cmdtest.NewScenarioTest("docs", "markdown", "--output-dir", outputDir).Locally()

	results, err := test.Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)

	// Make sure the output directory was created and contains azldev.md (at minimum).
	expectedPath := filepath.Join(outputDir, "azldev.md")
	require.FileExists(t, expectedPath, "Expected markdown file")
}

func TestAzlDevConfigDump(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	config := `
'$schema' = 'https://raw.githubusercontent.com/microsoft/azure-linux-dev-tools/refs/heads/main/schemas/azldev.schema.json'

[project]
description = 'simple'
log-dir = 'build/logs'
work-dir = 'build/work'
output-dir = 'out'

[component-groups]
[component-groups.default]
specs = ['**/*.spec']
excluded-paths = ['build/**', 'out/**']
`

	// Since default config may change, we need to exclude it from the snapshot.
	test := cmdtest.NewScenarioTest("config", "dump", "--no-default-config").AddFileContents("azldev.toml", strings.NewReader(config)).InContainer()

	snapshot.TestSnapshottableCmd(t, test)
}
