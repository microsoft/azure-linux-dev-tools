// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewComponentListCommand(t *testing.T) {
	cmd := component.NewComponentListCommand()
	require.NotNil(t, cmd)
	assert.Equal(t, "list", cmd.Use)
}

func TestComponentListCmd_NoMatch(t *testing.T) {
	const testComponentName = "test-component"

	testEnv := testutils.NewTestEnv(t)

	cmd := component.NewComponentListCommand()
	cmd.SetArgs([]string{testComponentName})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because we haven't set up any components.
	require.Error(t, err)
}

func TestListComponents_OneComponent(t *testing.T) {
	const (
		testComponentName = "test-component"
		testSpecPath      = "/path/to/spec"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Components[testComponentName] = projectconfig.ComponentConfig{
		Name: testComponentName,
		Spec: projectconfig.SpecSource{
			Path: testSpecPath,
		},
	}

	options := component.ListComponentOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	results, err := component.ListComponentConfigs(testEnv.Env, &options)
	require.NoError(t, err)
	require.Len(t, results, 1)

	result := results[0]
	assert.Equal(t, testComponentName, result.Name)
	assert.Equal(t, testSpecPath, result.Spec.Path)
}
