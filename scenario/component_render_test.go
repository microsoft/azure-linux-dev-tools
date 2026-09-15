// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//go:build scenario

package scenario_tests

import (
	"os"
	"path/filepath"
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

func TestRenderWithoutLockfileReleaseToolsUsePristineAndDeterministicInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test")
	}

	const script = `
set -eux

create_dist_git() {
	name=$1
	release=$2

	mkdir -p "upstream/$name"
	git -C "upstream/$name" init --initial-branch=main
	git -C "upstream/$name" config user.name "Upstream Author"
	git -C "upstream/$name" config user.email "upstream@example.com"

	cat >"upstream/$name/$name.spec" <<EOF
Name: $name
Version: 1.0
Release: $release
Summary: Test package
License: MIT

%description
Test package.

%files

%changelog
EOF

	git -C "upstream/$name" add .
	GIT_AUTHOR_DATE=2025-01-02T03:04:05Z \
	GIT_COMMITTER_DATE=2025-01-02T03:04:05Z \
		git -C "upstream/$name" commit -m "Initial package"
}

create_dist_git autorelease %autorelease
create_dist_git static-release '1%{?dist}'

autorelease_commit=$(git -C upstream/autorelease rev-parse HEAD)
static_commit=$(git -C upstream/static-release rev-parse HEAD)
upstream_base_uri="file://$PWD/upstream/\$pkg"

mkdir project
cat >project/azldev.toml <<EOF
includes = ["distro.toml"]

[project]
default-distro = { name = "test", version = "1" }

[components.autorelease]
spec = { type = "upstream", upstream-commit = "$autorelease_commit" }

[components.static-release]
spec = { type = "upstream", upstream-commit = "$static_commit" }
EOF

cat >project/distro.toml <<EOF
[distros.test]
default-version = "1"
dist-git-base-uri = "$upstream_base_uri"

[distros.test.versions.'1']
release-ver = "1"
dist-git-branch = "main"

[distros.test.versions.'1'.default-component-config]
spec = { type = "upstream", upstream-distro = { name = "test", version = "1" } }
EOF

git -C project init --initial-branch=main
git -C project config user.name "Project Author"
git -C project config user.email "project@example.com"
git -C project add .
GIT_AUTHOR_DATE=2026-02-03T04:05:06Z \
GIT_COMMITTER_DATE=2026-02-03T04:05:06Z \
	git -C project commit -m "Add components"

mkdir -p "$HOME"
echo '%packager First Host <first@example.com>' >"$HOME/.rpmmacros"
azldev -C project --without-lockfile component render \
	autorelease static-release -o "$PWD/render-one"

! grep -R "Uncommitted changes" render-one
grep -F '%autochangelog' render-one/a/autorelease/autorelease.spec
grep -F '* Tue Feb 03 2026 Project Author <project@example.com> - 1.0-2' \
	render-one/s/static-release/static-release.spec

echo '%packager Second Host <second@example.com>' >"$HOME/.rpmmacros"
azldev -C project --without-lockfile component render \
	autorelease static-release -o "$PWD/render-two"

diff -ru render-one render-two
`

	results, err := cmdtest.NewScenarioTest().
		WithScript(strings.NewReader(script)).
		InContainer().
		Run(t)
	require.NoError(t, err)

	t.Logf("Standard output:\n%s", results.Stdout)
	t.Logf("Standard error:\n%s", results.Stderr)
	results.AssertZeroExitCode(t)
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
	assert.Equal(t, "ok", output[0]["status"])

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
	assert.Equal(t, "ok", output[0]["status"])

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
		projecttest.AddFile("specs/test-patch/unreferenced.txt", "preserve me\n"),
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
	assert.Equal(t, "ok", output[0]["status"])

	// Verify the patch file is in the rendered output.
	patchPath := results.GetProjectOutputPath("SPECS", "t", "test-patch", "fix-stuff.patch")
	require.FileExists(t, patchPath, "Patch sidecar should be in rendered output")

	unreferencedPath := results.GetProjectOutputPath("SPECS", "t", "test-patch", "unreferenced.txt")
	require.FileExists(t, unreferencedPath, "Render should preserve unreferenced dist-git files")

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

// TestRenderSpecWithUndefinedMacros verifies that render copies a spec containing
// macros that are unavailable on the host without attempting to expand them.
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

	assert.Equal(t, "ok", output[0]["status"],
		"Spec with golang macros should render without macro processing")

	// The spec file should exist in the output.
	renderedSpecPath := results.GetProjectOutputPath("SPECS", "g", "golang-example", "golang-example.spec")
	require.FileExists(t, renderedSpecPath,
		"Spec should be copied into the rendered dist-git dir")

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	assert.NotContains(t, string(content), "## START: Set by rpmautospec")
	assert.Contains(t, string(content), "Release:        %autorelease")
	assert.Contains(t, string(content), "%autochangelog")
}

// TestRenderMultipleComponentsParallel verifies that rendering two or more
// components in a single invocation works correctly.
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

	betaContent, err := os.ReadFile(specBetaPath)
	require.NoError(t, err)
	assert.Contains(t, string(betaContent), "Release: %autorelease",
		"render should preserve %%autorelease")
	assert.NotContains(t, string(betaContent), "## START: Set by rpmautospec")
}

// TestRenderDoesNotParseSpecs verifies that render preserves malformed spec
// content instead of invoking RPM tooling to validate or transform it.
func TestRenderDoesNotParseSpecs(t *testing.T) {
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

	require.Contains(t, resultMap, "good-pkg", "good-pkg should be in results")
	require.Contains(t, resultMap, "broken-pkg", "broken-pkg should be in results")

	assert.Equal(t, "ok", resultMap["good-pkg"]["status"],
		"good-pkg should render successfully")
	assert.Equal(t, "ok", resultMap["broken-pkg"]["status"],
		"render should not parse or validate spec syntax")

	goodSpecPath := results.GetProjectOutputPath("SPECS", "g", "good-pkg", "good-pkg.spec")
	require.FileExists(t, goodSpecPath)

	brokenSpecPath := results.GetProjectOutputPath("SPECS", "b", "broken-pkg", "broken-pkg.spec")
	content, err := os.ReadFile(brokenSpecPath)
	require.NoError(t, err)
	assert.Equal(t, "this is not a valid spec file\n", string(content))
}

// TestRenderLocalSpecPreservesAutorelease verifies that rendering a local
// component applies overlays without expanding rpmautospec macros.
//
// Existing render tests never commit lock files, so buildSyntheticCommits
// finds no lock at HEAD and returns early. This test pre-bakes a lock file
// with a stale fingerprint and includes an overlay that changes the runtime
// fingerprint — exercising both the synthetic history pipeline and dirty
// detection in one pass.
func TestRenderLocalSpecPreservesAutorelease(t *testing.T) {
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

	renderedSpecPath := results.GetProjectOutputPath("SPECS", "s", "synth-local", "synth-local.spec")
	require.FileExists(t, renderedSpecPath)

	content, err := os.ReadFile(renderedSpecPath)
	require.NoError(t, err)

	contentStr := string(content)

	assert.Contains(t, contentStr, "Release: %autorelease")
	assert.NotContains(t, contentStr, "## START: Set by rpmautospec")

	// The overlay should be applied to the rendered spec, confirming that
	// dirty detection fired (the overlay changes the runtime fingerprint).
	assert.Contains(t, contentStr, "BuildRequires: dirty-dep",
		"Overlay should be applied to rendered spec")
}
