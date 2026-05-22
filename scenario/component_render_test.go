// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/cmdtest"
	"github.com/microsoft/azure-linux-dev-tools/scenario/internal/projecttest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localComponentConfig creates a component config for a local spec at the standard test path.
func localComponentConfig(name string, overlays ...projectconfig.ComponentOverlay) *projectconfig.ComponentConfig {
	return &projectconfig.ComponentConfig{
		Name: name,
		Spec: projectconfig.SpecSource{
			SourceType: projectconfig.SpecSourceTypeLocal,
			Path:       filepath.Join("specs", name, name+".spec"),
		},
		Overlays: overlays,
	}
}

func TestRenderSimpleLocalSpec(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("test-render"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("test-render")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "test-render", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Verify JSON output reports success.
	output := results.GetJSONResult()
	require.Len(t, output, 1, "Expected one component in the output")
	assert.Equal(t, "test-render", output[0]["component"])
	assert.Equal(t, "ok", output[0]["status"],
		"Simple spec should render without warnings when rpmautospec is installed")

	// Verify rendered spec file exists with expected content.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "t", "test-render", "test-render.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "Name: test-render")
	assert.Contains(t, contentStr, "Version: 1.0.0")
}

// TestRenderWithConfiguredOutputDir verifies that rendering works when the output
// directory comes from the project config (rendered-specs-dir) instead of --output-dir.
// This is the most common real-world usage. The config auto-sets --force, enabling
// stale cleanup without an explicit flag.
func TestRenderWithConfiguredOutputDir(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("config-test"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("config-test")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
		// Set rendered-specs-dir in project config instead of using -o.
		projecttest.WithRenderedSpecsDir("SPECS"),
	)

	results := projecttest.NewProjectTest(
		project,
		// No -o flag — output dir comes from config.
		[]string{"component", "render", "config-test"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	output := results.GetJSONResult()
	require.Len(t, output, 1)
	assert.Equal(t, "ok", output[0]["status"],
		"Spec should render ok with config-provided output dir")

	renderedSpecPath := results.GetProjectOutputPath("SPECS", "c", "config-test", "config-test.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Name: config-test")
}

func TestRenderWithOverlayApplied(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("test-overlay"),
		projecttest.WithVersion("2.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("test-overlay",
			projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAddSpecTag,
				Description: "Add test build dependency",
				Tag:         "BuildRequires",
				Value:       "test-overlay-dep",
			},
		)),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "test-overlay", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Verify success.
	output := results.GetJSONResult()
	require.Len(t, output, 1)
	assert.Equal(t, "ok", output[0]["status"], "Spec should render as ok when rpmautospec is installed")

	// Verify the overlay was applied — the rendered spec should contain the added tag.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "t", "test-overlay", "test-overlay.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "BuildRequires: test-overlay-dep",
		"Overlay should have added BuildRequires tag to rendered spec")
}

func TestRenderWithPatchSidecar(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("test-patch"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	patchContent := `--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old
+new
`

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("test-patch",
			projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAddPatch,
				Description: "Add test patch",
				Source:      "patches/fix-stuff.patch",
			},
		)),
		projecttest.AddFile("patches/fix-stuff.patch", patchContent),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "test-patch", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Verify success.
	output := results.GetJSONResult()
	require.Len(t, output, 1)
	assert.Equal(t, "ok", output[0]["status"], "Spec should render as ok when rpmautospec is installed")

	// Verify the patch file is in the rendered output.
	patchPath := results.GetProjectOutputPath("SPECS", "t", "test-patch", "fix-stuff.patch")
	require.FileExists(t, patchPath, "Patch sidecar should be in rendered output")

	// Verify the spec references the patch.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "t", "test-patch", "test-patch.spec")
	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "fix-stuff.patch",
		"Rendered spec should reference the added patch")
}

