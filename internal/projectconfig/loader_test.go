// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Allow to test private functions (i.e., loadAndResolveProjectConfig)
package projectconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // This is effectively a constant, but we need to compute it.
var testConfigPath = filepath.Join("/project", DefaultConfigFileName)

func TestLoadAndResolveProjectConfig(t *testing.T) {
	ctx := testctx.NewCtx()

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, "/non/existent")
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, config)
}

func TestLoadAndResolveProjectConfig_SyntaxError(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte("///"), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Nil(t, config)
}

func TestLoadAndResolveProjectConfig_BadSchema(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath,
		[]byte("[non-existent-section]"), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Nil(t, config)
}

func TestLoadAndResolveProjectConfig_BadSchema_PermissiveParsing(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath,
		[]byte("[non-existent-section]"), fileperms.PrivateFile))

	// With permissive parsing enabled, unknown fields should be silently ignored.
	config, err := loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)
	assert.NotNil(t, config)
}

func TestLoadAndResolveProjectConfig_PermissiveParsing_PreservesKnownFields(t *testing.T) {
	const configContents = `
[project]
description = "my project"

[some-unknown-section]
key = "value"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	// Strict parsing should fail on the unknown section.
	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Nil(t, config)

	// Permissive parsing should succeed and preserve the known fields.
	config, err = loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "my project", config.Project.Description)
}

func TestLoadAndResolveProjectConfig_PermissiveParsing_Includes(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["include.toml"]

[project]
description = "my project"
`},
		{"/project/include.toml", `
[project]
log-dir = "artifacts/logs"

[unknown-section]
key = "value"
`},
	}

	ctx := testctx.NewCtx()

	// Write out files.
	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	// Strict parsing should fail because the included file has an unknown section.
	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.Error(t, err)
	assert.Nil(t, config)

	// Permissive parsing should succeed and resolve fields from both files.
	config, err = loadAndResolveProjectConfig(ctx.FS(), true, testFiles[0].path)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "my project", config.Project.Description)
	assert.Equal(t, "/project/artifacts/logs", config.Project.LogDir)
}

func TestLoadAndResolveProjectConfig_EmptyFile(t *testing.T) {
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte{}, fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Check config
	require.NotNil(t, config)
	assert.Empty(t, config.ComponentGroups)
	assert.Empty(t, config.Components)
	assert.Empty(t, config.Distros)
	assert.Empty(t, config.Project.Description)
	assert.Empty(t, config.Project.LogDir)
	assert.Empty(t, config.Project.OutputDir)
	assert.Empty(t, config.Project.WorkDir)
}

func TestLoadAndResolveProjectConfig_ComponentGroup(t *testing.T) {
	const configContents = `
[component-groups.core]
specs = ["SPECS/**/*.spec"]
`

	configDir := filepath.Dir(testConfigPath)

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Confirm parsed data.
	if assert.Contains(t, config.ComponentGroups, "core") {
		componentGroup := config.ComponentGroups["core"]
		assert.Equal(t, []string{filepath.Join(configDir, "SPECS/**/*.spec")}, componentGroup.SpecPathPatterns)
	}

	require.Len(t, config.ComponentGroups, 1)
}

func TestLoadAndResolveProjectConfig_Component(t *testing.T) {
	const configContents = `
[components.abc]
[components.def]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Confirm parsed data.
	if assert.Contains(t, config.Components, "abc") {
		comp := config.Components["abc"]
		assert.Equal(t, "abc", comp.Name)
	}

	if assert.Contains(t, config.Components, "def") {
		comp := config.Components["def"]
		assert.Equal(t, "def", comp.Name)
	}

	assert.Len(t, config.Components, 2)
}

func TestLoadAndResolveProjectConfig_Distro(t *testing.T) {
	const configContents = `
[distros.abc]
description = "ABC Distro"
default-version = "9.3"

[distros.abc.versions.'9.3']
description = "ABC 9.3"
release-ver = "9.3"
dist-git-branch = "NinePointThree"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	if assert.Contains(t, config.Distros, "abc") {
		distro := config.Distros["abc"]
		assert.Equal(t, "ABC Distro", distro.Description)
		assert.Equal(t, "9.3", distro.DefaultVersion)

		if assert.Contains(t, distro.Versions, "9.3") {
			version := distro.Versions["9.3"]
			assert.Equal(t, "ABC 9.3", version.Description)
			assert.Equal(t, "9.3", version.ReleaseVer)
			assert.Equal(t, "NinePointThree", version.DistGitBranch)
		}

		assert.Len(t, distro.Versions, 1)
	}

	assert.Len(t, config.Distros, 1)
}

