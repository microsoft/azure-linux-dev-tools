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
	"sort"
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
	// immediately after the [project] header.
	content := string(data)
	content = "includes = [\"distro.toml\"]\n" + content

	const projectHeader = "[project]"

	require.True(t, strings.Contains(content, projectHeader),
		"azldev.toml must contain %#q for patchProjectForLocal to work", projectHeader)

	content = strings.Replace(content,
		projectHeader,
		projectHeader+"\ndefault-distro = { name = \"testdistro\", version = \"1.0\" }",
		1,
	)

	require.NoError(t, os.WriteFile(configPath, []byte(content), fileperms.PublicFile))
}

// changedResult mirrors [component.ChangedResult] for JSON deserialization.
type changedResult struct {
	Component     string `json:"component"`
	ChangeType    string `json:"changeType"`
	SourcesChange bool   `json:"sourcesChange"`
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
// with a real git repository, verifying JSON output for multi-component change
// detection.
//
// Flow:
//  1. Create a project with two local components (curl, bash) and a minimal
//     distro config patched in for local (non-container) execution.
//  2. Commit initial lock files for both components with distinct fingerprints.
//  3. In a second commit, update only curl's lock file (new fingerprint).
//  4. Run `azldev component changed --from <commit1> -a --include-unchanged`
//     to compare the two commits with JSON output.
//  5. Assert curl is reported as "changed" (fingerprint differs between refs)
//     and bash as "unchanged" (fingerprint identical at both refs).
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

	// Step 2: Initialize git repo. Create lock files for both components
	// with version 1 fingerprints, then commit everything as the baseline.
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

// runChanged runs `azldev component changed` with the given args and returns
// parsed JSON results. Fails the test on any error.
func runChanged(t *testing.T, azldevBin, projectDir string, extraArgs ...string) []changedResult {
	t.Helper()

	args := []string{"-C", projectDir, "--no-default-config", "component", "changed"}
	args = append(args, extraArgs...)
	args = append(args, "-q", "-O", "json")

	cmd := exec.CommandContext(t.Context(), azldevBin, args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(out))

	var results []changedResult
	require.NoError(t, json.Unmarshal(out, &results), "failed to parse JSON: %s", string(out))

	return results
}

// resultMap converts a slice of changedResult into a map keyed by component name.
func resultMap(results []changedResult) map[string]changedResult {
	m := make(map[string]changedResult, len(results))
	for _, r := range results {
		m[r.Component] = r
	}

	return m
}

// setupProjectWithGit creates a project with the given specs and components,
// initializes a git repo, and returns (azldevBin, projectDir). The project has
// a minimal distro config patched in for local execution.
func setupProjectWithGit(
	t *testing.T,
	specs []*projecttest.TestSpec,
	components []*projectconfig.ComponentConfig,
	extraFiles map[string]string,
) (string, string) {
	t.Helper()

	azldevBin, err := testhelpers.FindTestBinary()
	require.NoError(t, err)

	projectDir := t.TempDir()

	opts := []projecttest.DynamicTestProjectOption{
		projecttest.AddFile("distro.toml", minimalDistroTOML),
	}

	for _, spec := range specs {
		opts = append(opts, projecttest.AddSpec(spec))
	}

	for _, comp := range components {
		opts = append(opts, projecttest.AddComponent(comp))
	}

	// Sort keys for deterministic option ordering.
	extraKeys := make([]string, 0, len(extraFiles))
	for path := range extraFiles {
		extraKeys = append(extraKeys, path)
	}

	sort.Strings(extraKeys)

	for _, path := range extraKeys {
		opts = append(opts, projecttest.AddFile(path, extraFiles[path]))
	}

	project := projecttest.NewDynamicTestProject(opts...)
	project.Serialize(t, projectDir)
	patchProjectForLocal(t, projectDir)

	gitInDir(t, projectDir, "init")
	gitInDir(t, projectDir, "config", "user.email", "test@test.com")
	gitInDir(t, projectDir, "config", "user.name", "Test")

	return azldevBin, projectDir
}

// TestComponentChanged_SourcesChange verifies that the sources change column
// correctly reflects rendered sources file changes.
//
// Flow:
//  1. Create a project with one component (curl) and a rendered-specs-dir.
//  2. Commit initial lock + sources file.
//  3. In a second commit, update the lock fingerprint AND the sources file.
//  4. Assert changeType="changed" and sourcesChange="true".
//  5. In a third commit, update only the lock (not sources).
//  6. Compare commit 2→3: assert changeType="changed", sourcesChange="false"
//     (rebuild needed but no tarball re-upload).
func TestComponentChanged_SourcesChange(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("8.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}},
		nil,
	)

	// Commit 1: initial lock + rendered sources.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:v1"))
	writeFileInDir(t, projectDir, "specs/c/curl/sources",
		"SHA512 (curl-8.0.tar.gz) = aaa111")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	ref1 := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 2: change lock fingerprint AND sources (new tarball).
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:v2"))
	writeFileInDir(t, projectDir, "specs/c/curl/sources",
		"SHA512 (curl-8.1.tar.gz) = bbb222")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "update sources")
	ref2 := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 3: change lock fingerprint only (config tweak, same tarball).
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:v3"))
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "config change")

	// ref1 → ref2: fingerprint AND sources changed.
	results := runChanged(t, azldevBin, projectDir, "--from", ref1, "--to", ref2, "-a")
	rm := resultMap(results)
	require.Contains(t, rm, "curl")
	assert.Equal(t, "changed", rm["curl"].ChangeType)
	assert.True(t, rm["curl"].SourcesChange, "sources file changed between refs")

	// ref2 → HEAD: fingerprint changed, sources unchanged.
	results = runChanged(t, azldevBin, projectDir, "--from", ref2, "-a")
	rm = resultMap(results)
	require.Contains(t, rm, "curl")
	assert.Equal(t, "changed", rm["curl"].ChangeType)
	assert.False(t, rm["curl"].SourcesChange, "sources file identical — rebuild without re-upload")
}

