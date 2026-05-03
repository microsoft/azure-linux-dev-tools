// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalDistroTOML provides a minimal distro config for local scenario tests
// that don't use UseTestDefaultConfigs (which requires container mode).
const minimalDistroTOML = `[distros.testdistro]
description = "Test Distro"

[distros.testdistro.versions.'1.0']
release-ver = "1.0"
`

// patchProjectForLocal appends includes and default-distro to the project config
// so it works without UseTestDefaultConfigs. Call after Serialize.
func patchProjectForLocal(t *testing.T, projectDir string) {
	t.Helper()

	configPath := filepath.Join(projectDir, "azldev.toml")
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	// Inject includes at the top (before any table headers) and default-distro
	// under the existing [project] section by appending the key after the table.
	content := string(data)
	content = "includes = [\"distro.toml\"]\n" + content
	content = strings.Replace(content,
		"[project]",
		"[project]\ndefault-distro = { name = \"testdistro\", version = \"1.0\" }",
		1,
	)

	require.NoError(t, os.WriteFile(configPath, []byte(content), fileperms.PublicFile))
}

// changedResult mirrors [component.ChangedResult] for JSON deserialization.
type changedResult struct {
	Component     string `json:"component"`
	ChangeType    string `json:"changeType"`
	SourcesChange string `json:"sourcesChange"`
}

// gitInDir runs a git command in the specified directory.
func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))

	return strings.TrimSpace(string(out))
}

// writeFileInDir creates a file at relPath under dir with the given content.
func writeFileInDir(t *testing.T, dir, relPath, content string) {
	t.Helper()

	absPath := filepath.Join(dir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absPath), fileperms.PublicDir))
	require.NoError(t, os.WriteFile(absPath, []byte(content), fileperms.PublicFile))
}

// TestComponentChanged_E2E exercises the full `azldev component changed` command
// with a real git repository, verifying JSON output across multiple scenarios.
func TestComponentChanged_E2E(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, err := testhelpers.FindTestBinary()
	require.NoError(t, err)

	// Create a project with two local components.
	projectDir := t.TempDir()

	spec1 := projecttest.NewSpec(
		projecttest.WithName("curl"),
		projecttest.WithVersion("8.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	spec2 := projecttest.NewSpec(
		projecttest.WithName("bash"),
		projecttest.WithVersion("5.2.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec1),
		projecttest.AddSpec(spec2),
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

	// Init git repo and make first commit with lock files.
	gitInDir(t, projectDir, "init")
	gitInDir(t, projectDir, "config", "user.email", "test@test.com")
	gitInDir(t, projectDir, "config", "user.name", "Test")

	lockV1Curl := fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:curl-v1")
	lockV1Bash := fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:bash-v1")

	writeFileInDir(t, projectDir, "locks/curl.lock", lockV1Curl)
	writeFileInDir(t, projectDir, "locks/bash.lock", lockV1Bash)

	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")

	fromRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Second commit: change curl's lock, leave bash unchanged.
	lockV2Curl := fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:curl-v2")
	writeFileInDir(t, projectDir, "locks/curl.lock", lockV2Curl)

	gitInDir(t, projectDir, "add", "locks/curl.lock")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "update curl")

	// Run: all components, include unchanged.
	cmd := exec.CommandContext(t.Context(),
		azldevBin, "-C", projectDir, "--no-default-config", "component", "changed",
		"--from", fromRef, "-a", "--include-unchanged", "-q", "-O", "json",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(out))

	var results []changedResult
	require.NoError(t, json.Unmarshal(out, &results), "failed to parse JSON: %s", string(out))

	// Build a map for easy lookup.
	resultMap := make(map[string]changedResult)
	for _, result := range results {
		resultMap[result.Component] = result
	}

	// curl should be changed.
	curlResult, ok := resultMap["curl"]
	require.True(t, ok, "curl should be in results")
	assert.Equal(t, "changed", curlResult.ChangeType, "curl fingerprint changed")

	// bash should be unchanged.
	bashResult, ok := resultMap["bash"]
	require.True(t, ok, "bash should be in results (--all-components)")
	assert.Equal(t, "unchanged", bashResult.ChangeType, "bash fingerprint unchanged")
}

// TestComponentChanged_SameRef verifies that comparing a ref to itself produces
// no changes.
func TestComponentChanged_SameRef(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, err := testhelpers.FindTestBinary()
	require.NoError(t, err)

	projectDir := t.TempDir()

	spec := projecttest.NewSpec(
		projecttest.WithName("curl"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(&projectconfig.ComponentConfig{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}),
		projecttest.AddFile("distro.toml", minimalDistroTOML),
	)

	project.Serialize(t, projectDir)
	patchProjectForLocal(t, projectDir)

	gitInDir(t, projectDir, "init")
	gitInDir(t, projectDir, "config", "user.email", "test@test.com")
	gitInDir(t, projectDir, "config", "user.name", "Test")

	lockContent := fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:v1")
	writeFileInDir(t, projectDir, "locks/curl.lock", lockContent)

	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")

	ref := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Compare ref to itself — include unchanged so we get results.
	cmd := exec.CommandContext(t.Context(),
		azldevBin, "-C", projectDir, "--no-default-config", "component", "changed",
		"--from", ref, "--to", ref, "-a", "--include-unchanged", "-q", "-O", "json",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(out))

	var results []changedResult
	require.NoError(t, json.Unmarshal(out, &results), "failed to parse JSON: %s", string(out))

	require.NotEmpty(t, results, "same-ref comparison should return at least one component")

	for _, result := range results {
		assert.Equal(t, "unchanged", result.ChangeType, "no changes expected for same-ref comparison: %s", result.Component)
	}
}
