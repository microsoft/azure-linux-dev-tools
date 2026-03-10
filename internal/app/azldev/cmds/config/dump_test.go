// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config_test

import (
	"context"
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/config"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/microsoft/azure-linux-dev-tools/internal/projectconfig"
	"github.com/stretchr/testify/require"
)

func TestDumpConfig(t *testing.T) {
	const (
		testProjectRoot = "/non/existent/dir"
		testLogDir      = "/non/existent/logs"
		testWorkDir     = "/non/existent/work"
		testOutputDir   = "/non/existent/output"
	)

	cfg := &projectconfig.ProjectConfig{
		Project: projectconfig.ProjectInfo{
			LogDir:    testLogDir,
			WorkDir:   testWorkDir,
			OutputDir: testOutputDir,
		},
	}

	ctx, cancelFunc := context.WithCancel(t.Context())
	defer cancelFunc()

	testEnv := testutils.NewTestEnv(t)

	options := azldev.NewEnvOptions()
	options.DryRunnable = testEnv.DryRunnable
	options.EventListener = testEnv.EventListener
	options.Interfaces = testEnv.TestInterfaces
	options.ProjectDir = testProjectRoot
	options.Config = cfg

	env := azldev.NewEnv(ctx, options)

	configText, err := config.DumpConfig(env, config.ConfigDumpFormatTOML)
	require.NoError(t, err)
	require.NotEmpty(t, configText)

	configText, err = config.DumpConfig(env, config.ConfigDumpFormatJSON)
	require.NoError(t, err)
	require.NotEmpty(t, configText)
}
