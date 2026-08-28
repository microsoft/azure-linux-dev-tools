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

// These tests cover the opt-in lock-file-free mode selected by the global
// '--without-lockfile' flag, and assert that omitting the flag keeps azldev's
// default lock-file behavior.

// TestWithoutLockfile_GlobalFlagIsDocumented verifies that the preview flag is part
// of the CLI surface and defaults to off.
func TestWithoutLockfile_GlobalFlagIsDocumented(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	results, err := cmdtest.NewScenarioTest("--help").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)
	assert.Contains(t, results.Stdout, "--without-lockfile")
}

// TestWithoutLockfile_ComponentCommandsByMode verifies that the component command
// set matches the selected mode: the lock-file commands by default, and the
// refresh-upstream-commit command when the preview flag is passed.
func TestWithoutLockfile_ComponentCommandsByMode(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	testCases := []struct {
		name        string
		args        []string
		expected    []string
		notExpected []string
	}{
		{
			name:        "default mode",
			args:        []string{"component", "--help"},
			expected:    []string{"update", "history", "query"},
			notExpected: []string{"refresh-upstream-commit"},
		},
		{
			name:        "explicitly disabled",
			args:        []string{"--without-lockfile=false", "component", "--help"},
			expected:    []string{"update", "history", "query"},
			notExpected: []string{"refresh-upstream-commit"},
		},
		{
			name:     "lock-file-free mode",
			args:     []string{"--without-lockfile", "component", "--help"},
			expected: []string{"refresh-upstream-commit"},
			// The lock-file commands remain registered as hidden no-ops, so they
			// must not be advertised in help.
			notExpected: []string{"Refresh component lock files"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			results, err := cmdtest.NewScenarioTest(testCase.args...).Locally().Run(t)
			require.NoError(t, err)
			require.Zero(t, results.ExitCode)

			for _, expected := range testCase.expected {
				assert.Contains(t, results.Stdout, expected)
			}

			for _, notExpected := range testCase.notExpected {
				assert.NotContains(t, results.Stdout, notExpected)
			}
		})
	}
}

// TestWithoutLockfile_BuildFlagsByMode verifies that the lock-file-only component
// flags are registered only in the default mode.
func TestWithoutLockfile_BuildFlagsByMode(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	defaultResults, err := cmdtest.NewScenarioTest("component", "build", "--help").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, defaultResults.ExitCode)
	assert.Contains(t, defaultResults.Stdout, "--skip-lock-validation")

	previewResults, err := cmdtest.NewScenarioTest(
		"--without-lockfile", "component", "build", "--help",
	).Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, previewResults.ExitCode)
	assert.NotContains(t, previewResults.Stdout, "--skip-lock-validation")
}

// TestWithoutLockfile_LegacyCommandIsNoOp verifies that a lock-file command invoked
// in lock-file-free mode reports that it does nothing instead of failing.
func TestWithoutLockfile_LegacyCommandIsNoOp(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	results, err := cmdtest.NewScenarioTest(
		"--without-lockfile", "component", "update", "--all-components",
	).Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, results.ExitCode)
	assert.Contains(t, results.Stdout+results.Stderr, "no longer does anything")
}

// TestWithoutLockfile_AgentSkillsByMode verifies that the emitted agent skills
// describe the workflow of the selected mode.
func TestWithoutLockfile_AgentSkillsByMode(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	defaultResults, err := cmdtest.NewScenarioTest("docs", "agent", "show").Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, defaultResults.ExitCode)
	assert.Contains(t, defaultResults.Stdout, "azldev-update-component")
	assert.NotContains(t, defaultResults.Stdout, "azldev-refresh-upstream-commit")

	previewResults, err := cmdtest.NewScenarioTest(
		"--without-lockfile", "docs", "agent", "show",
	).Locally().Run(t)
	require.NoError(t, err)
	require.Zero(t, previewResults.ExitCode)
	assert.Contains(t, previewResults.Stdout, "azldev-refresh-upstream-commit")
	assert.NotContains(t, previewResults.Stdout, "azldev-update-component")
}

// TestWithoutLockfile_ComponentChanged compares components across two commits in
// lock-file-free mode, where change detection compares the project configuration
// resolved at each ref instead of stored lock files.
//
// Flow:
//  1. Create a project with two local components (curl, bash).
//  2. Commit the project as the baseline.
//  3. In a second commit, change only curl's spec content.
//  4. Run 'azldev --without-lockfile component changed' between the two commits.
//  5. Assert curl is "changed" (its spec directory contents differ) and bash is
//     "unchanged", with no lock files anywhere in the project.
func TestWithoutLockfile_ComponentChanged(t *testing.T) {
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

	cmd := exec.CommandContext(t.Context(),
		azldevBin, "--without-lockfile", "-C", projectDir, "--no-default-config",
		"component", "changed", "--from", fromRef, "-a", "--include-unchanged", "-q", "-O", "json",
	)

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

	// No lock files are consulted or created in this mode.
	entries, err := os.ReadDir(projectDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.NotEqual(t, "locks", entry.Name(), "lock-file-free mode must not create a lock directory")
		assert.False(t, strings.HasSuffix(entry.Name(), ".lock"))
	}
}

// TestWithoutLockfile_MCPToolsByMode verifies that the MCP tool surface follows the
// selected mode's command set.
func TestWithoutLockfile_MCPToolsByMode(t *testing.T) {
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

	previewResults, err := cmdtest.NewScenarioTest("--without-lockfile", "advanced", "mcp").
		Locally().WithStdin(strings.NewReader(listToolsRequest)).Run(t)
	require.NoError(t, err)
	require.Zero(t, previewResults.ExitCode)
	assert.NotContains(t, previewResults.Stdout, `"component-history"`)
	assert.Contains(t, previewResults.Stdout, `"component-changed"`)
}
