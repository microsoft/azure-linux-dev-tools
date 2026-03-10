// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package buildtest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	rpmlib "github.com/cavaliergopher/rpm"
	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

// BuildTest is a test that builds a component in an azldev project.
type BuildTest struct {
	inner *projecttest.ProjectTest
}

// BuildTestResults encapsulates the results of running a [BuildTest].
type BuildTestResults struct {
	inner *projecttest.ProjectTestResults
}

// NewBuildTest creates a new [BuildTest] for the specified project and component name.
// Optional [projecttest.ProjectTestOption]s can be passed to configure the underlying project test,
// such as [projecttest.WithTestDefaultConfigs] to include the test default configs.
func NewBuildTest(
	project projecttest.TestProject,
	componentName string,
	options ...projecttest.ProjectTestOption,
) *BuildTest {
	projectTest := projecttest.NewProjectTest(project, []string{"component", "build", componentName}, options...)

	return &BuildTest{
		inner: projectTest,
	}
}

// Run runs the build test, returning results.
func (p *BuildTest) Run(t *testing.T) *BuildTestResults {
	t.Helper()

	results := p.inner.RunInContainer(t)

	return &BuildTestResults{
		inner: results,
	}
}

// GetBuildOutputs returns the build outputs from the test results.
func (r *BuildTestResults) GetBuildOutputs(t *testing.T) []componentcmds.ComponentBuildResults {
	t.Helper()

	var output []componentcmds.ComponentBuildResults

	require.NoError(t, json.Unmarshal(r.inner.GetRawJSONBytes(), &output))

	// The paths we find in the deserialized JSON output are relative to the container's environment.
	// We convert the paths to host paths so our caller can make sense of them and use them.
	for outputIndex := range output {
		output[outputIndex].RPMPaths = r.containerPathsToHostPaths(output[outputIndex].RPMPaths)
		output[outputIndex].SRPMPaths = r.containerPathsToHostPaths(output[outputIndex].SRPMPaths)
	}

	return output
}

func (r *BuildTestResults) containerPathsToHostPaths(paths []string) []string {
	return lo.Map(paths, func(path string, _ int) string {
		return r.containerPathToHostPath(path)
	})
}

func (r *BuildTestResults) containerPathToHostPath(path string) string {
	return strings.ReplaceAll(path, r.inner.GetInContainerProjectPath(), r.inner.GetProjectOutputPath())
}

// GetSRPMs returns the SRPMs produced by the build test.
func (r *BuildTestResults) GetSRPMs(t *testing.T) []*rpmlib.Package {
	t.Helper()

	outputs := r.GetBuildOutputs(t)

	return lo.FlatMap(outputs, func(result componentcmds.ComponentBuildResults, _ int) []*rpmlib.Package {
		return lo.Map(result.SRPMPaths, func(path string, _ int) *rpmlib.Package {
			return openRPM(t, path)
		})
	})
}

// GetRPMs returns the RPMs produced by the build test.
func (r *BuildTestResults) GetRPMs(t *testing.T) []*rpmlib.Package {
	t.Helper()

	outputs := r.GetBuildOutputs(t)

	return lo.FlatMap(outputs, func(result componentcmds.ComponentBuildResults, _ int) []*rpmlib.Package {
		return lo.Map(result.RPMPaths, func(path string, _ int) *rpmlib.Package {
			return openRPM(t, path)
		})
	})
}

func openRPM(t *testing.T, rpmPath string) *rpmlib.Package {
	t.Helper()

	rpmFile, err := os.Open(rpmPath)
	require.NoError(t, err)
	require.NotNil(t, rpmFile)

	defer rpmFile.Close()

	pkg, err := rpmlib.Read(rpmFile)
	require.NoError(t, err)

	return pkg
}