func TestLoadAndResolveProjectConfig_Project(t *testing.T) {
	const configContents = `
[project]
description = "my project"
log-dir = "artifacts/logs"
work-dir = "artifacts/work"
output-dir = "out"
`

	configDir := filepath.Dir(testConfigPath)

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Validate config, making sure paths were made absolute.
	assert.Equal(t, "my project", config.Project.Description)
	assert.Equal(t, filepath.Join(configDir, "artifacts/logs"), config.Project.LogDir)
	assert.Equal(t, filepath.Join(configDir, "artifacts/work"), config.Project.WorkDir)
	assert.Equal(t, filepath.Join(configDir, "out"), config.Project.OutputDir)
}

func TestLoadAndResolveProjectConfig_Includes(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["include.toml"]

[project]
description = "my project"
`},
		{"/project/include.toml", `
includes = ["subdir/include.toml"]

[project]
description = "overridden"
`},
		{"/project/subdir/include.toml", `
[project]
log-dir = "artifacts/logs"
`},
	}

	ctx := testctx.NewCtx()

	// Write out files.
	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.NoError(t, err)

	// Validate resolved config.
	assert.Equal(t, "overridden", config.Project.Description)
	assert.Equal(t, "/project/subdir/artifacts/logs", config.Project.LogDir)
}

func TestLoadAndResolveProjectConfig_DuplicateComponents(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["include.toml"]

[components.abc]
`},
		{"/project/include.toml", `
[components.abc]
`},
	}

	ctx := testctx.NewCtx()

	// Write out files.
	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.ErrorIs(t, err, ErrDuplicateComponents)
}

