// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocklessComponents_NoGlobalFlag(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	results, err := cmdtest.NewScenarioTest("--help").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)
	assert.NotContains(t, results.Stdout, "--without-lockfile")
}

func TestLocklessComponents_BothWorkflowsAvailable(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	results, err := cmdtest.NewScenarioTest("component", "--help").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)
	for _, name := range []string{"update", "history", "query", "refresh-upstream-commit"} {
		assert.Contains(t, results.Stdout, name)
	}
}

func TestLocklessComponents_LockValidationFlagAvailable(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	defaultResults, err := cmdtest.NewScenarioTest("component", "build", "--help").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, defaultResults.ExitCode)
	assert.Contains(t, defaultResults.Stdout, "--skip-lock-validation")

}

func TestLocklessComponents_AgentSkillsCoverBothWorkflows(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	defaultResults, err := cmdtest.NewScenarioTest("docs", "agent", "show").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, defaultResults.ExitCode)
	assert.Contains(t, defaultResults.Stdout, "azldev-update-component")
	assert.Contains(t, defaultResults.Stdout, "azldev-refresh-upstream-commit")

}

func TestLocklessComponents_ComponentChanged(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, err := testhelpers.FindTestBinary()
	require.NoError(t, err)

	projectDir := t.TempDir()

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(projecttest.NewSpec(
			projecttest.WithName("curl"),
			projecttest.WithVersion("8.0.0"),
			projecttest.WithRelease("1%{?dist}"),
			projecttest.WithBuildArch(projecttest.NoArch),
		)),
		projecttest.AddSpec(projecttest.NewSpec(
			projecttest.WithName("bash"),
			projecttest.WithVersion("5.2.0"),
			projecttest.WithRelease("1%{?dist}"),
			projecttest.WithBuildArch(projecttest.NoArch),
		)),
		projecttest.AddComponent(&projectconfig.ComponentConfig{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}),
		projecttest.AddComponent(&projectconfig.ComponentConfig{
			Name: "bash",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "bash", "bash.spec"),
			},
		}),
		projecttest.AddFile("distro.toml", minimalDistroTOML),
	)

	project.Serialize(t, projectDir)
	patchProjectForLocal(t, projectDir)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "lockless-components.txt"),
		[]byte("curl\n"), fileperms.PublicFile))

	gitInDir(t, projectDir, "init")
	gitInDir(t, projectDir, "config", "user.email", "test@test.com")
	gitInDir(t, projectDir, "config", "user.name", "Test")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")

	fromRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Change only curl's spec content.
	curlSpecPath := filepath.Join(projectDir, "specs", "curl", "curl.spec")
	curlSpec, err := os.ReadFile(curlSpecPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		curlSpecPath,
		append(curlSpec, []byte("\n# changed by the scenario test\n")...),
		fileperms.PublicFile,
	))

	gitInDir(t, projectDir, "add", filepath.Join("specs", "curl", "curl.spec"))
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "change curl")

	args := []string{
		"-C", projectDir, "--no-default-config",
		"component", "changed", "--from", fromRef, "-a", "--include-unchanged", "-q", "-O", "json",
	}
	cmd := exec.CommandContext(t.Context(), azldevBin, args...)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(out))

	var results []changedResult
	require.NoError(t, json.Unmarshal(out, &results), "failed to parse JSON: %s", string(out))

	resultMap := make(map[string]changedResult, len(results))
	for _, result := range results {
		resultMap[result.Component] = result
	}

	curlResult, ok := resultMap["curl"]
	require.True(t, ok, "curl should be in results")
	assert.Equal(t, "changed", curlResult.ChangeType, "curl spec contents changed")

	bashResult, ok := resultMap["bash"]
	require.True(t, ok, "bash should be in results (--all-components)")
	assert.Equal(t, "unchanged", bashResult.ChangeType, "bash is untouched")

	// Change detection does not create lock files.
	entries, err := os.ReadDir(projectDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.NotEqual(t, "locks", entry.Name(), "change detection must not create a lock directory")
		assert.False(t, strings.HasSuffix(entry.Name(), ".lock"))
	}
}

func TestLocklessComponents_MCPTools(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	const listToolsRequest = `{"jsonrpc":"2.0", "method": "tools/list", "id": 1}` + "\n"

	defaultResults, err := cmdtest.NewScenarioTest("advanced", "mcp").
		Locally().WithStdin(strings.NewReader(listToolsRequest)).Run(t)
	require.NoError(t, err)
	require.Zero(t, defaultResults.ExitCode)
	assert.Contains(t, defaultResults.Stdout, `"component-history"`)

	assert.Contains(t, defaultResults.Stdout, `"component-changed"`)
}
