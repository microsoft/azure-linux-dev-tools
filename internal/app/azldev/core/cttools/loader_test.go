// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cttools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/cttools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_SimpleFile(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `
[distros.testdistro]
description = "Test Distro"
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)
	require.Contains(t, config.Distros, "testdistro")
	assert.Equal(t, "Test Distro", config.Distros["testdistro"].Description)
}

func TestLoadConfig_IncludeResolution(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `
include = ["sub.toml"]

[distros.testdistro]
description = "Test Distro"
`)

	writeFile(t, filepath.Join(dir, "sub.toml"), `
[mock-options-templates.rpm]
options = ["opt1", "opt2"]
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)

	require.Contains(t, config.Distros, "testdistro")
	require.Contains(t, config.MockOptionsTemplates, "rpm")
	assert.Equal(t, []string{"opt1", "opt2"}, config.MockOptionsTemplates["rpm"].Options)
}

func TestLoadConfig_NestedIncludes(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))

	writeFile(t, filepath.Join(dir, "main.toml"), `
include = ["sub/mid.toml"]

[distros.d]
description = "D"
`)

	writeFile(t, filepath.Join(dir, "sub", "mid.toml"), `
include = ["leaf.toml"]

[mock-options-templates.rpm]
options = ["a"]
`)

	writeFile(t, filepath.Join(dir, "sub", "leaf.toml"), `
[build-root-templates.srpm]
packages = ["bash"]
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d")
	require.Contains(t, config.MockOptionsTemplates, "rpm")
	require.Contains(t, config.BuildRootTemplates, "srpm")
	assert.Equal(t, []string{"bash"}, config.BuildRootTemplates["srpm"].Packages)
}

func TestLoadConfig_GlobIncludes(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))

	writeFile(t, filepath.Join(dir, "main.toml"), `
include = ["templates/*.toml"]
`)

	writeFile(t, filepath.Join(dir, "templates", "mock.toml"), `
[mock-options-templates.rpm]
options = ["opt1"]
`)

	writeFile(t, filepath.Join(dir, "templates", "build.toml"), `
[build-root-templates.srpm]
packages = ["bash"]
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)

	require.Contains(t, config.MockOptionsTemplates, "rpm")
	require.Contains(t, config.BuildRootTemplates, "srpm")
}

func TestLoadConfig_DeepMerge_MapsMerge(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `
include = ["extra.toml"]

[distros.d1]
description = "D1"
`)

	writeFile(t, filepath.Join(dir, "extra.toml"), `
[distros.d2]
description = "D2"
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d1")
	require.Contains(t, config.Distros, "d2")
}

func TestLoadConfig_DeepMerge_ArraysConcatenate(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `
include = ["extra.toml"]

[[distros.d.shadow-allowlists]]
tag-name = "tag1"
`)

	writeFile(t, filepath.Join(dir, "extra.toml"), `
[[distros.d.shadow-allowlists]]
tag-name = "tag2"
`)

	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d")

	allowlists := config.Distros["d"].ShadowAllowlists
	require.Len(t, allowlists, 2)
	assert.Equal(t, "tag1", allowlists[0].TagName)
	assert.Equal(t, "tag2", allowlists[1].TagName)
}

func TestLoadConfig_CircularInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "a.toml"), `include = ["b.toml"]`)
	writeFile(t, filepath.Join(dir, "b.toml"), `include = ["a.toml"]`)

	_, err := cttools.LoadConfig(filepath.Join(dir, "a.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular include")
}

func TestLoadConfig_MissingInclude(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `include = ["nonexistent.toml"]`)

	// Glob returns no matches for nonexistent files, so this should succeed with empty config.
	config, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.NoError(t, err)
	assert.Empty(t, config.Distros)
}

func TestLoadConfig_InvalidTOML(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `this is not valid toml {{{`)

	_, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse TOML")
}

func TestLoadConfig_InvalidIncludeType(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "main.toml"), `include = 42`)

	_, err := cttools.LoadConfig(filepath.Join(dir, "main.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an array")
}

// writeFile is a test helper that writes content to a file, creating it if needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
