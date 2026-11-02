// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoVendorConfigFileDeclarationAndInheritance(t *testing.T) {
	ctx := testctx.NewCtx()

	files := map[string]string{
		"/project/azldev.toml": `includes = ["defaults/group.toml", "components/app.comp.toml"]`,
		"/project/defaults/group.toml": `
[component-groups.base]
components = ["app"]
[component-groups.base.default-component-config.go-vendor]
enabled = true
config-file = "go-vendor-tools.toml"
`,
		"/project/components/app.comp.toml": `[components.app]`,
	}
	for path, content := range files {
		require.NoError(t, fileutils.WriteFile(ctx.FS(), path, []byte(content), fileperms.PublicFile))
	}

	for _, withoutLockfile := range []bool{false, true} {
		config, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{withoutLockfile: withoutLockfile},
			"/project/azldev.toml")
		require.NoError(t, err)
		resolved, err := ResolveComponentConfig(config.Components["app"], config.DefaultComponentConfig,
			ComponentConfig{}, config.ComponentGroups, []string{"base"})
		require.NoError(t, err)
		assert.Equal(t, "/project/defaults/go-vendor-tools.toml", resolved.GoVendor.ConfigFile)
		assert.True(t, resolved.GoVendor.IsEnabled())
	}
}

func TestGoVendorInlineConfigurationIsRejected(t *testing.T) {
	for _, contents := range []string{
		"[components.app.go-vendor]\nenabled = true\ntool-config = '[archive]'",
		"[components.app.go-vendor.tool-config.archive]\nuse_module_proxy = true",
	} {
		ctx := testctx.NewCtx()
		require.NoError(t, fileutils.WriteFile(ctx.FS(), "/project/azldev.toml", []byte(contents), fileperms.PublicFile))
		_, err := loadAndResolveProjectConfig(ctx.FS(), loadOptions{}, "/project/azldev.toml")
		require.Error(t, err)
	}
}
