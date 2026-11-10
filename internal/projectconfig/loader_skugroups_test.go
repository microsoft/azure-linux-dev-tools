// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

//nolint:testpackage // Exercises the private loadAndResolveProjectConfig loader.
package projectconfig

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfigFiles(t *testing.T, files []struct{ path, contents string }) *testctx.TestCtx {
	t.Helper()

	ctx := testctx.NewCtx()
	for _, file := range files {
		require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(file.path)))
		require.NoError(t, fileutils.WriteFile(ctx.FS(), file.path, []byte(file.contents), fileperms.PrivateFile))
	}

	return ctx
}

func TestLoadAndResolveProjectConfig_SKUGroupsAndVMSKUsMergedAcrossIncludes(t *testing.T) {
	ctx := writeConfigFiles(t, []struct{ path, contents string }{
		{testConfigPath, `
includes = ["skus-a.toml", "skus-b.toml"]
`},
		{"/project/skus-a.toml", `
[sku-groups.amd]
arch = "amd64"
vm-sizes = ["Standard_D4s_v5"]

[vm-skus.Standard_D4s_v5]
vcpus = 4
`},
		{"/project/skus-b.toml", `
[sku-groups.arm]
arch = "arm64"
vm-sizes = ["Standard_D4ps_v5"]

[vm-skus.Standard_D4ps_v5]
vcpus = 4
`},
	})

	config, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{}, testConfigPath)
	require.NoError(t, err)

	require.Contains(t, config.SKUGroups, "amd")
	require.Contains(t, config.SKUGroups, "arm")
	assert.Equal(t, []string{"Standard_D4s_v5"}, config.SKUGroups["amd"].VMSizes)
	assert.Equal(t, SKUArchARM64, config.SKUGroups["arm"].Arch)
	require.Contains(t, config.VMSKUs, "Standard_D4s_v5")
	require.Contains(t, config.VMSKUs, "Standard_D4ps_v5")
}

func TestLoadAndResolveProjectConfig_DuplicateSKUGroupAcrossIncludes(t *testing.T) {
	ctx := writeConfigFiles(t, []struct{ path, contents string }{
		{testConfigPath, `
includes = ["a.toml", "b.toml"]
`},
		{"/project/a.toml", `
[sku-groups.perf]
arch = "amd64"
vm-sizes = ["Standard_D4s_v5"]
`},
		{"/project/b.toml", `
[sku-groups.perf]
arch = "arm64"
vm-sizes = ["Standard_D4ps_v5"]
`},
	})

	_, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{}, testConfigPath)
	require.ErrorIs(t, err, ErrDuplicateSKUGroups)
}

func TestLoadAndResolveProjectConfig_DuplicateVMSKUAcrossIncludes(t *testing.T) {
	ctx := writeConfigFiles(t, []struct{ path, contents string }{
		{testConfigPath, `
includes = ["a.toml", "b.toml"]
`},
		{"/project/a.toml", `
[vm-skus.Standard_D4s_v5]
vcpus = 4
`},
		{"/project/b.toml", `
[vm-skus.Standard_D4s_v5]
vcpus = 8
`},
	})

	_, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{}, testConfigPath)
	require.ErrorIs(t, err, ErrDuplicateVMSKU)
}

// TestLoadAndResolveProjectConfig_EmptySKUGroupsListRejected confirms a literal
// 'sku-groups = []' reaches validation as a non-nil empty slice and is rejected.
func TestLoadAndResolveProjectConfig_EmptySKUGroupsListRejected(t *testing.T) {
	ctx := writeConfigFiles(t, []struct{ path, contents string }{
		{testConfigPath, `
[tests.smoke]
type = "pytest"
[tests.smoke.pytest]
working-dir = "tests"
test-paths = ["test_smoke.py"]

[images.base]
description = "base"
definition = { type = "kiwi", path = "base.kiwi" }
[images.base.tests]
tests = [{ name = "smoke", sku-groups = [] }]
`},
	})

	_, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{}, testConfigPath)
	require.ErrorIs(t, err, ErrInvalidTestRef)
	require.ErrorContains(t, err, "empty 'sku-groups'")
}
