// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/require"
)

// We test running `azldev advanced mock shell` to make sure the default configuration is
// enough to run with it.
func TestMockShellWithDefaultConfig(t *testing.T) {
	t.Parallel()

	// Skip unless doing long tests
	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Create a minimal project with test default configs for distro and mock configurations.
	project := projecttest.NewDynamicTestProject(
		projecttest.UseTestDefaultConfigs(),
	)

	// Serialize the project to a staging directory.
	projectStagingDir := t.TempDir()
	project.Serialize(t, projectStagingDir)

	// Run the mock shell command. The 'whoami' command should report back 'root',
	// which will tell us it made it into the root environment.
	results, err := cmdtest.NewScenarioTest().
		WithArgs("-C", "project", "-v", "advanced", "mock", "shell", "whoami").
		AddDirRecursive(t, "project", projectStagingDir).
		AddDirRecursive(t, projecttest.TestDefaultConfigsSubdir, projecttest.TestDefaultConfigsDir()).
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)

	require.NoError(t, err)
	results.AssertZeroExitCode(t)
	results.AssertStdoutContains(t, "root")
}
