// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"os/exec"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/rpm/mock"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewComponentQueryCommand(t *testing.T) {
	cmd := component.NewComponentQueryCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "query", cmd.Use)
}

func TestComponentQueryCmd_NoMatch(t *testing.T) {
	const testComponentName = "test-component"

	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewComponentQueryCommand()
	cmd.SetArgs([]string{testComponentName})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}

func TestQueryComponents_OneComponent(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "/path/to/spec"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       testSpecPath,
		},
	}

	// Pretend mock is present.
	testEnv.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)

	// Mock the rpmspec command to return valid output
	// NOTE: This takes a dependency on knowing how rpmspec gets invoked.
	testEnv.CmdFactory.RunAndGetOutputHandler = func(cmd *exec.Cmd) (string, error) {
		// Return mock rpmspec output in the expected format: name|epoch|version|release
		return "name=test-component\nepoch=0\nversion=1.0.0\nrelease=1.azl3\n", nil
	}

	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	// Simulate the spec file existing.
	err := fileutils.WriteFile(testEnv.FS(), testSpecPath, []byte("test spec content"), fileperms.PublicFile)
	require.NoError(t, err)

	results, err := component.QueryComponents(testEnv.Env, &options)
	require.NoError(t, err)
	require.Len(t, results, 1)

	result := results[0]
	assert.Equal(t, testComponentName, result.Name)
}
