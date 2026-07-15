// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveImageTestsToRun_UsesNewTestsRefs(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "/project/tests"}},
		"functional_core":    {Type: "lisa", Lisa: map[string]any{"criteria": map[string]any{"priority": []any{1}}}},
	}
	testEnv.Config.TestGroups = map[string]projectconfig.TestGroup{
		"vm-base-functional": {Tests: []projectconfig.TestRef{{Name: "functional_core"}}},
	}

	imageCfg := &projectconfig.ImageConfig{
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{
				{Name: "static-image-checks"},
				{Group: "vm-base-functional"},
			},
		},
	}

	resolved, legacy, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	assert.Empty(t, legacy)
	require.Len(t, resolved, 2)
	assert.Equal(t, "static-image-checks", resolved[0].Name)
	assert.Equal(t, "functional_core", resolved[1].Name)
}

func TestResolveImageTestsToRun_FallsBackToLegacyTestSuites(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	imageCfg := &projectconfig.ImageConfig{
		Tests: &projectconfig.ImageTestsConfig{
			TestSuites: []projectconfig.TestSuiteRef{{Name: "smoke"}, {Name: "integration"}},
		},
	}

	resolved, legacy, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	assert.Empty(t, resolved)
	assert.Equal(t, []string{"smoke", "integration"}, legacy)
}

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