func TestLoadAndResolveProjectConfig_DuplicateComponentGroups(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["include.toml"]

[component-groups.abc]
`},
		{"/project/include.toml", `
[component-groups.abc]
`},
	}

	ctx := testctx.NewCtx()

	// Write out files.
	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.ErrorIs(t, err, ErrDuplicateComponentGroups)
}

func TestLoadAndResolveProjectConfig_MergeDistros(t *testing.T) {
	// First config defines a distro with one version.
	const configContents1 = `
[distros.abc]
description = "ABC Distro"
default-version = "9.3"

[distros.abc.versions.'9.3']
description = "ABC 9.3"
release-ver = "9.3"
dist-git-branch = "NinePointThree"
`

	// Second config redefines the same distro, overriding the description and adding a new version.
	const configContents2 = `
[distros.abc]
description = "ABC Distro (updated)"

[distros.abc.versions.'10.0']
description = "ABC 10.0"
release-ver = "10.0"
dist-git-branch = "TenPointZero"
`

	configPath1 := testConfigPath
	configPath2 := filepath.Join("/project", "extra.toml")

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath1, []byte(configContents1), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath2, []byte(configContents2), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, configPath1, configPath2)
	require.NoError(t, err)

	if assert.Contains(t, config.Distros, "abc") {
		distro := config.Distros["abc"]

		// Description should be overridden by the second config.
		assert.Equal(t, "ABC Distro (updated)", distro.Description)

		// Default version should be preserved from the first config.
		assert.Equal(t, "9.3", distro.DefaultVersion)

		// Both versions should be present (merged).
		assert.Len(t, distro.Versions, 2)

		if assert.Contains(t, distro.Versions, "9.3") {
			version := distro.Versions["9.3"]
			assert.Equal(t, "ABC 9.3", version.Description)
			assert.Equal(t, "9.3", version.ReleaseVer)
			assert.Equal(t, "NinePointThree", version.DistGitBranch)
		}

		if assert.Contains(t, distro.Versions, "10.0") {
			version := distro.Versions["10.0"]
			assert.Equal(t, "ABC 10.0", version.Description)
			assert.Equal(t, "10.0", version.ReleaseVer)
			assert.Equal(t, "TenPointZero", version.DistGitBranch)
		}
	}

	assert.Len(t, config.Distros, 1)
}

func TestLoadAndResolveProjectConfig_DuplicateComponentsAcrossFiles(t *testing.T) {
	// Two separate config files both defining the same component should error.
	// Unlike distros, components do not support merging across config files.
	const configContents1 = `
[components.foo]
`

	const configContents2 = `
[components.foo]
`

	configPath1 := testConfigPath
	configPath2 := filepath.Join("/project", "extra.toml")

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath1, []byte(configContents1), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath2, []byte(configContents2), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, configPath1, configPath2)
	require.ErrorIs(t, err, ErrDuplicateComponents)
}

func TestLoadAndResolveProjectConfig_ComponentGroupWithMembers(t *testing.T) {
	const configContents = `
[component-groups.core]
components = ["foo", "bar"]
description = "Core components"
specs = ["SPECS/**/*.spec"]
`

	configDir := filepath.Dir(testConfigPath)

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// Confirm group parsed correctly.
	if assert.Contains(t, config.ComponentGroups, "core") {
		group := config.ComponentGroups["core"]
		assert.Equal(t, "Core components", group.Description)
		assert.Equal(t, []string{"foo", "bar"}, group.Components)
		assert.Equal(t, []string{filepath.Join(configDir, "SPECS/**/*.spec")}, group.SpecPathPatterns)
	}

	// Confirm GroupsByComponent mapping.
	assert.Contains(t, config.GroupsByComponent, "foo")
	assert.Contains(t, config.GroupsByComponent, "bar")
	assert.Equal(t, []string{"core"}, config.GroupsByComponent["foo"])
	assert.Equal(t, []string{"core"}, config.GroupsByComponent["bar"])
}

func TestLoadAndResolveProjectConfig_GroupsByComponent_MultipleGroups(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["extra.toml"]

[component-groups.alpha]
components = ["shared", "only-alpha"]
`},
		{"/project/extra.toml", `
[component-groups.beta]
components = ["shared", "only-beta"]
`},
	}

	ctx := testctx.NewCtx()
	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.NoError(t, err)

	// "shared" belongs to both groups.
	assert.ElementsMatch(t, []string{"alpha", "beta"}, config.GroupsByComponent["shared"])

	// "only-alpha" belongs to just alpha.
	assert.Equal(t, []string{"alpha"}, config.GroupsByComponent["only-alpha"])

	// "only-beta" belongs to just beta.
	assert.Equal(t, []string{"beta"}, config.GroupsByComponent["only-beta"])
}

func TestLoadAndResolveProjectConfig_ComponentGroupWithDefaultConfig(t *testing.T) {
	const configContents = `
[component-groups.core]
components = ["foo"]

[component-groups.core.default-component-config.build]
with = ["tests"]
without = ["docs"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	if assert.Contains(t, config.ComponentGroups, "core") {
		group := config.ComponentGroups["core"]
		assert.Equal(t, []string{"tests"}, group.DefaultComponentConfig.Build.With)
		assert.Equal(t, []string{"docs"}, group.DefaultComponentConfig.Build.Without)
	}

	// Confirm GroupsByComponent mapping.
	assert.Equal(t, []string{"core"}, config.GroupsByComponent["foo"])
}

func TestLoadAndResolveProjectConfig_GroupsByComponent_EmptyMembers(t *testing.T) {
	const configContents = `
[component-groups.empty]
specs = ["SPECS/**/*.spec"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	// No members means no GroupsByComponent entries.
	assert.Empty(t, config.GroupsByComponent)
}

func TestLoadAndResolveProjectConfig_MissingInclude(t *testing.T) {
	const configContents = `
includes = ["include.toml"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, config)
}

func TestLoadAndResolveProjectConfig_IncludePatternMatchesNothing(t *testing.T) {
	const configContents = `
includes = ["*non-existent*.toml"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)
}

func TestLoadAndResolveProjectConfig_DefaultPackageConfig(t *testing.T) {
	const configContents = `
[default-package-config.publish]
rpm-channel = "base"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	assert.Equal(t, "base", config.DefaultPackageConfig.Publish.RPMChannel)
}

func TestLoadAndResolveProjectConfig_DefaultPackageConfig_MergedAcrossFiles(t *testing.T) {
	// First file sets a channel; second file overrides it.
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["extra.toml"]

[default-package-config.publish]
rpm-channel = "base"
`},
		{"/project/extra.toml", `
[default-package-config.publish]
rpm-channel = "stable"
`},
	}

	ctx := testctx.NewCtx()
	for _, f := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(f.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), f.path, []byte(f.contents), fileperms.PrivateFile))
	}

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.NoError(t, err)

	// The later-loaded file wins.
	assert.Equal(t, "stable", config.DefaultPackageConfig.Publish.RPMChannel)
}