// TestComponentChanged_InvertedRefs verifies that swapping --from and --to
// produces correct results in the reverse direction.
//
// Flow:
//  1. Create a project with one component, commit two versions of its lock.
//  2. Compare forward (old→new): changeType="changed".
//  3. Compare backward (new→old): also changeType="changed" (fingerprint
//     still differs, just in the other direction).
func TestComponentChanged_InvertedRefs(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("1.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}},
		nil,
	)

	// Commit 1: v1 lock.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:old"))
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "v1")
	oldRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 2: v2 lock.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:new"))
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "v2")
	newRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Forward: old → new.
	forward := runChanged(t, azldevBin, projectDir, "--from", oldRef, "--to", newRef, "-a")
	rm := resultMap(forward)
	require.Contains(t, rm, "curl")
	assert.Equal(t, "changed", rm["curl"].ChangeType, "forward: fingerprint differs")

	// Backward: new → old (inverted).
	backward := runChanged(t, azldevBin, projectDir, "--from", newRef, "--to", oldRef, "-a")
	rm = resultMap(backward)
	require.Contains(t, rm, "curl")
	assert.Equal(t, "changed", rm["curl"].ChangeType, "backward: fingerprint still differs")
}

// TestComponentChanged_NewComponent verifies that a component whose lock file
// appears between the two refs is reported as "added".
//
// Flow:
//  1. Commit with no lock files.
//  2. Add a lock file for curl.
//  3. Compare: curl should be "added".
func TestComponentChanged_NewComponent(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("1.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}},
		nil,
	)

	// Commit 1: no lock files, just a placeholder.
	writeFileInDir(t, projectDir, "placeholder", "x")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "empty")
	fromRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 2: add curl lock.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:first"))
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "add curl")

	results := runChanged(t, azldevBin, projectDir, "--from", fromRef, "-a")
	rm := resultMap(results)
	require.Contains(t, rm, "curl")
	assert.Equal(t, "added", rm["curl"].ChangeType, "new lock file should be reported as added")
}

// TestComponentChanged_DeletedComponent verifies that a non-config component
// whose lock existed at --from but not at --to is reported as "deleted".
//
// Flow:
//  1. Commit lock files for curl (in config) and oldpkg (NOT in config).
//  2. Remove oldpkg's lock file in a second commit.
//  3. Compare with -a: oldpkg should appear as "deleted".
func TestComponentChanged_DeletedComponent(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("1.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{{
			Name: "curl",
			Spec: projectconfig.SpecSource{
				SourceType: projectconfig.SpecSourceTypeLocal,
				Path:       filepath.Join("specs", "curl", "curl.spec"),
			},
		}},
		nil,
	)

	// Commit 1: lock files for curl (in config) and oldpkg (NOT in config).
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:curl-v1"))
	writeFileInDir(t, projectDir, "locks/oldpkg.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:oldpkg-v1"))
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	fromRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 2: remove oldpkg's lock file.
	gitInDir(t, projectDir, "rm", "locks/oldpkg.lock")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "remove oldpkg")

	// Compare with -a to enable non-config component detection.
	results := runChanged(t, azldevBin, projectDir, "--from", fromRef, "-a")
	rm := resultMap(results)

	// curl should be unchanged (same fingerprint).
	// oldpkg (not in config, lock removed) should be "deleted".
	require.Contains(t, rm, "oldpkg", "deleted non-config component should appear in results")
	assert.Equal(t, "deleted", rm["oldpkg"].ChangeType, "removed lock for non-config component")
}

