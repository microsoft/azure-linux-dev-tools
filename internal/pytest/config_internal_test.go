// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package pytest

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestDefinitionToSuiteConfig_Pytest(t *testing.T) {
	resolvedTest := projectconfig.ResolvedTest{
		Name: "static-image-checks",
		Definition: projectconfig.TestDefinition{
			Type:        "pytest",
			Description: "offline validation",
			Pytest:      map[string]any{"working-dir": "/project/tests", "install": "pyproject"},
		},
	}

	suite, err := testDefinitionToSuiteConfig(resolvedTest)
	require.NoError(t, err)
	require.NotNil(t, suite)
	assert.Equal(t, "static-image-checks", suite.Name)
	require.NotNil(t, suite.Pytest)
	assert.Equal(t, "/project/tests", suite.Pytest.WorkingDir)
	assert.Equal(t, projectconfig.PytestInstallPyproject, suite.Pytest.Install)
}

func TestTestDefinitionToSuiteConfig_Pytest_ResolvesRelativeWorkingDir(t *testing.T) {
	// Simulate a test defined in an included config under /project/sub with a
	// relative working-dir. The generated suite must receive the working-dir
	// resolved against that config's directory, not the authored relative value.
	definition := projectconfig.TestDefinition{
		Type:   "pytest",
		Pytest: map[string]any{"working-dir": "tests", "test-paths": []any{"cases/"}},
	}.WithConfigDir("/project/sub")

	resolvedTest := projectconfig.ResolvedTest{Name: "static-image-checks", Definition: definition}

	suite, err := testDefinitionToSuiteConfig(resolvedTest)
	require.NoError(t, err)
	require.NotNil(t, suite.Pytest)
	assert.Equal(t, "/project/sub/tests", suite.Pytest.WorkingDir)
}
