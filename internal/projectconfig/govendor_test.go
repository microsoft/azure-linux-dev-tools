// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/mitchellh/hashstructure/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoVendorConfig(t *testing.T) {
	var config projectconfig.ComponentConfig
	require.NoError(t, toml.Unmarshal([]byte(`
[go-vendor]
enabled = true
module-dir = "cmd/app"
prep-packages = ["git"]
config-file = "go-vendor-tools.toml"
`), &config))
	require.True(t, config.GoVendor.IsEnabled())
	require.NoError(t, config.GoVendor.Validate())
	resolved := config.WithAbsolutePaths("/project")
	assert.Equal(t, filepath.Join("/project", "go-vendor-tools.toml"), resolved.GoVendor.ConfigFile)
	assert.Equal(t, "go-vendor-tools.toml", config.GoVendor.ConfigFile)
	assert.Equal(t, resolved.GoVendor, resolved.WithAbsolutePaths("/elsewhere").GoVendor)
	require.NoError(t, config.MergeUpdatesFrom(&projectconfig.ComponentConfig{
		GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(false)},
	}))
	assert.False(t, config.GoVendor.IsEnabled(), "explicit false must override an inherited true")
}

func TestGoVendorConfigValidation(t *testing.T) {
	for _, config := range []*projectconfig.GoVendorConfig{
		{ModuleDir: "../outside"},
		{ModuleDir: "/absolute"},
		{ModuleDir: "%{macro}"},
		{ConfigFile: "bad\x00path"},
		{ConfigFile: "bad\npath"},
		{PrepPackages: []string{"--bad"}},
		{PrepPackages: []string{""}},
	} {
		require.Error(t, config.Validate())
	}

	var absent *projectconfig.GoVendorConfig
	assert.False(t, absent.IsEnabled())
	require.NoError(t, absent.Validate())
}

func TestGoVendorFingerprint(t *testing.T) {
	empty := projectconfig.ComponentConfig{}
	included, err := empty.HashInclude("GoVendor", nil)
	require.NoError(t, err)
	assert.False(t, included, "absent vendoring must not invalidate existing component fingerprints")
	included, err = empty.HashInclude("Build", nil)
	require.NoError(t, err)
	assert.True(t, included)

	base := projectconfig.ComponentConfig{GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(true)}}
	original, err := hashstructure.Hash(base, hashstructure.FormatV2, &hashstructure.HashOptions{TagName: "fingerprint"})
	require.NoError(t, err)

	for _, config := range []*projectconfig.GoVendorConfig{
		{Enabled: lo.ToPtr(false)},
		{Enabled: lo.ToPtr(true), ModuleDir: "sub"},
		{Enabled: lo.ToPtr(true), PrepPackages: []string{"git"}},
	} {
		changed := base
		changed.GoVendor = config
		hash, err := hashstructure.Hash(changed, hashstructure.FormatV2, &hashstructure.HashOptions{TagName: "fingerprint"})
		require.NoError(t, err)
		assert.NotEqual(t, original, hash)
	}
}

func TestGoVendorConfigFileContentIdentity(t *testing.T) {
	ctx := testctx.NewCtx()

	var absent *projectconfig.GoVendorConfig

	identity, err := absent.SourceContentIdentity(ctx.FS())
	require.NoError(t, err)
	assert.Empty(t, identity)

	config := &projectconfig.GoVendorConfig{}
	identity, err = config.SourceContentIdentity(ctx.FS())
	require.NoError(t, err)
	assert.Empty(t, identity)

	config.ConfigFile = "/project/vendor.toml"
	_, err = config.SourceContentIdentity(ctx.FS())
	require.ErrorIs(t, err, os.ErrNotExist)

	for _, contents := range []string{"", "# reviewed\n[archive]\nuse_module_proxy = true\n"} {
		require.NoError(t, fileutils.WriteFile(ctx.FS(), config.ConfigFile, []byte(contents), fileperms.PublicFile))
		actual, err := config.ReadConfigFile(ctx.FS())
		require.NoError(t, err)
		assert.Equal(t, contents, actual)

		identity, err = config.SourceContentIdentity(ctx.FS())
		require.NoError(t, err)
		expected, err := fileutils.ComputeFileHash(ctx.FS(), fileutils.HashTypeSHA256, config.ConfigFile)
		require.NoError(t, err)
		assert.Equal(t, expected, identity)
	}
}

func TestGoVendorConfigFileValidation(t *testing.T) {
	for _, contents := range []string{"[", "[general]\ngo_mod_dir = 'subdir'"} {
		ctx := testctx.NewCtx()
		require.NoError(t, fileutils.WriteFile(ctx.FS(), "/project/vendor.toml", []byte(contents), fileperms.PublicFile))

		config := projectconfig.GoVendorConfig{ConfigFile: "/project/vendor.toml"}
		_, err := config.ReadConfigFile(ctx.FS())
		require.Error(t, err)
		assert.Contains(t, err.Error(), config.ConfigFile)
	}
}
