// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
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
tool-config = '''
[archive.dependency_overrides]
"example.com/module" = "v1.2.3"
'''
`), &config))
	require.True(t, config.GoVendor.IsEnabled())
	require.NoError(t, config.GoVendor.Validate())
	require.Equal(t, config.GoVendor, config.WithAbsolutePaths("/project").GoVendor)
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
		{ToolConfig: "["},
		{ToolConfig: "[general]\ngo_mod_dir = 'subdir'"},
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
		{Enabled: lo.ToPtr(true), ToolConfig: "[archive]\ntidy = false"},
		{Enabled: lo.ToPtr(true), PrepPackages: []string{"git"}},
	} {
		changed := base
		changed.GoVendor = config
		hash, err := hashstructure.Hash(changed, hashstructure.FormatV2, &hashstructure.HashOptions{TagName: "fingerprint"})
		require.NoError(t, err)
		assert.NotEqual(t, original, hash)
	}
}