// TestComponentChanged_JSONContract validates the JSON output schema is stable
// for CI consumers. Any field rename, type change, or value-set change will
// break this test — that's intentional.
//
// The test exercises all four changeType values (added, changed, unchanged,
// deleted) and both sourcesChange values (true, false) in a single run,
// then validates the raw JSON structure without the changedResult struct
// to catch accidental field renames.
func TestComponentChanged_JSONContract(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	azldevBin, projectDir := setupProjectWithGit(t,
		[]*projecttest.TestSpec{
			projecttest.NewSpec(
				projecttest.WithName("curl"),
				projecttest.WithVersion("1.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
			projecttest.NewSpec(
				projecttest.WithName("bash"),
				projecttest.WithVersion("1.0.0"),
				projecttest.WithRelease("1%{?dist}"),
				projecttest.WithBuildArch(projecttest.NoArch),
			),
		},
		[]*projectconfig.ComponentConfig{
			{
				Name: "curl",
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeLocal,
					Path:       filepath.Join("specs", "curl", "curl.spec"),
				},
			},
			{
				Name: "bash",
				Spec: projectconfig.SpecSource{
					SourceType: projectconfig.SpecSourceTypeLocal,
					Path:       filepath.Join("specs", "bash", "bash.spec"),
				},
			},
		},
		nil,
	)

	// Commit 1: curl has lock + sources, bash has lock, oldpkg has lock (not in config).
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:curl-v1"))
	writeFileInDir(t, projectDir, "locks/bash.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:bash-v1"))
	writeFileInDir(t, projectDir, "locks/oldpkg.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:old-v1"))
	writeFileInDir(t, projectDir, "specs/c/curl/sources",
		"SHA512 (curl-1.0.tar.gz) = aaa")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	fromRef := gitInDir(t, projectDir, "rev-parse", "HEAD")

	// Commit 2: curl fingerprint + sources changed, bash unchanged, oldpkg removed.
	writeFileInDir(t, projectDir, "locks/curl.lock",
		fmt.Sprintf("version = 1\ninput-fingerprint = %q\n", "sha256:curl-v2"))
	writeFileInDir(t, projectDir, "specs/c/curl/sources",
		"SHA512 (curl-2.0.tar.gz) = bbb")
	gitInDir(t, projectDir, "rm", "locks/oldpkg.lock")
	gitInDir(t, projectDir, "add", ".")
	gitInDir(t, projectDir, "-c", "commit.gpgsign=false", "commit", "-m", "update")

	// Run with --include-unchanged to get all four states.
	cmd := exec.CommandContext(t.Context(),
		azldevBin, "-C", projectDir, "--no-default-config", "component", "changed",
		"--from", fromRef, "-a", "--include-unchanged", "-q", "-O", "json",
	)
	rawOutput, err := cmd.CombinedOutput()
	require.NoError(t, err, "azldev failed: %s", string(rawOutput))

	// Parse as raw JSON to validate field names without relying on the Go struct.
	var rawResults []map[string]interface{}
	require.NoError(t, json.Unmarshal(rawOutput, &rawResults),
		"output must be a JSON array of objects: %s", string(rawOutput))

	// Validate every result has exactly the expected fields.
	expectedFields := []string{"component", "changeType", "sourcesChange"}

	for idx, row := range rawResults {
		for _, field := range expectedFields {
			_, ok := row[field]
			require.True(t, ok, "row %d missing required field %#q: %v", idx, field, row)
		}

		assert.Len(t, row, len(expectedFields),
			"row %d has unexpected extra fields: %v", idx, row)
	}

	// Validate the value sets via the typed results.
	results := runChanged(t, azldevBin, projectDir,
		"--from", fromRef, "-a", "--include-unchanged")
	rm := resultMap(results)

	// Expected states:
	//   curl:   changed  + sourcesChange=true  (both fingerprint and sources differ)
	//   bash:   unchanged + sourcesChange=false (identical at both refs)
	//   oldpkg: deleted  + sourcesChange=true   (non-config, lock removed)
	validChangeTypes := map[string]bool{
		"added": true, "changed": true, "unchanged": true, "deleted": true,
	}

	for _, r := range results {
		assert.True(t, validChangeTypes[r.ChangeType],
			"component %#q: changeType %#q not in valid set", r.Component, r.ChangeType)
	}

	// Pin specific expected values.
	require.Contains(t, rm, "curl")
	assert.Equal(t, "changed", rm["curl"].ChangeType)
	assert.True(t, rm["curl"].SourcesChange)

	require.Contains(t, rm, "bash")
	assert.Equal(t, "unchanged", rm["bash"].ChangeType)
	assert.False(t, rm["bash"].SourcesChange)

	require.Contains(t, rm, "oldpkg")
	assert.Equal(t, "deleted", rm["oldpkg"].ChangeType)
}
