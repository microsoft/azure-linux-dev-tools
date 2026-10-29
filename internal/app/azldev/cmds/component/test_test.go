// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTMTComponentName = "hostname"
	testImagePath        = "/project/image.qcow2"
	testRPMPath          = "/project/component.rpm"
)

func newTMTTestEnv(t *testing.T, tests *projectconfig.ComponentTestsConfig) *testutils.TestEnv {
	t.Helper()

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testTMTComponentName] = projectconfig.ComponentConfig{
		Name: testTMTComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       "/project/" + testTMTComponentName + ".spec",
		},
		Tests: tests,
	}

	return testEnv
}

func TestNewComponentTestCmd(t *testing.T) {
	cmd := component.NewComponentTestCmd()

	require.NotNil(t, cmd)
	assert.Equal(t, "test COMPONENT", cmd.Use)
	assert.NotNil(t, cmd.RunE)

	for _, name := range []string{
		"image-path", "rpm", "test", "work-dir", "provision", "source-dir", "plan", "from-spec",
	} {
		assert.NotNil(t, cmd.Flags().Lookup(name), "%s flag should be registered", name)
	}

	fromSpec := cmd.Flags().Lookup("from-spec")
	require.NotNil(t, fromSpec)
	assert.NotEmpty(t, fromSpec.Deprecated, "--from-spec should be marked deprecated")

	// Removed flags: memory, firmware, connection, user
	for _, name := range []string{"memory", "firmware", "connection", "user"} {
		assert.Nil(t, cmd.Flags().Lookup(name), "%s flag should not be registered", name)
	}
}

func TestComponentTestCmdNoMatch(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := component.NewComponentTestCmd()
	cmd.SetArgs([]string{"missing-component", "--image-path", testImagePath, "--rpm", testRPMPath})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

// TestComponentTestCmdPreflightsBeforePreparingEnvironment proves the fix for
// the deterministic-input-error-after-side-effects issue: a catalog test
// missing 'tmt.source' (and no --source-dir) must fail before azldev creates a
// venv or runs pip install, not partway through runOneTMTTest.
func TestComponentTestCmdPreflightsBeforePreparingEnvironment(t *testing.T) {
	testEnv := newTMTTestEnv(t, &projectconfig.ComponentTestsConfig{
		Tests: []projectconfig.TestRef{{Name: "ci"}},
	})
	testEnv.Config.Tests["ci"] = projectconfig.TestDefinition{Type: "tmt", Tmt: map[string]any{}}

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := component.NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath, "--plan", "/plans/all",
	})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "'tmt.source'")
	assert.Empty(t, testEnv.CommandsExecuted, "no venv/pip commands should run before the preflight check")
}

// TestComponentTestCmdRejectsOverlappingSourceAndWorkDir covers the scenario
// from the review finding: '--source-dir .' from the project root with the
// default (cwd) work directory makes the managed per-test directory a child
// of the supplied source tree.
func TestComponentTestCmdRejectsOverlappingSourceAndWorkDir(t *testing.T) {
	testEnv := newTMTTestEnv(t, nil)
	require.NoError(t, fileutils.WriteFile(
		testEnv.TestFS, filepath.Join("/project", ".fmf", "version"), []byte("1\n"), fileperms.PrivateFile,
	))

	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testImagePath, []byte("image"), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(testEnv.TestFS, testRPMPath, []byte("rpm"), fileperms.PrivateFile))

	cmd := component.NewComponentTestCmd()
	cmd.SetArgs([]string{
		testTMTComponentName, "--image-path", testImagePath, "--rpm", testRPMPath,
		"--plan", "/plans/all", "--source-dir", ".",
	})

	err := cmd.ExecuteContext(testEnv.Env)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not overlap")
	assert.Empty(t, testEnv.CommandsExecuted, "no venv/pip commands should run before the overlap check")
}