func TestLoadAndResolveProjectConfig_DefaultPackageConfig_MergedAcrossTopLevelFiles(t *testing.T) {
	// Two separate top-level config files; the second one overrides the first.
	const (
		configContents1 = `
[default-package-config.publish]
rpm-channel = "first"
`
		configContents2 = `
[default-package-config.publish]
rpm-channel = "second"
`
	)

	configPath1 := testConfigPath
	configPath2 := filepath.Join("/project", "extra.toml")

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath1, []byte(configContents1), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath2, []byte(configContents2), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, configPath1, configPath2)
	require.NoError(t, err)

	assert.Equal(t, "second", config.DefaultPackageConfig.Publish.RPMChannel)
}

func TestLoadAndResolveProjectConfig_PackageGroups(t *testing.T) {
	const configContents = `
[package-groups.devel-packages]
description = "Development subpackages"
packages = ["curl-devel", "wget2-devel"]

[package-groups.devel-packages.default-package-config.publish]
rpm-channel = "devel"

[package-groups.debug-packages]
description = "Debug info packages"
packages = ["curl-debuginfo", "curl-debugsource"]

[package-groups.debug-packages.default-package-config.publish]
rpm-channel = "none"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	require.Len(t, config.PackageGroups, 2)

	if assert.Contains(t, config.PackageGroups, "devel-packages") {
		g := config.PackageGroups["devel-packages"]
		assert.Equal(t, "Development subpackages", g.Description)
		assert.Equal(t, []string{"curl-devel", "wget2-devel"}, g.Packages)
		assert.Equal(t, "devel", g.DefaultPackageConfig.Publish.RPMChannel)
	}

	if assert.Contains(t, config.PackageGroups, "debug-packages") {
		g := config.PackageGroups["debug-packages"]
		assert.Equal(t, "Debug info packages", g.Description)
		assert.Equal(t, []string{"curl-debuginfo", "curl-debugsource"}, g.Packages)
		assert.Equal(t, "none", g.DefaultPackageConfig.Publish.RPMChannel)
	}
}

func TestLoadAndResolveProjectConfig_DuplicatePackageGroups(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["extra.toml"]

[package-groups.devel-packages]
packages = ["curl-devel"]
`},
		{"/project/extra.toml", `
[package-groups.devel-packages]
packages = ["wget2-devel"]
`},
	}

	ctx := testctx.NewCtx()
	for _, f := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(f.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), f.path, []byte(f.contents), fileperms.PrivateFile))
	}

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.ErrorIs(t, err, ErrDuplicatePackageGroups)
}

func TestLoadAndResolveProjectConfig_DuplicatePackageGroupsAcrossTopLevelFiles(t *testing.T) {
	const (
		configContents1 = `
[package-groups.devel-packages]
packages = ["curl-devel"]
`
		configContents2 = `
[package-groups.devel-packages]
packages = ["wget2-devel"]
`
	)

	configPath1 := testConfigPath
	configPath2 := filepath.Join("/project", "extra.toml")

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath1, []byte(configContents1), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configPath2, []byte(configContents2), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, configPath1, configPath2)
	require.ErrorIs(t, err, ErrDuplicatePackageGroups)
}

