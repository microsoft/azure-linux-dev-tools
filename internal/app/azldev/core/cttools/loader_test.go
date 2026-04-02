// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cttools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/cttools"
	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConfigDir = "/testconfig"

func TestLoadConfig_SimpleFile(t *testing.T) {
	ctx := testctx.NewCtx()
	mainPath := filepath.Join(testConfigDir, "main.toml")

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
[distros.testdistro]
description = "Test Distro"
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)
	require.Contains(t, config.Distros, "testdistro")
	assert.Equal(t, "Test Distro", config.Distros["testdistro"].Description)
}

func TestLoadConfig_IncludeResolution(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
include = ["sub.toml"]

[distros.testdistro]
description = "Test Distro"
`), fileperms.PrivateFile))

	subPath := filepath.Join(testConfigDir, "sub.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), subPath, []byte(`
[mock-options-templates.rpm]
options = ["opt1", "opt2"]
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)

	require.Contains(t, config.Distros, "testdistro")
	require.Contains(t, config.MockOptionsTemplates, "rpm")
	assert.Equal(t, []string{"opt1", "opt2"}, config.MockOptionsTemplates["rpm"].Options)
}

func TestLoadConfig_NestedIncludes(t *testing.T) {
	ctx := testctx.NewCtx()
	subDir := filepath.Join(testConfigDir, "sub")

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), subDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
include = ["sub/mid.toml"]

[distros.d]
description = "D"
`), fileperms.PrivateFile))

	midPath := filepath.Join(subDir, "mid.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), midPath, []byte(`
include = ["leaf.toml"]

[mock-options-templates.rpm]
options = ["a"]
`), fileperms.PrivateFile))

	leafPath := filepath.Join(subDir, "leaf.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), leafPath, []byte(`
[build-root-templates.srpm]
packages = ["bash"]
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d")
	require.Contains(t, config.MockOptionsTemplates, "rpm")
	require.Contains(t, config.BuildRootTemplates, "srpm")
	assert.Equal(t, []string{"bash"}, config.BuildRootTemplates["srpm"].Packages)
}

func TestLoadConfig_GlobIncludes(t *testing.T) {
	ctx := testctx.NewCtx()
	tmplDir := filepath.Join(testConfigDir, "templates")

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), tmplDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
include = ["templates/*.toml"]
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(tmplDir, "mock.toml"), []byte(`
[mock-options-templates.rpm]
options = ["opt1"]
`), fileperms.PrivateFile))

	require.NoError(t, fileutils.WriteFile(ctx.FS(), filepath.Join(tmplDir, "build.toml"), []byte(`
[build-root-templates.srpm]
packages = ["bash"]
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)

	require.Contains(t, config.MockOptionsTemplates, "rpm")
	require.Contains(t, config.BuildRootTemplates, "srpm")
}

func TestLoadConfig_DeepMerge_MapsMerge(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
include = ["extra.toml"]

[distros.d1]
description = "D1"
`), fileperms.PrivateFile))

	extraPath := filepath.Join(testConfigDir, "extra.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), extraPath, []byte(`
[distros.d2]
description = "D2"
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d1")
	require.Contains(t, config.Distros, "d2")
}

func TestLoadConfig_DeepMerge_ArraysConcatenate(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`
include = ["extra.toml"]

[[distros.d.shadow-allowlists]]
tag-name = "tag1"
`), fileperms.PrivateFile))

	extraPath := filepath.Join(testConfigDir, "extra.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), extraPath, []byte(`
[[distros.d.shadow-allowlists]]
tag-name = "tag2"
`), fileperms.PrivateFile))

	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)

	require.Contains(t, config.Distros, "d")

	allowlists := config.Distros["d"].ShadowAllowlists
	require.Len(t, allowlists, 2)
	assert.Equal(t, "tag1", allowlists[0].TagName)
	assert.Equal(t, "tag2", allowlists[1].TagName)
}

func TestLoadConfig_CircularInclude(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	aPath := filepath.Join(testConfigDir, "a.toml")
	bPath := filepath.Join(testConfigDir, "b.toml")

	require.NoError(t, fileutils.WriteFile(ctx.FS(), aPath, []byte(`include = ["b.toml"]`), fileperms.PrivateFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), bPath, []byte(`include = ["a.toml"]`), fileperms.PrivateFile))

	_, err := cttools.LoadConfig(ctx.FS(), aPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular include")
}

func TestLoadConfig_MissingInclude(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	content := []byte(`include = ["nonexistent.toml"]`)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, content, fileperms.PrivateFile))

	// Non-glob include that doesn't exist should produce an error.
	_, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadConfig_MissingGlobInclude(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	content := []byte(`include = ["nonexistent/*.toml"]`)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, content, fileperms.PrivateFile))

	// Glob pattern with no matches should silently succeed.
	config, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.NoError(t, err)
	assert.Empty(t, config.Distros)
}

func TestLoadConfig_InvalidTOML(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	content := []byte(`this is not valid toml {{{`)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, content, fileperms.PrivateFile))

	_, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse TOML")
}

func TestLoadConfig_InvalidIncludeType(t *testing.T) {
	ctx := testctx.NewCtx()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testConfigDir))

	mainPath := filepath.Join(testConfigDir, "main.toml")
	require.NoError(t, fileutils.WriteFile(ctx.FS(), mainPath, []byte(`include = 42`), fileperms.PrivateFile))

	_, err := cttools.LoadConfig(ctx.FS(), mainPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an array")
}
