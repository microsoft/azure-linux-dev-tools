// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package component_test

import (
	"testing"

	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev"
	componentcmds "github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/cmds/component"
	"github.com/microsoft/azure-linux-dev-tools/internal/app/azldev/core/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCheckCmd(t *testing.T) {
	cmd := componentcmds.NewCheckCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "check", cmd.Use)
	assert.NotNil(t, cmd.RunE)
}

func TestNewCheckCmdFlags(t *testing.T) {
	cmd := componentcmds.NewCheckCmd()

	evrFlag := cmd.Flags().Lookup("evr")
	require.NotNil(t, evrFlag, "--evr flag should be registered")
	assert.Equal(t, "false", evrFlag.DefValue)

	fromFlag := cmd.Flags().Lookup("from")
	require.NotNil(t, fromFlag, "--from flag should be registered")
	assert.Empty(t, fromFlag.DefValue, "--from should be optional")

	toFlag := cmd.Flags().Lookup("to")
	require.NotNil(t, toFlag, "--to flag should be registered")
	assert.Equal(t, "HEAD", toFlag.DefValue, "--to should default to HEAD")

	allComponentsFlag := cmd.Flags().Lookup("all-components")
	require.NotNil(t, allComponentsFlag, "--all-components flag should be registered")
}

// TestNewCheckCmdHasNoRemovedModeFlags pins the collapse of the three former mode flags into
// '--evr'. The removed flags must not reappear as hidden aliases.
func TestNewCheckCmdHasNoRemovedModeFlags(t *testing.T) {
	cmd := componentcmds.NewCheckCmd()

	for _, removed := range []string{"release-counters", "rebuild-readiness"} {
		assert.Nil(t, cmd.Flags().Lookup(removed), "--%s should no longer be registered", removed)
	}
}

func TestCheckCmdRequiresAMode(t *testing.T) {
	env := testutils.NewTestEnv(t)
	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"-p", "nss"})

	err := cmd.ExecuteContext(env.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--evr")
}

func TestCheckCmdRejectsDisabledEVRMode(t *testing.T) {
	env := testutils.NewTestEnv(t)

	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"--evr=false", "--from", "HEAD"})

	err := cmd.ExecuteContext(env.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--evr")
}

// TestCheckCmdRejectsToWithoutFrom proves '--to' is only meaningful for the cross-ref check, so
// a caller cannot silently believe it constrained a run that never used it.
func TestCheckCmdRejectsToWithoutFrom(t *testing.T) {
	env := testutils.NewTestEnv(t)
	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"--evr", "--to", "HEAD", "-p", "nss"})

	err := cmd.ExecuteContext(env.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'--to' requires '--from'")
}

// TestCheckCmdEVRWithoutFromIsAccepted proves '--from' is optional: the run proceeds past option
// validation into component resolution instead of rejecting the invocation.
func TestCheckCmdEVRWithoutFromIsAccepted(t *testing.T) {
	env := testutils.NewTestEnv(t)
	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"--evr", "nonexistent-component"})

	err := cmd.ExecuteContext(env.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
	assert.NotContains(t, err.Error(), "--from")
}

func TestCheckCmdNoComponents(t *testing.T) {
	env := testutils.NewTestEnv(t)

	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"--evr", "--from", "HEAD", "nonexistent-component"})

	err := cmd.ExecuteContext(env.Env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "component not found")
}

func TestCheckCmdRequiresProjectDirectory(t *testing.T) {
	testEnv := testutils.NewTestEnv(t)

	envOptions := azldev.NewEnvOptions()
	envOptions.DryRunnable = testEnv.DryRunnable
	envOptions.EventListener = testEnv.EventListener
	envOptions.Interfaces = testEnv.TestInterfaces
	envOptions.Config = testEnv.Config
	missingProjectEnv := azldev.NewEnv(t.Context(), envOptions)

	cmd := componentcmds.NewCheckCmd()
	cmd.SetArgs([]string{"--evr", "--from", "HEAD", "-p", "nss"})

	err := cmd.ExecuteContext(missingProjectEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid project and configuration")
}