func TestRenderStaleCleanup(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("keep-me"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	// Pre-populate a stale SPECS directory alongside the real component.
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("keep-me")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
		projecttest.AddFile("SPECS/s/stale-component/RENDER_FAILED", "Rendering failed.\n"),
	)

	results := projecttest.NewProjectTest(
		project,
		// Render all with -a and --clean-stale to trigger stale cleanup.
		[]string{"component", "render", "-a", "-o", "project/SPECS", "--force", "--clean-stale"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Verify the kept component was rendered.
	output := results.GetJSONResult()
	require.Len(t, output, 1)
	assert.Equal(t, "keep-me", output[0]["component"])

	// Verify the stale directory was cleaned up.
	stalePath := results.GetProjectOutputPath("SPECS", "s", "stale-component")
	assert.NoDirExists(t, stalePath, "Stale component directory should have been removed")

	// Verify the kept component still exists.
	keptPath := results.GetProjectOutputPath("SPECS", "k", "keep-me")
	assert.DirExists(t, keptPath, "Rendered component directory should still exist")
}

// TestRenderRefusesOverwriteWithoutForce verifies that rendering to an existing
// component output directory fails without --force, protecting against accidental
// data loss.
func TestRenderRefusesOverwriteWithoutForce(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("no-clobber"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	// Pre-populate the output directory with existing content.
	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("no-clobber")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
		projecttest.AddFile("SPECS/n/no-clobber/existing-file.txt", "do not delete me\n"),
	)

	results := projecttest.NewProjectTest(
		project,
		// Render WITHOUT --force — should fail because output dir exists.
		[]string{"component", "render", "no-clobber", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	output := results.GetJSONResult()
	require.Len(t, output, 1)

	// Should report error because the output directory already exists.
	assert.Equal(t, "error", output[0]["status"],
		"Render should fail when output dir exists without --force")

	// The pre-existing file should NOT have been deleted.
	existingPath := results.GetProjectOutputPath("SPECS", "n", "no-clobber", "existing-file.txt")
	require.FileExists(t, existingPath,
		"Pre-existing file should be preserved when --force is not set")
}

// TestRenderSpecWithUndefinedMacros verifies that a spec using macros not available
// on the host (like %gometa for golang packages) renders successfully via mock.
// The mock chroot has all ecosystem macro packages (go-srpm-macros, etc.) available
// via @buildsys-build, so rpmautospec and spectool succeed where host tools would fail.
func TestRenderSpecWithUndefinedMacros(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Simulate a golang-style spec that uses %gometa — a macro defined by
	// go-rpm-macros which is typically not installed on the host.
	// Must set %goipath and %forgeurl before %gometa (required by the macro).
	goSpecContent := `%global goipath         go.uber.org/atomic
%global forgeurl        https://github.com/uber-go/atomic
Version:                1.11.0

%gometa

%global common_description %{expand:
Test golang package using gometa macro.}

Name:           golang-example
Release:        %autorelease
Summary:        Example golang package
License:        MIT

%description
%{common_description}

%prep
%goprep

%build
%gobuild

%install
%goinstall

%files
%license LICENSE

%changelog
%autochangelog
`

	project := projecttest.NewDynamicTestProject(
		projecttest.AddComponent(localComponentConfig("golang-example")),
		// Write the custom spec content directly via AddFile since AddSpec's
		// TestSpec renderer doesn't support %gometa.
		projecttest.AddFile("specs/golang-example/golang-example.spec", goSpecContent),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "golang-example", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	output := results.GetJSONResult()
	require.Len(t, output, 1)

	// With mock processing (azl4 chroot has go-srpm-macros + rpmautospec),
	// the spec should render successfully.
	assert.Equal(t, "ok", output[0]["status"],
		"Spec with golang macros should render ok via mock processing")

	// The spec file should exist in the output.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "g", "golang-example", "golang-example.spec")
	require.FileExists(t, renderedSpecPath,
		"Spec should be rendered via mock processing")

	// The rendered spec should have rpmautospec headers (macros were processed).
	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	// rpmautospec should have added its header with the %define for autorelease.
	assert.Contains(t, string(content), "## START: Set by rpmautospec",
		"rpmautospec should have processed the spec")
	// %autochangelog should be expanded to real changelog entries.
	assert.NotContains(t, string(content), "%autochangelog",
		"%%autochangelog should be expanded to real entries")
}

// TestRenderMultipleComponentsParallel verifies that rendering two or more
// components in a single invocation works correctly. This exercises the batch
// mock processing path with parallel bash jobs.
func TestRenderMultipleComponentsParallel(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	specA := projecttest.NewSpec(
		projecttest.WithName("comp-alpha"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	specB := projecttest.NewSpec(
		projecttest.WithName("comp-beta"),
		projecttest.WithVersion("2.0.0"),
		projecttest.WithRelease("%autorelease"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(specA),
		projecttest.AddSpec(specB),
		projecttest.AddComponent(localComponentConfig("comp-alpha")),
		projecttest.AddComponent(localComponentConfig("comp-beta")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "-a", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	output := results.GetJSONResult()
	require.Len(t, output, 2, "Expected two components in the output")

	// Build a map for easier assertion.
	resultMap := make(map[string]map[string]interface{}, len(output))
	for _, entry := range output {
		name, ok := entry["component"].(string)
		require.True(t, ok, "component field should be a string")
		resultMap[name] = entry
	}

	// Both should succeed.
	require.Contains(t, resultMap, "comp-alpha", "comp-alpha should be in results")
	require.Contains(t, resultMap, "comp-beta", "comp-beta should be in results")

	assert.Equal(t, "ok", resultMap["comp-alpha"]["status"],
		"comp-alpha should render ok")
	assert.Equal(t, "ok", resultMap["comp-beta"]["status"],
		"comp-beta should render ok")

	// Verify both rendered specs exist.
	specAlphaPath := results.GetProjectOutputPath("SPECS", "c", "comp-alpha", "comp-alpha.spec")
	require.FileExists(t, specAlphaPath)

	specBetaPath := results.GetProjectOutputPath("SPECS", "c", "comp-beta", "comp-beta.spec")
	require.FileExists(t, specBetaPath)

	// comp-beta uses %autorelease, so rpmautospec should have processed it.
	betaContent, err := os.ReadFile(specBetaPath)
	require.NoError(t, err)
	assert.Contains(t, string(betaContent), "## START: Set by rpmautospec",
		"rpmautospec should have expanded %%autorelease for comp-beta")
}

// TestRenderBrokenSpecWithGoodSpec verifies that a malformed spec produces an
// error result while a valid spec in the same batch still renders successfully.
// This exercises the Python script's error handling in a real mock chroot.
func TestRenderBrokenSpecWithGoodSpec(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	goodSpec := projecttest.NewSpec(
		projecttest.WithName("good-pkg"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("1%{?dist}"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(goodSpec),
		projecttest.AddComponent(localComponentConfig("good-pkg")),
		// Add a broken spec as a raw file — not valid RPM spec syntax.
		projecttest.AddFile("specs/broken-pkg/broken-pkg.spec", "this is not a valid spec file\n"),
		projecttest.AddComponent(localComponentConfig("broken-pkg")),
		projecttest.UseTestDefaultConfigs(),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "-a", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	output := results.GetJSONResult()
	require.Len(t, output, 2, "Expected two components in the output")

	// Build a map for easier assertion.
	resultMap := make(map[string]map[string]interface{}, len(output))
	for _, entry := range output {
		name, ok := entry["component"].(string)
		require.True(t, ok, "component field should be a string")
		resultMap[name] = entry
	}

	// Good spec should succeed despite the broken one in the same batch.
	require.Contains(t, resultMap, "good-pkg", "good-pkg should be in results")
	require.Contains(t, resultMap, "broken-pkg", "broken-pkg should be in results")

	assert.Equal(t, "ok", resultMap["good-pkg"]["status"],
		"good-pkg should render ok even when another component fails")

	goodSpecPath := results.GetProjectOutputPath("SPECS", "g", "good-pkg", "good-pkg.spec")
	require.FileExists(t, goodSpecPath)

	// Broken spec should produce an error status.
	assert.Equal(t, "error", resultMap["broken-pkg"]["status"],
		"broken-pkg should report error for malformed spec")

	// Error marker file should be written for the broken component.
	markerPath := results.GetProjectOutputPath("SPECS", "b", "broken-pkg", "RENDER_FAILED")
	require.FileExists(t, markerPath, "RENDER_FAILED marker should exist for broken component")
}

// TestRenderLocalSpecWithSyntheticHistory verifies that rendering a local
// component with a committed lock file produces synthetic commits that
// rpmautospec can use to expand %autorelease, and that dirty detection
// correctly identifies uncommitted fingerprint changes.
//
// Existing render tests never commit lock files, so buildSyntheticCommits
// finds no lock at HEAD and returns early. This test pre-bakes a lock file
// with a stale fingerprint and includes an overlay that changes the runtime
// fingerprint — exercising both the synthetic history pipeline and dirty
// detection in one pass.
func TestRenderLocalSpecWithSyntheticHistory(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	spec := projecttest.NewSpec(
		projecttest.WithName("synth-local"),
		projecttest.WithVersion("1.0.0"),
		projecttest.WithRelease("%autorelease"),
		projecttest.WithBuildArch(projecttest.NoArch),
	)

	// Pre-baked lock file with a stale fingerprint. The overlay in the
	// component config changes the runtime fingerprint, so dirty detection
	// will fire and add a synthetic commit.
	const lockFileContent = `version = 1
input-fingerprint = "pre-baked-for-test"
`

	project := projecttest.NewDynamicTestProject(
		projecttest.AddSpec(spec),
		projecttest.AddComponent(localComponentConfig("synth-local",
			projectconfig.ComponentOverlay{
				Type:        projectconfig.ComponentOverlayAddSpecTag,
				Description: "Add overlay to trigger dirty detection",
				Tag:         "BuildRequires",
				Value:       "dirty-dep",
			},
		)),
		projecttest.UseTestDefaultConfigs(),
		projecttest.AddFile("locks/synth-local.lock", lockFileContent),
		projecttest.WithGitRepo(),
	)

	results := projecttest.NewProjectTest(
		project,
		[]string{"component", "render", "synth-local", "-o", "project/SPECS"},
		projecttest.WithTestDefaultConfigs(),
	).RunInContainer(t)

	// Verify JSON output reports success.
	output := results.GetJSONResult()
	require.Len(t, output, 1, "Expected one component in the output")
	assert.Equal(t, "ok", output[0]["status"],
		"Local component with lock file should render ok")

	// Verify rendered spec exists and has rpmautospec processing.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "s", "synth-local", "synth-local.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	contentStr := string(content)

	// rpmautospec should have processed %autorelease using the synthetic
	// git history (which includes the dirty-detected commit from the
	// pre-baked lock file's stale fingerprint).
	assert.Contains(t, contentStr, "## START: Set by rpmautospec",
		"rpmautospec should have expanded %%autorelease for local component with lock file")

	// The overlay should be applied to the rendered spec, confirming that
	// dirty detection fired (the overlay changes the runtime fingerprint).
	assert.Contains(t, contentStr, "BuildRequires: dirty-dep",
		"Overlay should be applied to rendered spec")
}

// TestRenderUpstreamFromLocalDistGit verifies that the synthetic dist-git
// pipeline produces correct Release and changelog values by cloning from a
// controlled local file:// bare repo with a known commit history.
//
// Setup:
//
//	Upstream dist-git (3 commits): C1(v1.0.0) → C2(v1.1.0) → C3(add patch)
//	Project lock file (2 fingerprint changes): fp1, fp2
//	Dirty detection: runtime fingerprint ≠ fp2 → 1 extra synthetic commit
//
//	All synthetic commits reference C3 (HEAD), so interleaving places them
//	after the upstream history: C1 → C2 → C3 → S1 → S2 → S3(dirty)
//	rpmautospec resets release on Version change (C2), giving Release = 5.
//
// The bare repo is created inside the container script because
// AddDirRecursive loses git internal structure needed for file:// clones.
func TestRenderUpstreamFromLocalDistGit(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping long test")
	}

	// Spec content using %autorelease and %autochangelog so rpmautospec
	// will derive Release and changelog from the git history.
	specContent := `Name:    test-pkg
Version: 1.0.0
Release: %autorelease
Summary: Test package for local dist-git
License: MIT
BuildArch: noarch

%description
Test package.

%build
echo hello >file.txt

%install
mkdir -p %{buildroot}/%{_datadir}/test-pkg
cp file.txt %{buildroot}/%{_datadir}/test-pkg/file.txt

%files
%{_datadir}/test-pkg

%changelog
%autochangelog
`

	// Custom distro config pointing at a local bare repo.
	// Using file:// URI — this is why we relaxed validateAbsoluteURL.
	distroConfig := `[distros.local-test]
description = "Local test dist-git"
default-version = "1.0"
dist-git-base-uri = "file:///workdir/upstream-repo/$pkg.git"
lookaside-base-uri = "file:///dev/null/$pkg/$filename/$hashtype/$hash/$filename"

[distros.local-test.versions.'1.0']
description = "Local test v1.0"
release-ver = "1.0"
dist-git-branch = "main"
`

	project := projecttest.NewDynamicTestProject(
		projecttest.AddComponent(&projectconfig.ComponentConfig{
			Name: "test-pkg",
			Spec: projectconfig.SpecSource{
				UpstreamDistro: projectconfig.DistroReference{
					Name:    "local-test",
					Version: "1.0",
				},
			},
		}),
		projecttest.UseTestDefaultConfigs(),
		projecttest.AddFile("local-distro.toml", distroConfig),
		// The spec content is used by the script to build the upstream repo.
		projecttest.AddFile("upstream-spec.txt", specContent),
		projecttest.WithGitRepo(),
	)

	projectStagingDir := t.TempDir()
	project.Serialize(t, projectStagingDir)

	// Patch the azldev.toml to include the local distro config.
	azldevToml, err := os.ReadFile(filepath.Join(projectStagingDir, "azldev.toml"))
	require.NoError(t, err)

	patched := strings.Replace(string(azldevToml),
		"includes = [",
		"includes = [\"local-distro.toml\", ",
		1,
	)
	require.NoError(t, os.WriteFile(filepath.Join(projectStagingDir, "azldev.toml"), []byte(patched), 0o644))

	// Re-commit the patched azldev.toml so the git repo is clean.
	patchCmd := exec.CommandContext(t.Context(), "git", "add", "azldev.toml")
	patchCmd.Dir = projectStagingDir
	patchOut, err := patchCmd.CombinedOutput()
	require.NoError(t, err, "git add failed: %s", string(patchOut))

	commitCmd := exec.CommandContext(t.Context(), "git", "-c", "commit.gpgsign=false", "commit", "--amend", "--no-edit")
	commitCmd.Dir = projectStagingDir
	commitOut, err := commitCmd.CombinedOutput()
	require.NoError(t, err, "git commit --amend failed: %s", string(commitOut))

	// The script:
	// 1. Creates the upstream bare repo with 3 controlled commits
	// 2. Commits the lock file twice with different fingerprints to simulate
	//    a realistic workflow (import → overlay change)
	// 3. Renders and captures output
	//
	// This produces 2 fingerprint changes + 1 dirty detection = 3 synthetic
	// commits, interleaved on top of 3 upstream commits = 6 total → Release 6.
	testScript := `
set -ex

rm -rf project/build
ln -s /var/lib/mock project/build

# --- Create the upstream dist-git repo with controlled commits ---
WORK=$(mktemp -d)
cd "$WORK"
git init -b main
git config user.email "test@test.com"
git config user.name "Test User"

# Commit 1: initial import
cp /workdir/project/upstream-spec.txt test-pkg.spec
git add .
git -c commit.gpgsign=false commit --date="2024-01-15T12:00:00Z" -m "Initial import of test-pkg"
FIRST_COMMIT=$(git rev-parse HEAD)

# Commit 2: bump version
sed -i 's/Version: 1.0.0/Version: 1.1.0/' test-pkg.spec
git add .
git -c commit.gpgsign=false commit --date="2024-06-01T12:00:00Z" -m "Bump to 1.1.0"

# Commit 3: add patch
printf -- '--- a/x\n+++ b/x\n' > fix.patch
git add .
git -c commit.gpgsign=false commit --date="2025-01-10T12:00:00Z" -m "Add fix.patch for build issue"
HEAD_COMMIT=$(git rev-parse HEAD)

# Clone to bare repo at the path the distro config expects
mkdir -p /workdir/upstream-repo
git clone --bare "$WORK" /workdir/upstream-repo/test-pkg.git
cd /workdir

# --- Create lock file with multiple fingerprint changes ---
# This simulates a realistic project history where the component is
# imported, then overlays are added/modified over time.
mkdir -p project/locks
cd project

# Lock commit 1: initial import (fingerprint fp1)
cat > locks/test-pkg.lock <<EOF
version = 1
import-commit = "$FIRST_COMMIT"
upstream-commit = "$HEAD_COMMIT"
input-fingerprint = "fp1-initial-import"
EOF
git add locks/test-pkg.lock
git -c commit.gpgsign=false commit -m "Import test-pkg with initial lock"

# Lock commit 2: simulated overlay addition (fingerprint fp2)
cat > locks/test-pkg.lock <<EOF
version = 1
import-commit = "$FIRST_COMMIT"
upstream-commit = "$HEAD_COMMIT"
input-fingerprint = "fp2-added-buildrequires"
EOF
git add locks/test-pkg.lock
git -c commit.gpgsign=false commit -m "Update lock: add BuildRequires overlay"

cd /workdir

# --- Render ---
azldev -C project -v component render test-pkg -o project/SPECS --output-format json >result.json
`

	scenarioTest := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(testScript)).
		AddDirRecursive(t, "project", projectStagingDir).
		AddDirRecursive(t, projecttest.TestDefaultConfigsSubdir, projecttest.TestDefaultConfigsDir())

	testResults, err := scenarioTest.
		InContainer().
		WithPrivilege().
		WithNetwork().
		Run(t)

	require.NoError(t, err)

	t.Logf("Standard output:\n%s", testResults.Stdout)
	t.Logf("Standard error:\n%s", testResults.Stderr)

	testResults.AssertZeroExitCode(t)

	outputBytes, err := os.ReadFile(filepath.Join(testResults.Workdir, "result.json"))
	require.NoError(t, err)
	t.Logf("Render output:\n%s", string(outputBytes))

	// Verify rendered spec exists.
	renderedSpecPath := filepath.Join(testResults.Workdir, "project", "SPECS", "t", "test-pkg", "test-pkg.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	contentStr := string(content)
	t.Logf("Rendered spec:\n%s", contentStr)

	// rpmautospec should have processed %autorelease.
	assert.Contains(t, contentStr, "## START: Set by rpmautospec",
		"rpmautospec should have expanded %%autorelease")

	// The Version should reflect the spec at the pinned upstream commit.
	// Commit 2 bumped Version from 1.0.0 to 1.1.0; commit 3 (HEAD) didn't
	// change it back. This confirms the correct commit was checked out.
	assert.Contains(t, contentStr, "Version: 1.1.0",
		"Rendered spec should have Version from the pinned upstream commit")

	// Verify the release number from the rpmautospec %define block.
	// rpmautospec writes a Lua block containing: release_number = N;
	// where N is the computed release based on commit count since the
	// last Version change.
	//
	// Expected release calculation:
	// All synthetic commits reference the same upstream-commit (C3/HEAD),
	// so interleaving places them all in the "top" group after C3:
	//   C1 → C2 → C3 → S1(fp1) → S2(fp2) → S3(dirty)
	// rpmautospec resets release on Version change (C2 bumps 1.0.0 → 1.1.0):
	//   C2 = release 1 (reset), C3 = 2, S1 = 3, S2 = 4, S3 = 5
	releasePattern := regexp.MustCompile(`release_number = (\d+);`)
	releaseMatch := releasePattern.FindStringSubmatch(contentStr)
	require.NotEmpty(t, releaseMatch,
		"Should find release_number in rpmautospec Lua block")
	assert.Equal(t, "5", releaseMatch[1],
		"Release should be 5 (commits since last Version change: C2=1, C3=2, S1=3, S2=4, S3=5)")

	// %autochangelog should be expanded to real entries from the controlled history.
	assert.NotContains(t, contentStr, "%autochangelog",
		"%%autochangelog should be expanded to real entries")

	// The changelog should contain our controlled commit messages in
	// newest-first order. Synthetic commits (S1, S2, S3) appear before
	// upstream commits (C3, C2, C1) because interleaving places them on top.
	assert.Contains(t, contentStr, "Add fix.patch for build issue",
		"Changelog should contain the third commit message")
	assert.Contains(t, contentStr, "Bump to 1.1.0",
		"Changelog should contain the second commit message")
	assert.Contains(t, contentStr, "Initial import of test-pkg",
		"Changelog should contain the first commit message")

	// Verify ordering: synthetic commits should appear above upstream commits.
	// In newest-first changelog, "Import test-pkg with initial lock" (S1)
	// must precede "Add fix.patch for build issue" (C3).
	s1Pos := strings.Index(contentStr, "Import test-pkg with initial lock")
	c3Pos := strings.Index(contentStr, "Add fix.patch for build issue")
	require.Greater(t, s1Pos, 0, "S1 commit message should be in changelog")
	require.Greater(t, c3Pos, 0, "C3 commit message should be in changelog")
	assert.Less(t, s1Pos, c3Pos,
		"Synthetic commit (S1) should appear before upstream commit (C3) in changelog")
}
