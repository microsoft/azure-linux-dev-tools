// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projecttest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/containertest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/require"
)

const (
	projectSubdir = "project"

	// TestDefaultConfigsSubdir is the subdirectory name used for test default configs in the container.
	TestDefaultConfigsSubdir = "testdefaults"

	// TestDefaultConfigsIncludePath is the relative include path to use from a project's azldev.toml
	// to include the test default configs. This path is relative to the project directory.
	TestDefaultConfigsIncludePath = "../" + TestDefaultConfigsSubdir + "/defaults.toml"
)

// TestDefaultConfigsDir returns the absolute path to the scenario/testdata/defaultconfigs directory.
// This function uses runtime.Caller to resolve the path relative to this source file's location.
func TestDefaultConfigsDir() string {
	// Get the path of this source file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get caller information for TestDefaultConfigsDir")
	}

	// Navigate from scenario/internal/projecttest/ up to scenario/testdata/defaultconfigs/
	scenarioDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	return filepath.Join(scenarioDir, "testdata", "defaultconfigs")
}

// ProjectTest represents a runnable, project-oriented test case.
type ProjectTest struct {
	project               TestProject
	commandArgs           []string
	useTestDefaultConfigs bool
}

// ProjectTestOption is a function that can be used to configure a [ProjectTest].
type ProjectTestOption func(*ProjectTest)

// WithTestDefaultConfigs configures the project test to copy and use the test default configs.
// When enabled, the test default configs from scenario/testdata/defaultconfigs/ will be
// copied into the container and made available for the project to include.
func WithTestDefaultConfigs() ProjectTestOption {
	return func(p *ProjectTest) {
		p.useTestDefaultConfigs = true
	}
}

// NewProjectTest creates a new [ProjectTest] with the specified project and command arguments.
func NewProjectTest(project TestProject, commandArgs []string, options ...ProjectTestOption) *ProjectTest {
	params := &ProjectTest{
		project:     project,
		commandArgs: commandArgs,
	}

	for _, option := range options {
		option(params)
	}

	return params
}

// ProjectTestResults encapsulates the results of running a [ProjectTest] in a container.
type ProjectTestResults struct {
	inner       testhelpers.TestResults
	output      []map[string]interface{}
	outputBytes []byte
}

// RunInContainer runs the project test in a container, returning results.
func (p *ProjectTest) RunInContainer(t *testing.T) *ProjectTestResults {
	t.Helper()

	// Serialize the project to a temporary staging directory on the host.
	projectStagingDir := t.TempDir()
	p.project.Serialize(t, projectStagingDir)

	// Create a script that runs the command with the provided arguments.
	testScript := fmt.Sprintf(`
set -x

# Ensure the build output dir is writable by mock; we accomplish this by symlinking
# to the well-known dir created by mock for this purpose.
rm -rf project/build
ln -s /var/lib/mock project/build

azldev -C project -v %s --output-format json >result.json
`, strings.Join(p.commandArgs, " "))

	// NOTE: We need to run in a privileged container so 'mock' can create its nested root environment.
	// NOTE: We need to enable networking so 'mock' can download Azure Linux packages to build a root.
	scenarioTest := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(testScript)).
		AddDirRecursive(t, projectSubdir, projectStagingDir)

	// If test default configs are requested, copy them into the container as a sibling directory
	// to the project directory. This allows the project's azldev.toml to include them via a
	// relative path like "../testdefaults/defaults.toml".
	if p.useTestDefaultConfigs {
		scenarioTest.AddDirRecursive(t, TestDefaultConfigsSubdir, TestDefaultConfigsDir())
	}

	results, err := scenarioTest.
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)

	require.NoError(t, err)

	t.Logf("Standard output:\n%s", results.Stdout)
	t.Logf("Standard error:\n%s", results.Stderr)

	results.AssertZeroExitCode(t)

	// Find the output file.
	outputFilePath := filepath.Join(results.Workdir, "result.json")
	require.FileExists(t, outputFilePath, "Expected output file to exist")

	// Read it.
	outputBytes, err := os.ReadFile(outputFilePath)
	require.NoError(t, err, "Failed to read output file")

	t.Logf("Build output:\n%s", string(outputBytes))

	// Parse it as JSON
	var output []map[string]interface{}

	require.NoError(t, json.Unmarshal(outputBytes, &output))

	return &ProjectTestResults{
		inner:       results,
		output:      output,
		outputBytes: outputBytes,
	}
}

// GetInContainerProjectPath returns the path to the project directory inside the container.
func (r *ProjectTestResults) GetInContainerProjectPath(pathComponents ...string) string {
	pathComponents = append([]string{containertest.ContainerWorkDir, projectSubdir}, pathComponents...)

	return filepath.Join(pathComponents...)
}

// GetProjectOutputPath returns the path to the project output directory on the host.
func (r *ProjectTestResults) GetProjectOutputPath(pathComponents ...string) string {
	pathComponents = append([]string{r.inner.Workdir, projectSubdir}, pathComponents...)

	return filepath.Join(pathComponents...)
}

// GetRawJSONBytes returns the raw JSON bytes of the command-under-test's output.
func (r *ProjectTestResults) GetRawJSONBytes() []byte {
	return r.outputBytes
}

// GetJSONResult returns the parsed JSON output of the command-under-test's output.
func (r *ProjectTestResults) GetJSONResult() []map[string]any {
	return r.output
}
