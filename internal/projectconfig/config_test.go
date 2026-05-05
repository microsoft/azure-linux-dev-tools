// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package projectconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/global/testctx"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileperms"
	"github.com/microsoft/azure-linux-dev-tools/internal/utils/fileutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProjectDir  = "/test/project"
	testXDGHome     = "/test/xdghome"
	testProjectDesc = "from project"
	testUserDesc    = "from user"
)

// newTestCtxWithXDGConfigHome returns a [*testctx.TestCtx] whose [opctx.OSEnv] reports
// `XDG_CONFIG_HOME` as [testXDGHome], so that user-config discovery is driven by the
// injected environment abstraction rather than the host process environment.
func newTestCtxWithXDGConfigHome() *testctx.TestCtx {
	osEnv := testctx.NewTestOSEnv()
	osEnv.SetEnv("XDG_CONFIG_HOME", testXDGHome)

	return testctx.NewCtx(testctx.WithOSEnv(osEnv))
}

func writeProjectConfig(t *testing.T, ctx *testctx.TestCtx, contents string) {
	t.Helper()

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testProjectDir))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(testProjectDir, projectconfig.DefaultConfigFileName),
		[]byte(contents), fileperms.PublicFile))
}

// TestLoadProjectConfig_NoUserConfig verifies that loading succeeds (and produces a
// project-derived value) when no user-level config file is present.
func TestLoadProjectConfig_NoUserConfig(t *testing.T) {
	ctx := newTestCtxWithXDGConfigHome()
	writeProjectConfig(t, ctx, `
[project]
description = "`+testProjectDesc+`"
`)

	_, config, err := projectconfig.LoadProjectConfig(
		ctx, ctx.FS(), ctx.OSEnv(), testProjectDir, true /*disableDefaultConfig*/, t.TempDir(), nil, false,
	)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, testProjectDesc, config.Project.Description)
}

// TestLoadProjectConfig_UserConfigOverridesProject verifies that values defined in the
// user-level config file override those defined by the project config file.
func TestLoadProjectConfig_UserConfigOverridesProject(t *testing.T) {
	ctx := newTestCtxWithXDGConfigHome()
	writeProjectConfig(t, ctx, `
[project]
description = "`+testProjectDesc+`"
log-dir = "from-project/logs"
`)

	// Write a user-level config under the XDG config home.
	userConfigPath := projectconfig.UserConfigFilePath(ctx.OSEnv())
	require.Equal(t, filepath.Join(testXDGHome, "azldev", "config.toml"), userConfigPath)
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(userConfigPath)))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), userConfigPath, []byte(`
[project]
description = "`+testUserDesc+`"
output-dir = "/from/user/out"
`), fileperms.PublicFile))

	_, config, err := projectconfig.LoadProjectConfig(
		ctx, ctx.FS(), ctx.OSEnv(), testProjectDir, true /*disableDefaultConfig*/, t.TempDir(), nil, false,
	)
	require.NoError(t, err)
	require.NotNil(t, config)

	// description was set in both files; user config wins because it is loaded last.
	assert.Equal(t, testUserDesc, config.Project.Description)
	// output-dir was only set by the user config — it should be applied.
	assert.Equal(t, "/from/user/out", config.Project.OutputDir)
	// log-dir was only set by the project config — it should not be wiped out by the
	// user config (project merges only fill non-empty fields).
	assert.Equal(t, filepath.Join(testProjectDir, "from-project/logs"), config.Project.LogDir)
}

// TestLoadProjectConfig_ExtraConfigFileOverridesUserConfig verifies that --config-file
// extras (invocation-specific) take precedence over the user-level config (user-specific).
func TestLoadProjectConfig_ExtraConfigFileOverridesUserConfig(t *testing.T) {
	ctx := newTestCtxWithXDGConfigHome()
	writeProjectConfig(t, ctx, `
[project]
description = "`+testProjectDesc+`"
`)

	// Extra --config-file.
	const extraConfigPath = "/test/extra/extra.toml"

	require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(extraConfigPath)))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), extraConfigPath, []byte(`
[project]
description = "from extra"
`), fileperms.PublicFile))

	// User config.
	userConfigPath := projectconfig.UserConfigFilePath(ctx.OSEnv())
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), filepath.Dir(userConfigPath)))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), userConfigPath, []byte(`
[project]
description = "`+testUserDesc+`"
`), fileperms.PublicFile))

	_, config, err := projectconfig.LoadProjectConfig(
		ctx, ctx.FS(), ctx.OSEnv(), testProjectDir, true /*disableDefaultConfig*/, t.TempDir(),
		[]string{extraConfigPath}, false,
	)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, "from extra", config.Project.Description)
}

// TestLoadProjectConfig_UserConfigIncludesAreResolved verifies that the user-level config
// file participates fully in include resolution.
func TestLoadProjectConfig_UserConfigIncludesAreResolved(t *testing.T) {
	ctx := newTestCtxWithXDGConfigHome()
	writeProjectConfig(t, ctx, `
[project]
description = "`+testProjectDesc+`"
`)

	// User config that pulls in another file via includes.
	userConfigPath := projectconfig.UserConfigFilePath(ctx.OSEnv())
	userConfigDir := filepath.Dir(userConfigPath)
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), userConfigDir))
	require.NoError(t, fileutils.WriteFile(ctx.FS(), userConfigPath, []byte(`
includes = ["overrides.toml"]
`), fileperms.PublicFile))
	require.NoError(t, fileutils.WriteFile(ctx.FS(),
		filepath.Join(userConfigDir, "overrides.toml"), []byte(`
[project]
description = "`+testUserDesc+`"
`), fileperms.PublicFile))

	_, config, err := projectconfig.LoadProjectConfig(
		ctx, ctx.FS(), ctx.OSEnv(), testProjectDir, true /*disableDefaultConfig*/, t.TempDir(), nil, false,
	)
	require.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, testUserDesc, config.Project.Description)
}