func TestLoadAndResolveProjectConfig_PackageGroups_EmptyPackageName(t *testing.T) {
	const configContents = `
[package-groups.bad-group]
packages = ["curl-devel", ""]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "packages[1]")
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestLoadAndResolveProjectConfig_PackageGroups_DuplicatePackageWithinGroup(t *testing.T) {
	const configContents = `
[package-groups.my-group]
packages = ["curl-devel", "wget2-devel", "curl-devel"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "curl-devel")
	assert.Contains(t, err.Error(), "more than once")
}

func TestLoadAndResolveProjectConfig_PackageGroups_DuplicatePackageAcrossGroups(t *testing.T) {
	const configContents = `
[package-groups.group-a]
packages = ["curl-devel", "wget2-devel"]

[package-groups.group-b]
packages = ["wget2-devel", "bash-devel"]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wget2-devel")
	assert.Contains(t, err.Error(), "may only belong to one group")
}

func TestLoadAndResolveProjectConfig_ComponentPackageOverrides(t *testing.T) {
	const configContents = `
[components.curl]

[components.curl.packages.curl-devel.publish]
rpm-channel = "devel"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	if assert.Contains(t, config.Components, "curl") {
		comp := config.Components["curl"]

		if assert.Contains(t, comp.Packages, "curl-devel") {
			assert.Equal(t, "devel", comp.Packages["curl-devel"].Publish.RPMChannel)
		}
	}
}

func TestLoadAndResolveProjectConfig_TestSuite(t *testing.T) {
	const configContents = `
[test-suites.smoke]
description = "Smoke tests for images"

[test-suites.integration]
description = "Integration tests"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	require.Len(t, config.TestSuites, 2)

	if assert.Contains(t, config.TestSuites, "smoke") {
		smokeTest := config.TestSuites["smoke"]
		assert.Equal(t, "smoke", smokeTest.Name)
		assert.Equal(t, "Smoke tests for images", smokeTest.Description)
	}

	if assert.Contains(t, config.TestSuites, "integration") {
		integrationTest := config.TestSuites["integration"]
		assert.Equal(t, "integration", integrationTest.Name)
		assert.Equal(t, "Integration tests", integrationTest.Description)
	}
}

func TestLoadAndResolveProjectConfig_DuplicateTests(t *testing.T) {
	testFiles := []struct {
		path     string
		contents string
	}{
		{testConfigPath, `
includes = ["include.toml"]

[test-suites.smoke]
description = "Smoke tests"
`},
		{"/project/include.toml", `
[test-suites.smoke]
description = "Other smoke tests"
`},
	}

	ctx := testctx.NewCtx()

	for _, testFile := range testFiles {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(testFile.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), testFile.path, []byte(testFile.contents), fileperms.PrivateFile))
	}

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testFiles[0].path)
	require.ErrorIs(t, err, ErrDuplicateTestSuites)
}

func TestLoadAndResolveProjectConfig_ImageWithValidTestRef(t *testing.T) {
	const configContents = `
[test-suites.smoke]
description = "Smoke tests"

[images.myimage]
description = "Test image"

[images.myimage.tests]
test-suites = [{ name = "smoke" }]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.NoError(t, err)

	if assert.Contains(t, config.Images, "myimage") {
		assert.Equal(t, []TestSuiteRef{{Name: "smoke"}}, config.Images["myimage"].Tests.TestSuites)
	}
}

