// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
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

func TestQueryComponents_MissingRenderedSpecsDir(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	// Test env constructProjectConfig leaves RenderedSpecsDir empty.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"any"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendered-specs-dir is not configured")
}

func TestQueryComponents_RenderedSpecsDirDoesNotExist(t *testing.T) {
	const renderedSpecsDir = "/project/specs"

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	// Do NOT create the directory on the test filesystem.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"any"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

// Smoke test: when filter matches no components, the resolver surfaces an
// error before any rendered-spec validation runs.
func TestQueryComponents_NoComponentsSelected(t *testing.T) {
	const renderedSpecsDir = "/project/specs"

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedSpecsDir))

	// No components configured at all.
	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{"nonexistent"},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
}
