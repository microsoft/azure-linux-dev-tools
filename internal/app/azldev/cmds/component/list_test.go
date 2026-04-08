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
	assert.Empty(t, result.RenderedSpecDir, "RenderedSpecDir should be empty when rendered-specs-dir is not configured")
}

func TestListComponents_WithRenderedSpecsDir(t *testing.T) {
	const (
		testComponentName = "vim"
		testSpecPath      = "/path/to/spec"
		testRenderedDir   = "/path/to/repo/specs"
	)

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = testRenderedDir
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
	assert.Equal(t, testRenderedDir+"/"+testComponentName, result.RenderedSpecDir)
}

func TestListComponents_MultipleWithRenderedSpecsDir(t *testing.T) {
	const testRenderedDir = "/rendered/specs"

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = testRenderedDir

	testEnv.Config.Components["curl"] = projectconfig.ComponentConfig{
		Name: "curl",
		Spec: projectconfig.SpecSource{Path: "/specs/curl.spec"},
	}
	testEnv.Config.Components["vim"] = projectconfig.ComponentConfig{
		Name: "vim",
		Spec: projectconfig.SpecSource{Path: "/specs/vim.spec"},
	}

	options := component.ListComponentOptions{
		ComponentFilter: components.ComponentFilter{
			IncludeAllComponents: true,
		},
	}

	results, err := component.ListComponentConfigs(testEnv.Env, &options)
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, result := range results {
		assert.Equal(t, testRenderedDir+"/"+result.Name, result.RenderedSpecDir)
	}
}