func TestLoadAndResolveProjectConfig_ImageWithInvalidTestRef(t *testing.T) {
	const configContents = `
[images.myimage]
description = "Test image"

[images.myimage.tests]
test-suites = [{ name = "nonexistent" }]
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	_, err := loadAndResolveProjectConfig(ctx.FS(), false, testConfigPath)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUndefinedTestSuite)
	assert.Contains(t, err.Error(), "nonexistent")
}

// TestLoadAndResolveProjectConfig_DeprecatedChannelField_DefaultPackageConfig verifies that the
// deprecated 'publish.channel' field is preserved as-is after loading and used as a fallback
// by the channel resolver via [PackagePublishConfig.EffectiveRPMChannel].
func TestLoadAndResolveProjectConfig_DeprecatedChannelField_DefaultPackageConfig(t *testing.T) {
	const configContents = `
[default-package-config.publish]
channel = "rpm-base"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)

	// The deprecated field is preserved; no migration happens at load time.
	assert.Empty(t, config.DefaultPackageConfig.Publish.RPMChannel)
	assert.Equal(t, "rpm-base", config.DefaultPackageConfig.Publish.DeprecatedChannel)
	// The resolver falls back to the deprecated field when rpm-channel is unset.
	assert.Equal(t, "rpm-base", config.DefaultPackageConfig.Publish.EffectiveRPMChannel())
}

// TestLoadAndResolveProjectConfig_DeprecatedChannelField_PackageGroup verifies that the deprecated
// 'publish.channel' field is preserved and used as a fallback by the channel resolver in
// package-group default-package-config.
func TestLoadAndResolveProjectConfig_DeprecatedChannelField_PackageGroup(t *testing.T) {
	const configContents = `
[package-groups.my-group]
packages = ["curl-devel"]

[package-groups.my-group.default-package-config.publish]
channel = "rpm-sdk"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)

	require.Contains(t, config.PackageGroups, "my-group")
	g := config.PackageGroups["my-group"]
	// The deprecated field is preserved; the resolver falls back to it via EffectiveRPMChannel.
	assert.Empty(t, g.DefaultPackageConfig.Publish.RPMChannel)
	assert.Equal(t, "rpm-sdk", g.DefaultPackageConfig.Publish.DeprecatedChannel)
	assert.Equal(t, "rpm-sdk", g.DefaultPackageConfig.Publish.EffectiveRPMChannel())
}

// TestLoadAndResolveProjectConfig_DeprecatedChannelField_ComponentPackage verifies that the deprecated
// 'publish.channel' field is preserved and used as a fallback by the channel resolver in
// per-package component overrides.
func TestLoadAndResolveProjectConfig_DeprecatedChannelField_ComponentPackage(t *testing.T) {
	const configContents = `
[components.curl]

[components.curl.packages.curl-devel.publish]
channel = "devel"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)

	require.Contains(t, config.Components, "curl")
	comp := config.Components["curl"]
	require.Contains(t, comp.Packages, "curl-devel")
	// The deprecated field is preserved; the resolver falls back to it via EffectiveRPMChannel.
	assert.Empty(t, comp.Packages["curl-devel"].Publish.RPMChannel)
	assert.Equal(t, "devel", comp.Packages["curl-devel"].Publish.DeprecatedChannel)
	assert.Equal(t, "devel", comp.Packages["curl-devel"].Publish.EffectiveRPMChannel())
}

// TestLoadAndResolveProjectConfig_DeprecatedChannelField_NotOverriddenByNewField verifies that when
// both 'publish.channel' (deprecated) and 'publish.rpm-channel' are set, rpm-channel takes precedence
// via [PackagePublishConfig.EffectiveRPMChannel].
func TestLoadAndResolveProjectConfig_DeprecatedChannelField_NotOverriddenByNewField(t *testing.T) {
	const configContents = `
[default-package-config.publish]
channel = "old-channel"
rpm-channel = "new-channel"
`

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.WriteFile(ctx.FS(), testConfigPath, []byte(configContents), fileperms.PrivateFile))

	config, err := loadAndResolveProjectConfig(ctx.FS(), true, testConfigPath)
	require.NoError(t, err)

	// Both fields are preserved as loaded.
	assert.Equal(t, "new-channel", config.DefaultPackageConfig.Publish.RPMChannel)
	assert.Equal(t, "old-channel", config.DefaultPackageConfig.Publish.DeprecatedChannel)
	// rpm-channel takes precedence over the deprecated field.
	assert.Equal(t, "new-channel", config.DefaultPackageConfig.Publish.EffectiveRPMChannel(),
		"rpm-channel should take precedence over the deprecated channel field")
}
