// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package image

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveImageTestsToRun_UsesNewTestsRefs(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "/project/tests"}},
		"functional_core":     {Type: "lisa", Lisa: map[string]any{"criteria": map[string]any{"priority": []any{1}}}},
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

	resolved, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	assert.Equal(t, "static-image-checks", resolved[0].Name)
	assert.Equal(t, "functional_core", resolved[1].Name)
}

func TestResolveImageTestsToRun_NoWarnWhenOnlyNewTestsPresent(t *testing.T) {
	var buf bytes.Buffer

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	t.Cleanup(func() { slog.SetDefault(prev) })

	testEnv := testutils.NewTestEnv(t)
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"static-image-checks": {Type: "pytest", Pytest: map[string]any{"working-dir": "/project/tests"}},
	}

	imageCfg := &projectconfig.ImageConfig{
		Name: "vm-base",
		Tests: &projectconfig.ImageTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "static-image-checks"}},
		},
	}

	_, err := resolveImageTestsToRun(testEnv.Config, imageCfg, nil)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "ignored")
}

func TestResolveLisaFrameworkDir_UsesLisaDirWhenSet(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)
	require.NoError(t, fileutils.MkdirAll(testEnv.Env.FS(), "/checkouts/lisa"))

	options := &ImageTestOptions{LisaDir: "/checkouts/lisa"}

	dir, err := resolveLisaFrameworkDir(testEnv.Env, "my-test", "/work/lisa", nil, options)
	require.NoError(t, err)
	assert.Equal(t, "/checkouts/lisa", dir)
}

func TestResolveLisaFrameworkDir_LisaDirMissingReturnsError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	options := &ImageTestOptions{LisaDir: "/does/not/exist"}

	_, err := resolveLisaFrameworkDir(testEnv.Env, "my-test", "/work/lisa", nil, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestResolveLisaFrameworkDir_NoLisaDirAndNoFrameworkReturnsError(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	options := &ImageTestOptions{}

	_, err := resolveLisaFrameworkDir(testEnv.Env, "my-test", "/work/lisa", nil, options)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--lisa-dir")
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
