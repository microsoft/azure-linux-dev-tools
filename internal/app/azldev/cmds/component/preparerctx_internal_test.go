// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/components/components_testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestGoVendorPreparerOptions(t *testing.T) {
	const disabled = "disabled"
	for _, mode := range []string{"enabled", disabled, "missing-mock", "missing-distro"} {
		t.Run(mode, func(t *testing.T) {
			env := testutils.NewTestEnv(t)
			component := components_testutils.NewMockComponent(gomock.NewController(t))
			component.EXPECT().GetConfig().AnyTimes().Return(&projectconfig.ComponentConfig{
				GoVendor: &projectconfig.GoVendorConfig{Enabled: lo.ToPtr(mode != disabled)},
			})

			switch mode {
			case "missing-mock", disabled:
				clearProjectMockConfig(env.Env.Config())
			case "missing-distro":
				env.Env.Config().Project.DefaultDistro.Name = "absent"
			}

			options, err := goVendorPreparerOptions(env.Env, component, nil)

			switch mode {
			case "enabled":
				require.NoError(t, err)
				require.Len(t, options, 1)
			case disabled:
				require.NoError(t, err)
				assert.Empty(t, options)
			default:
				require.Error(t, err)
				assert.Empty(t, options)
			}
		})
	}
}

func TestGoVendorHistoryCustomizations(t *testing.T) {
	config := &projectconfig.GoVendorConfig{
		Enabled: lo.ToPtr(true), ModuleDir: "cmd/app",
		ToolConfig: "[archive]\ntidy = false", PrepPackages: []string{"git"},
	}
	assert.Equal(t, []CustomizationItem{
		{Kind: "go-vendor.enabled", Value: "true"},
		{Kind: "go-vendor.module-dir", Value: "cmd/app"},
		{Kind: "go-vendor.tool-config", Value: config.ToolConfig},
		{Kind: "go-vendor.prep-packages", Value: "git"},
	}, collectCustomizations("app", &projectconfig.ComponentConfig{GoVendor: config}))
	assert.Empty(t, appendGoVendorItems(nil, &projectconfig.GoVendorConfig{}))
}
