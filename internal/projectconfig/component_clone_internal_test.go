// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComponentClonePreservesBackReferences(t *testing.T) {
	source, pinSource := &ConfigFile{}, &ConfigFile{}
	config := ComponentConfig{
		SourceConfigFile: source, upstreamCommitConfigFile: pinSource,
		GoVendor: &GoVendorConfig{Enabled: lo.ToPtr(true), PrepPackages: []string{"git"}},
		Build:    ComponentBuildConfig{Defines: map[string]string{"feature": "1"}},
	}
	source.Components = map[string]ComponentConfig{"app": config}
	pinSource.Components = source.Components

	resolved := config.WithAbsolutePaths("/project")
	require.Same(t, source, resolved.SourceConfigFile)
	require.Same(t, pinSource, resolved.upstreamCommitConfigFile)
	require.NotSame(t, config.GoVendor, resolved.GoVendor)
	require.Equal(t, config.GoVendor, resolved.GoVendor)
	resolved.Build.Defines["feature"] = "2"
	*resolved.GoVendor.Enabled = false
	resolved.GoVendor.PrepPackages[0] = "changed"
	assert.True(t, config.GoVendor.IsEnabled())

	assert.Equal(t, "1", config.Build.Defines["feature"])
	assert.Equal(t, []string{"git"}, config.GoVendor.PrepPackages)
}
