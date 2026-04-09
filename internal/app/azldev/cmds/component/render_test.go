// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRenderCmd(t *testing.T) {
	cmd := componentcmds.NewRenderCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "render", cmd.Use)
	assert.NotNil(t, cmd.RunE)
}

func TestNewRenderCmd_Flags(t *testing.T) {
	cmd := componentcmds.NewRenderCmd()

	outputDirFlag := cmd.Flags().Lookup("output-dir")
	require.NotNil(t, outputDirFlag, "output-dir flag should be registered")
	assert.Equal(t, "o", outputDirFlag.Shorthand)
	assert.Empty(t, outputDirFlag.DefValue)

	allFlag := cmd.Flags().Lookup("all-components")
	require.NotNil(t, allFlag, "all-components flag should be registered")

	componentFlag := cmd.Flags().Lookup("component")
	require.NotNil(t, componentFlag, "component flag should be registered")

	failOnErrorFlag := cmd.Flags().Lookup("fail-on-error")
	require.NotNil(t, failOnErrorFlag, "fail-on-error flag should be registered")
	assert.Equal(t, "false", failOnErrorFlag.DefValue)

	forceFlag := cmd.Flags().Lookup("force")
	require.NotNil(t, forceFlag, "force flag should be registered")
	assert.Equal(t, "false", forceFlag.DefValue)

	cleanStaleFlag := cmd.Flags().Lookup("clean-stale")
	require.NotNil(t, cleanStaleFlag, "clean-stale flag should be registered")
	assert.Equal(t, "false", cleanStaleFlag.DefValue)
}

func TestRenderCmd_NoComponents(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-o", "SPECS", "nonexistent-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	// We expect an error because no components match (not the output-dir error).
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

func TestRenderCmd_NoOutputDir(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-a"})

	err := cmd.ExecuteContext(testEnv.Env)

	// Without config rendered-specs-dir or -o, render should fail.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no output directory configured")
}

func TestRenderCmd_CleanStaleRequiresAll(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	cmd := componentcmds.NewRenderCmd()
	cmd.SetArgs([]string{"-o", "SPECS", "--clean-stale", "some-component"})

	err := cmd.ExecuteContext(testEnv.Env)

	// --clean-stale without -a should fail.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--clean-stale requires -a")
}
