// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
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

func prepareSuccessfulBatchQuery(
	t *testing.T, testEnv *testutils.TestEnv, componentName, specPath string,
) {
	t.Helper()

	const renderedSpecsDir = "/project/specs"

	testEnv.Config.Project.RenderedSpecsDir = renderedSpecsDir

	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), specPath, []byte("test spec content"), fileperms.PublicFile,
	))

	renderedSpecDir, err := components.RenderedSpecDir(renderedSpecsDir, componentName)
	require.NoError(t, err)
	require.NoError(t, fileutils.MkdirAll(testEnv.FS(), renderedSpecDir))
	require.NoError(t, fileutils.WriteFile(
		testEnv.FS(), filepath.Join(renderedSpecDir, componentName+".spec"),
		[]byte("test rendered spec content"), fileperms.PublicFile,
	))

	type queryResult struct {
		Name    string  `json:"name"`
		SrpmOut string  `json:"srpmOut"`
		BinOut  string  `json:"binOut"`
		Error   *string `json:"error"`
	}

	resultJSON, err := json.Marshal([]queryResult{{
		Name:    componentName,
		SrpmOut: "name=" + componentName + "\nepoch=0\nversion=1.0.0\nrelease=1.azl4\n",
	}})
	require.NoError(t, err)

	testEnv.CmdFactory.RegisterCommandInSearchPath(mock.MockBinary)
	testEnv.CmdFactory.RunHandler = func(cmd *exec.Cmd) error {
		if !slices.Contains(cmd.Args, "--chroot") {
			return nil
		}

		scratchDirs, globErr := fileutils.Glob(
			testEnv.FS(), filepath.Join(testEnv.Env.WorkDir(), "azldev-query-scratch-*"),
		)
		if globErr != nil {
			return globErr
		}

		if len(scratchDirs) != 1 {
			return fmt.Errorf("expected one query scratch directory, found %d", len(scratchDirs))
		}

		return fileutils.WriteFile(
			testEnv.FS(), filepath.Join(scratchDirs[0], "results.json"), resultJSON, fileperms.PublicFile,
		)
	}
}

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

func TestQueryComponents_ResolvesComponentTests(t *testing.T) {
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
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Group: "runtime"}},
		},
	}
	testEnv.Config.Tests = map[string]projectconfig.TestDefinition{
		"runtime-a": {Type: "pytest", Pytest: map[string]any{"test-paths": []string{"tests/a.py"}}},
		"runtime-b": {Type: "pytest", Pytest: map[string]any{"test-paths": []string{"tests/b.py"}}},
	}
	testEnv.Config.TestGroups = map[string]projectconfig.TestGroup{
		"runtime": {Tests: []projectconfig.TestRef{{Name: "runtime-a"}, {Name: "runtime-b"}}},
	}

	prepareSuccessfulBatchQuery(t, testEnv, testComponentName, testSpecPath)

	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	results, err := component.QueryComponents(testEnv.Env, &options)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []string{"runtime-a", "runtime-b"}, results[0].ResolvedTests)
}

func TestQueryComponents_InvalidComponentTestRef(t *testing.T) {
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
		Tests: &projectconfig.ComponentTestsConfig{
			Tests: []projectconfig.TestRef{{Name: "missing-test"}},
		},
	}

	prepareSuccessfulBatchQuery(t, testEnv, testComponentName, testSpecPath)

	options := component.QueryComponentsOptions{
		ComponentFilter: components.ComponentFilter{
			ComponentNamePatterns: []string{testComponentName},
		},
	}

	_, err := component.QueryComponents(testEnv.Env, &options)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to resolve tests for component")
	require.ErrorContains(t, err, "missing-test")
}
