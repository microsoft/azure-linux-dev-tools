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

func TestFindProjectRootAndConfigFile_NonExistentDir(t *testing.T) {
	ctx := testctx.NewCtx()

	projectRoot, configPath, err := projectconfig.FindProjectRootAndConfigFile(ctx.FS(), "/non/existent")
	require.Error(t, err)
	assert.Empty(t, projectRoot)
	assert.Empty(t, configPath)
}

func TestFindProjectRootAndConfigFile_EmptyDir(t *testing.T) {
	const testSpecifiedDir = "/project"

	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), testSpecifiedDir))

	projectRoot, configPath, err := projectconfig.FindProjectRootAndConfigFile(ctx.FS(), testSpecifiedDir)
	require.Error(t, err)
	assert.Empty(t, projectRoot)
	assert.Empty(t, configPath)
}

func TestFindProjectRootAndConfigFile_ConfigFilePresent(t *testing.T) {
	const testSpecifiedDir = "/project"

	subdirPath := filepath.Join(testSpecifiedDir, "subdir")

	// Set up dir with config file.
	ctx := testctx.NewCtx()
	require.NoError(t, fileutils.MkdirAll(ctx.FS(), subdirPath))

	configFilePath := filepath.Join(testSpecifiedDir, projectconfig.DefaultConfigFileName)
	require.NoError(t, fileutils.WriteFile(ctx.FS(), configFilePath, []byte{}, fileperms.PrivateFile))

	// Make sure we can find everything when directly handed the root dir.
	t.Run("root dir", func(t *testing.T) {
		projectRoot, configPath, err := projectconfig.FindProjectRootAndConfigFile(ctx.FS(), testSpecifiedDir)
		require.NoError(t, err)
		assert.Equal(t, testSpecifiedDir, projectRoot)
		assert.Equal(t, configFilePath, configPath)
	})

	// Make sure we can find everything when handed a subdir.
	t.Run("subdir", func(t *testing.T) {
		projectRoot, configPath, err := projectconfig.FindProjectRootAndConfigFile(ctx.FS(), subdirPath)
		require.NoError(t, err)
		assert.Equal(t, testSpecifiedDir, projectRoot)
		assert.Equal(t, configFilePath, configPath)
	})
}
